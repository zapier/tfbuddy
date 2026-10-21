package tfc_trigger

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
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
		if placeholderInClass(td) {
			return fmt.Errorf("discover rule %q: triggerDir %q has a placeholder inside [...], which makes it a set of characters rather than a directory name", r.Dir, td)
		}
	}
	for _, ex := range r.Exclude {
		if !doublestar.ValidatePattern(strings.Trim(ex, "/")) {
			return fmt.Errorf("discover rule %q: invalid exclude pattern %q", r.Dir, ex)
		}
	}
	return nil
}

// placeholderInClass reports whether a placeholder sits inside a [...]
// character class of glob. Matching a triggerDir before its placeholders are
// filled stands `*` in for each one, which inside a class matches only a
// literal `*`, so such a triggerDir would never find its workspaces.
func placeholderInClass(glob string) bool {
	inClass := false
	for i := 0; i < len(glob); i++ {
		switch c := glob[i]; {
		case c == '\\':
			i++ // the next character is literal
		case c == '[' && !inClass:
			inClass = true
		case c == ']' && inClass:
			inClass = false
		case c == '{' && inClass:
			if loc := placeholderRE.FindStringIndex(glob[i:]); loc != nil && loc[0] == 0 {
				return true
			}
		}
	}
	return false
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

// matchDirSuffix reports whether some directory the rule matches could
// trigger through dir, and returns the wildcard values that pins down.
// workspaceForDir triggers a workspace whose dir ends dir as a string, so
// dir "services/api/" is triggered by a change in "archive/services/api/"
// and even in "oldservices/api/". The first segment of such a suffix may be
// only part of a directory name, so a wildcard there stays unset (""), even
// when dir is exactly as deep as the rule: "oldfoo/api/" triggers "foo/api/".
func (r *DiscoverRule) matchDirSuffix(dir string) ([]string, bool) {
	pattern := r.dirSegments()
	segs := strings.Split(strings.Trim(dir, "/"), "/")
	if len(segs) < len(pattern) {
		return nil, false
	}
	tail := segs[len(segs)-len(pattern):]
	captures := make([]string, r.wildcards())
	w := 0
	for i, seg := range pattern {
		switch {
		case seg == "*":
			if i > 0 {
				captures[w] = tail[i]
			}
			w++
		case i == 0:
			if !strings.HasSuffix(tail[0], seg) {
				return nil, false
			}
		case seg != tail[i]:
			return nil, false
		}
	}
	return captures, true
}

// matchTriggerDir reports whether p matches the triggerDir template td, and
// returns whichever of the rule's wildcards td pins down. A placeholder only
// pins its wildcard when it fills a whole path segment ahead of any `**` or
// `{a,b}` alternative, either of which can match a different number of path
// segments; elsewhere its position in p is ambiguous and it is left unset ("").
func (r *DiscoverRule) matchTriggerDir(td, p string) ([]string, bool) {
	if match, err := doublestar.Match(placeholderRE.ReplaceAllString(td, "*"), p); err != nil || !match {
		return nil, false
	}
	captures := make([]string, r.wildcards())
	pSegs := strings.Split(p, "/")
	for i, seg := range strings.Split(td, "/") {
		if i >= len(pSegs) || strings.Contains(seg, "**") || strings.Contains(placeholderRE.ReplaceAllString(seg, ""), "{") {
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
		if captures, ok := r.matchDirSuffix(dir); ok {
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

// maxNameSplits caps how many ways of splitting one workspace name are
// checked one by one, and maxNameSplitSteps how long finding them may take;
// past either, every directory the rules match is listed instead.
const (
	maxNameSplits     = 64
	maxNameSplitSteps = 20000
)

// nameSplits returns every set of wildcard values that fills the rule's Name
// as name, and false when there are more than maxNameSplits or the search
// takes more than maxNameSplitSteps steps. Adjacent placeholders can make
// even a search that finds nothing take exponentially many steps. A value is
// one directory name, so it is never empty and never contains a slash.
func (r *DiscoverRule) nameSplits(name string) ([][]string, bool) {
	// The name template as alternating literals (n == 0) and placeholders.
	type part struct {
		literal string
		n       int
	}
	var parts []part
	last := 0
	for _, loc := range placeholderRE.FindAllStringSubmatchIndex(r.Name, -1) {
		if loc[0] > last {
			parts = append(parts, part{literal: r.Name[last:loc[0]]})
		}
		n, _ := strconv.Atoi(r.Name[loc[2]:loc[3]])
		parts = append(parts, part{n: n})
		last = loc[1]
	}
	if last < len(r.Name) {
		parts = append(parts, part{literal: r.Name[last:]})
	}

	var splits [][]string
	captures := make([]string, r.wildcards())
	steps := 0
	var walk func(i, pos int) bool
	walk = func(i, pos int) bool {
		steps++
		if steps > maxNameSplitSteps {
			return false
		}
		if i == len(parts) {
			if pos == len(name) {
				if len(splits) == maxNameSplits {
					return false
				}
				splits = append(splits, append([]string(nil), captures...))
			}
			return true
		}
		p := parts[i]
		if p.n == 0 {
			if !strings.HasPrefix(name[pos:], p.literal) {
				return true
			}
			return walk(i+1, pos+len(p.literal))
		}
		// A placeholder used twice must repeat its value.
		if v := captures[p.n-1]; v != "" {
			if !strings.HasPrefix(name[pos:], v) {
				return true
			}
			return walk(i+1, pos+len(v))
		}
		for end := pos + 1; end <= len(name); end++ {
			v := name[pos:end]
			if strings.Contains(v, "/") {
				break
			}
			if v == "." || v == ".." {
				continue
			}
			captures[p.n-1] = v
			ok := walk(i+1, end)
			captures[p.n-1] = ""
			if !ok {
				return false
			}
		}
		return true
	}
	complete := walk(0, 0)
	return splits, complete
}

// dirFor returns the directory whose wildcards match captures.
func (r *DiscoverRule) dirFor(captures []string) string {
	var segs []string
	w := 0
	for _, seg := range r.dirSegments() {
		if seg == "*" {
			seg = captures[w]
			w++
		}
		segs = append(segs, seg)
	}
	return strings.Join(segs, "/")
}

// dirLister returns every directory below dir, recursively, relative to the
// repo root.
type dirLister func(ctx context.Context, dir string) ([]string, error)

// dirIndex lists each root at most once and answers whether a directory
// exists from the listings made so far.
type dirIndex struct {
	list   dirLister
	roots  []string
	exists map[string]bool
}

func newDirIndex(list dirLister) *dirIndex {
	return &dirIndex{list: list, exists: map[string]bool{}}
}

// listedBelow reports whether a listing so far reached dir. A listing returns
// its root's descendants, never the root itself, so only a strict ancestor's
// listing says whether dir exists.
func (x *dirIndex) listedBelow(dir string) bool {
	for _, root := range x.roots {
		if root == "" || strings.HasPrefix(dir, root+"/") {
			return true
		}
	}
	return false
}

// listDirs returns every directory below root, from an earlier listing that
// covered root when there is one.
func (x *dirIndex) listDirs(ctx context.Context, root string) ([]string, error) {
	var dirs []string
	if x.listedBelow(root) || slices.Contains(x.roots, root) {
		for d := range x.exists {
			if root == "" || strings.HasPrefix(d, root+"/") {
				dirs = append(dirs, d)
			}
		}
		sort.Strings(dirs)
		return dirs, nil
	}
	dirs, err := x.list(ctx, root)
	if err != nil {
		return nil, err
	}
	x.roots = append(x.roots, root)
	for _, d := range dirs {
		x.exists[d] = true
	}
	return dirs, nil
}

// has reports whether dir exists, listing root, a strict ancestor of dir,
// first when no earlier listing reached dir.
func (x *dirIndex) has(ctx context.Context, dir, root string) (bool, error) {
	if !x.listedBelow(dir) {
		if _, err := x.listDirs(ctx, root); err != nil {
			return false, err
		}
	}
	return x.exists[dir], nil
}

// discoveredWorkspace is a workspace a discover rule added.
type discoveredWorkspace struct {
	ws   *TFCWorkspace
	rule *DiscoverRule
}

// discoverWorkspaces adds the workspaces cfg's discover rules derive to
// cfg.Workspaces, limited to those modifiedFiles could trigger so that an MR
// lists only the directories it touches.
func (cfg *ProjectConfig) discoverWorkspaces(ctx context.Context, list dirLister, modifiedFiles []string) error {
	idx := newDirIndex(list)
	var discovered []discoveredWorkspace
	for _, r := range cfg.Discover {
		found, err := cfg.discoverFromRoots(ctx, idx, r, r.listRoots(modifiedFiles))
		if err != nil {
			return err
		}
		discovered = append(discovered, found...)
	}
	return cfg.checkNamesUnique(ctx, idx, discovered)
}

// discoverAllWorkspaces adds every workspace cfg's discover rules derive, for
// a workspace named explicitly that the MR's changes do not reach. Having
// listed everything, addDiscovered sees every duplicate name itself.
func (cfg *ProjectConfig) discoverAllWorkspaces(ctx context.Context, list dirLister) error {
	return cfg.discoverAll(ctx, newDirIndex(list))
}

func (cfg *ProjectConfig) discoverAll(ctx context.Context, idx *dirIndex) error {
	for _, r := range cfg.Discover {
		if _, err := cfg.discoverFromRoots(ctx, idx, r, []string{r.listRoot(nil)}); err != nil {
			return err
		}
	}
	return nil
}

func (cfg *ProjectConfig) discoverFromRoots(ctx context.Context, idx *dirIndex, r *DiscoverRule, roots []string) ([]discoveredWorkspace, error) {
	var discovered []discoveredWorkspace
	for _, root := range roots {
		dirs, err := idx.listDirs(ctx, root)
		if err != nil {
			return nil, fmt.Errorf("could not list %q for discover rule %q: %w", root, r.Dir, err)
		}
		found := 0
		for _, dir := range dirs {
			captures, ok := r.matchDir(dir)
			if !ok || r.excluded(dir) {
				continue
			}
			ws := r.workspace(dir, captures)
			added, err := cfg.addDiscovered(ws)
			if err != nil {
				return nil, err
			}
			if added {
				discovered = append(discovered, discoveredWorkspace{ws: ws, rule: r})
			}
			found++
		}
		log.Debug().Str("rule", r.Dir).Str("root", root).Int("listed", len(dirs)).Int("workspaces", found).Msg("discovered workspaces")
	}
	return discovered, nil
}

// checkNamesUnique fails when another rule would give a discovered
// directory a different workspace (organization or name), or when a directory
// the listings so far did not reach would derive the same workspace as one
// discovered. Two MRs would then
// plan different directories against one workspace, and neither MR's listing
// shows the other directory. For each discovered name it works out every
// directory any rule could derive that name from, and checks whether those
// directories exist. A name with too many such directories falls back to
// listing everything every rule matches.
func (cfg *ProjectConfig) checkNamesUnique(ctx context.Context, idx *dirIndex, discovered []discoveredWorkspace) error {
	for _, d := range discovered {
		// Every rule that matches the directory must give it the same
		// workspace, or the workspace an MR plans would depend on which rule's
		// triggerDirs it happened to touch.
		dir := strings.TrimSuffix(d.ws.Dir, "/")
		for _, r := range cfg.Discover {
			if r == d.rule || r.excluded(dir) {
				continue
			}
			captures, ok := r.matchDir(dir)
			if !ok {
				continue
			}
			switch other := r.workspace(dir, captures); {
			case cfg.hasExplicit(other):
				// An explicit entry claims that workspace, so the rule does
				// not derive it here; addDiscovered skips it the same way.
			case sameWorkspace(other, d.ws):
				// Whichever rules the MR reached, the workspace gets every
				// rule's triggerDirs.
				if err := mergeDefinition(d.ws, other); err != nil {
					return err
				}
			default:
				return utils.CreatePermanentError(fmt.Errorf(
					"discover rules derive both workspace %q and workspace %q for dir %q; exclude it from one rule or add an explicit workspace entry",
					workspaceID(d.ws), workspaceID(other), dir))
			}
		}
		for _, r := range cfg.Discover {
			// A rule in another organization derives a different workspace,
			// whatever its name.
			if r.Organization != d.ws.Organization {
				continue
			}
			splits, complete := r.nameSplits(d.ws.Name)
			if !complete {
				return cfg.discoverAll(ctx, idx)
			}
			for _, captures := range splits {
				dir := r.dirFor(captures)
				if sameDir(dir, d.ws.Dir) || r.excluded(dir) || cfg.hasExplicitDir(dir) {
					continue
				}
				exists, err := idx.has(ctx, dir, r.listRoot(captures[:1]))
				if err != nil {
					return fmt.Errorf("could not check whether %q also derives workspace %q: %w", dir, d.ws.Name, err)
				}
				if exists {
					return utils.CreatePermanentError(fmt.Errorf(
						"discover rules derive workspace %q from both %q and %q; add an explicit workspace entry for one of them",
						workspaceID(d.ws), strings.TrimSuffix(d.ws.Dir, "/"), dir))
				}
			}
		}
	}
	return nil
}

// hasExplicit reports whether an explicit entry claims ws's workspace or dir.
func (cfg *ProjectConfig) hasExplicit(ws *TFCWorkspace) bool {
	for _, existing := range cfg.Workspaces {
		if !existing.Discovered && (sameWorkspace(existing, ws) || sameDir(existing.Dir, ws.Dir)) {
			return true
		}
	}
	return false
}

func (cfg *ProjectConfig) hasExplicitDir(dir string) bool {
	for _, ws := range cfg.Workspaces {
		if !ws.Discovered && sameDir(ws.Dir, dir) {
			return true
		}
	}
	return false
}

// addDiscovered adds ws unless an explicit workspace already claims the same
// workspace or dir, which lets an explicit entry override discovery for one
// directory, and reports whether it did. A workspace is its organization and
// name. Another rule deriving the same workspace for the same dir merges into
// it (see mergeDefinition). Two discovered directories claiming one workspace, or one directory
// claimed as two workspaces, is a config error: planning both would let each
// overwrite the other, or put one directory's code in two states.
func (cfg *ProjectConfig) addDiscovered(ws *TFCWorkspace) (bool, error) {
	for _, existing := range cfg.Workspaces {
		same := sameWorkspace(existing, ws)
		if !same && !sameDir(existing.Dir, ws.Dir) {
			continue
		}
		if !existing.Discovered {
			return false, nil
		}
		if same && sameDir(existing.Dir, ws.Dir) {
			return false, mergeDefinition(existing, ws)
		}
		return false, utils.CreatePermanentError(fmt.Errorf(
			"discover rules derive workspace %q for dir %q and workspace %q for dir %q; add an explicit workspace entry to resolve it",
			workspaceID(existing), existing.Dir, workspaceID(ws), ws.Dir))
	}
	cfg.Workspaces = append(cfg.Workspaces, ws)
	return true, nil
}

// mergeDefinition adds ws's triggerDirs to existing, the same workspace for
// the same dir as derived by another rule, so each rule's dependencies
// trigger it. Rules that disagree on how the workspace runs are a config
// error.
func mergeDefinition(existing, ws *TFCWorkspace) error {
	if existing.Mode != ws.Mode || existing.AutoMerge != ws.AutoMerge {
		return utils.CreatePermanentError(fmt.Errorf(
			"discover rules derive workspace %q for dir %q with different mode or autoMerge; make the rules agree or add an explicit workspace entry",
			workspaceID(ws), strings.TrimSuffix(ws.Dir, "/")))
	}
	for _, td := range ws.TriggerDirs {
		if !slices.Contains(existing.TriggerDirs, td) {
			existing.TriggerDirs = append(existing.TriggerDirs, td)
		}
	}
	return nil
}

// sameWorkspace reports whether a and b are the same TFC workspace.
func sameWorkspace(a, b *TFCWorkspace) bool {
	return a.Organization == b.Organization && a.Name == b.Name
}

// workspaceID names a workspace as organization/name.
func workspaceID(ws *TFCWorkspace) string {
	return ws.Organization + "/" + ws.Name
}

// sameDir compares workspace dirs, whose trailing slash is optional.
func sameDir(a, b string) bool {
	return strings.TrimSuffix(a, "/") == strings.TrimSuffix(b, "/")
}

// clone returns a copy of cfg whose workspace list discovery can extend
// independently.
func (cfg *ProjectConfig) clone() *ProjectConfig {
	return &ProjectConfig{
		Workspaces: append([]*TFCWorkspace(nil), cfg.Workspaces...),
		Discover:   cfg.Discover,
	}
}
