package tfc_trigger

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/creasty/defaults"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/utils"
)

// maxListRootsPerRule caps the directory listings one rule makes for one
// request. An MR touching more places than this (a bulk change across many
// services) lists everything the rule could match once instead, which is
// fewer calls than listing each place on its own.
const maxListRootsPerRule = 20

var placeholderRE = regexp.MustCompile(`\{(\d+)\}`)

// DiscoverRule derives workspaces from the repository's directory layout, so a
// repo whose workspaces follow a naming convention does not need one entry per
// workspace. Every directory matching Dir becomes a workspace; {1}, {2}, … in
// Name and TriggerDirs stand for the directory names Dir's first, second, … `*`
// matched. For example, with Dir "services/*/*/" the directory
// "services/api/staging/" fills Name "services-{1}-{2}" as "services-api-staging".
type DiscoverRule struct {
	// Dir is a slash-separated path whose segments are either literal names or
	// `*`, which matches exactly one directory name.
	Dir string `yaml:"dir" validate:"empty=false"`
	// Exclude lists doublestar patterns for directories Dir matches that are
	// not workspaces.
	Exclude      []string `yaml:"exclude,omitempty"`
	Name         string   `yaml:"name" validate:"empty=false"`
	Organization string   `yaml:"organization" validate:"empty=false"`
	Mode         string   `yaml:"mode" default:"apply-before-merge" validate:"one_of=apply-before-merge,merge-before-apply,tfc-vcs-repo"`
	TriggerDirs  []string `yaml:"triggerDirs,omitempty"`
	AutoMerge    bool     `yaml:"autoMerge" default:"true"`
}

func (r *DiscoverRule) UnmarshalYAML(unmarshal func(interface{}) error) error {
	err := defaults.Set(r)
	if err != nil {
		return fmt.Errorf("failed to set defaults for discover rule: %v", err)
	}

	type plain DiscoverRule
	if err := unmarshal((*plain)(r)); err != nil {
		return err
	}

	return nil
}

func (r *DiscoverRule) dirSegments() []string {
	return strings.Split(strings.Trim(r.Dir, "/"), "/")
}

func (r *DiscoverRule) wildcards() int {
	n := 0
	for _, seg := range r.dirSegments() {
		if seg == "*" {
			n++
		}
	}
	return n
}

// validate checks what the struct tags cannot: that Dir is a pattern this
// package can match segment by segment, and that every placeholder refers to
// one of its wildcards.
func (r *DiscoverRule) validate() error {
	for _, seg := range r.dirSegments() {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("discover rule %q: dir must be a relative path without empty, '.' or '..' segments", r.Dir)
		}
		if seg != "*" && strings.ContainsAny(seg, `*?[]{}\`) {
			return fmt.Errorf("discover rule %q: each dir segment must be a literal name or '*', got %q", r.Dir, seg)
		}
	}
	wildcards := r.wildcards()
	if wildcards == 0 {
		return fmt.Errorf("discover rule %q: dir must contain at least one '*'", r.Dir)
	}

	// Every wildcard in the name keeps two directories from naming the same
	// workspace.
	used := map[int]bool{}
	for _, m := range placeholderRE.FindAllStringSubmatch(r.Name, -1) {
		n, _ := strconv.Atoi(m[1])
		used[n] = true
	}
	for n := 1; n <= wildcards; n++ {
		if !used[n] {
			return fmt.Errorf("discover rule %q: name %q must use every wildcard of dir, missing {%d}", r.Dir, r.Name, n)
		}
	}

	for _, tmpl := range append([]string{r.Name}, r.TriggerDirs...) {
		for _, m := range placeholderRE.FindAllStringSubmatch(tmpl, -1) {
			n, _ := strconv.Atoi(m[1])
			if n < 1 || n > wildcards {
				return fmt.Errorf("discover rule %q: %q refers to {%d}, but dir has %d wildcard(s)", r.Dir, tmpl, n, wildcards)
			}
		}
	}
	for _, td := range r.TriggerDirs {
		if !doublestar.ValidatePattern(placeholderRE.ReplaceAllString(td, "*")) {
			return fmt.Errorf("discover rule %q: invalid triggerDir pattern %q", r.Dir, td)
		}
	}
	for _, ex := range r.Exclude {
		if !doublestar.ValidatePattern(strings.Trim(ex, "/")) {
			return fmt.Errorf("discover rule %q: invalid exclude pattern %q", r.Dir, ex)
		}
	}
	return nil
}

// matchDir returns the directory names dir's wildcards matched, if dir
// matches the rule's Dir.
func (r *DiscoverRule) matchDir(dir string) ([]string, bool) {
	pattern := r.dirSegments()
	segs := strings.Split(strings.Trim(dir, "/"), "/")
	if len(segs) != len(pattern) {
		return nil, false
	}
	var captures []string
	for i, seg := range pattern {
		if seg == "*" {
			captures = append(captures, segs[i])
		} else if seg != segs[i] {
			return nil, false
		}
	}
	return captures, true
}

// matchTriggerDir reports whether p matches the triggerDir template td, and
// returns whichever of the rule's wildcards td pins down. A placeholder only
// pins its wildcard when it fills a whole path segment ahead of any `**`;
// elsewhere its position in p is ambiguous and it is left unset ("").
func (r *DiscoverRule) matchTriggerDir(td, p string) ([]string, bool) {
	if match, err := doublestar.Match(placeholderRE.ReplaceAllString(td, "*"), p); err != nil || !match {
		return nil, false
	}
	captures := make([]string, r.wildcards())
	pSegs := strings.Split(p, "/")
	for i, seg := range strings.Split(td, "/") {
		if i >= len(pSegs) || strings.Contains(seg, "**") {
			break
		}
		m := placeholderRE.FindStringSubmatch(seg)
		if m == nil || m[0] != seg {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		captures[n-1] = pSegs[i]
	}
	return captures, true
}

// listRoot returns the directory to list for the workspaces whose wildcards
// match captures: Dir up to its first wildcard that captures leaves unset,
// and never Dir's full depth, so a workspace directory is always inside the
// listing rather than its root. A deleted workspace directory is then simply
// absent from the listing.
func (r *DiscoverRule) listRoot(captures []string) string {
	pattern := r.dirSegments()
	var segs []string
	w := 0
	for _, seg := range pattern[:len(pattern)-1] {
		if seg == "*" {
			if w >= len(captures) || captures[w] == "" {
				break
			}
			seg = captures[w]
			w++
		}
		segs = append(segs, seg)
	}
	return strings.Join(segs, "/")
}

// listRoots returns the directories to list to find every workspace this rule
// derives that modifiedFiles could trigger, through either the workspace's
// own dir or its triggerDirs. Like triggeredWorkspaces, which then decides
// which of the workspaces found are actually triggered, it matches each
// modified file's directory.
func (r *DiscoverRule) listRoots(modifiedFiles []string) []string {
	var roots []string
	for _, mf := range modifiedFiles {
		dir := path.Dir(mf)
		if captures, ok := r.matchDir(dir); ok {
			roots = append(roots, r.listRoot(captures))
		}
		for _, td := range r.TriggerDirs {
			if captures, ok := r.matchTriggerDir(td, dir); ok {
				roots = append(roots, r.listRoot(captures))
			}
		}
	}
	roots = outermostDirs(roots)
	if len(roots) > maxListRootsPerRule {
		return []string{r.listRoot(nil)}
	}
	return roots
}

// outermostDirs dedupes dirs and drops any dir inside another one, since
// listings are recursive.
func outermostDirs(dirs []string) []string {
	var result []string
	for i, d := range dirs {
		covered := false
		for j, other := range dirs {
			if other == d && j < i {
				covered = true
				break
			}
			if other != d && (other == "" || strings.HasPrefix(d, other+"/")) {
				covered = true
				break
			}
		}
		if !covered {
			result = append(result, d)
		}
	}
	return result
}

func (r *DiscoverRule) excluded(dir string) bool {
	for _, ex := range r.Exclude {
		if match, _ := doublestar.Match(strings.Trim(ex, "/"), dir); match {
			return true
		}
	}
	return false
}

func (r *DiscoverRule) workspace(dir string, captures []string) *TFCWorkspace {
	fill := func(tmpl string) string {
		return placeholderRE.ReplaceAllStringFunc(tmpl, func(m string) string {
			n, _ := strconv.Atoi(placeholderRE.FindStringSubmatch(m)[1])
			return captures[n-1]
		})
	}
	ws := &TFCWorkspace{
		Name:         fill(r.Name),
		Organization: r.Organization,
		Dir:          dir + "/",
		Mode:         r.Mode,
		AutoMerge:    r.AutoMerge,
		Discovered:   true,
	}
	for _, td := range r.TriggerDirs {
		ws.TriggerDirs = append(ws.TriggerDirs, fill(td))
	}
	return ws
}

// dirLister returns every directory below dir, recursively, relative to the
// repo root.
type dirLister func(ctx context.Context, dir string) ([]string, error)

// discoverWorkspaces adds the workspaces cfg's discover rules derive to
// cfg.Workspaces, limited to those modifiedFiles could trigger so that an MR
// lists only the directories it touches.
func (cfg *ProjectConfig) discoverWorkspaces(ctx context.Context, list dirLister, modifiedFiles []string) error {
	for _, r := range cfg.Discover {
		if err := cfg.discoverFromRoots(ctx, list, r, r.listRoots(modifiedFiles)); err != nil {
			return err
		}
	}
	return nil
}

// discoverAllWorkspaces adds every workspace cfg's discover rules derive, for
// a workspace named explicitly that the MR's changes do not reach.
func (cfg *ProjectConfig) discoverAllWorkspaces(ctx context.Context, list dirLister) error {
	for _, r := range cfg.Discover {
		if err := cfg.discoverFromRoots(ctx, list, r, []string{r.listRoot(nil)}); err != nil {
			return err
		}
	}
	return nil
}

func (cfg *ProjectConfig) discoverFromRoots(ctx context.Context, list dirLister, r *DiscoverRule, roots []string) error {
	for _, root := range roots {
		dirs, err := list(ctx, root)
		if err != nil {
			return fmt.Errorf("could not list %q for discover rule %q: %w", root, r.Dir, err)
		}
		found := 0
		for _, dir := range dirs {
			captures, ok := r.matchDir(dir)
			if !ok || r.excluded(dir) {
				continue
			}
			if err := cfg.addDiscovered(r.workspace(dir, captures)); err != nil {
				return err
			}
			found++
		}
		log.Debug().Str("rule", r.Dir).Str("root", root).Int("listed", len(dirs)).Int("workspaces", found).Msg("discovered workspaces")
	}
	return nil
}

// addDiscovered adds ws unless an explicit workspace already claims its name
// or dir, which lets an explicit entry override discovery for one directory.
// Two discovered directories claiming one workspace name is a config error:
// planning both against the same workspace would let each overwrite the other.
func (cfg *ProjectConfig) addDiscovered(ws *TFCWorkspace) error {
	for _, existing := range cfg.Workspaces {
		if existing.Name != ws.Name && existing.Dir != ws.Dir {
			continue
		}
		if !existing.Discovered || (existing.Name == ws.Name && existing.Dir == ws.Dir) {
			return nil
		}
		return utils.CreatePermanentError(fmt.Errorf(
			"discover rules derive workspace %q for dir %q and workspace %q for dir %q; add an explicit workspace entry to resolve it",
			existing.Name, existing.Dir, ws.Name, ws.Dir))
	}
	cfg.Workspaces = append(cfg.Workspaces, ws)
	return nil
}
