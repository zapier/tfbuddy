package tfc_trigger

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/utils"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.opentelemetry.io/otel"
	"gopkg.in/yaml.v2"
)

// ClassNone is what ClassFor returns for a merge request that fits no class.
// Such a merge request falls back to Manual Mode: a code owner approves it and
// a person applies it with `tfc apply`.
const ClassNone = ""

// classNameRegex limits class names to what a Sentinel policy can read back
// out of a run message without ambiguity.
var classNameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// AutoModeConfig is the `autoMode` section of .tfbuddy.yaml. TFBuddy only ever reads
// it from the merge request's target branch, so an MR can neither add its
// author to the list nor widen a class.
type AutoModeConfig struct {
	Authors []*AutoModeAuthor `yaml:"authors"`
	Classes []*AutoModeClass  `yaml:"classes"`
}

// AutoModeAuthor is a GitLab user whose merge requests run in Auto Mode.
type AutoModeAuthor struct {
	// Username is matched against the merge request author read from the
	// GitLab API, never a note or commit author, which anyone can set.
	Username string `yaml:"username"`
	// CommitEmails are the git author and committer emails this user's commits
	// carry. A commit with any other email makes the merge request fall back to
	// Manual Mode.
	CommitEmails []string `yaml:"commitEmails"`
}

// AutoModeClass is a change class: a merge request has it when its author is one
// of Authors and every file it changes matches one of Paths.
type AutoModeClass struct {
	Name    string   `yaml:"name"`
	Authors []string `yaml:"authors"`
	Paths   []string `yaml:"paths"`
}

// LoadAutoModeConfig reads the Auto Mode section of .tfbuddy.yaml from targetBranch.
// It returns nil, and so puts no merge request in Auto Mode, when the branch has
// no .tfbuddy.yaml or the file has no Auto Mode section.
func LoadAutoModeConfig(ctx context.Context, gl vcs.GitClient, project, targetBranch string) (*AutoModeConfig, error) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "LoadAutoModeConfig")
	defer span.End()

	if targetBranch == "" {
		return nil, utils.CreatePermanentError(errors.New("cannot read the Auto Mode config without a target branch"))
	}
	b, err := gl.GetRepoFile(ctx, project, ProjectConfigFilename, targetBranch)
	if err != nil {
		// GitLab refused the read, most likely because the branch has no
		// file. Treating that as "no Auto Mode" fails safe: the MR takes the
		// normal flow and nothing applies without a `tfc apply`.
		if errors.Is(err, utils.ErrPermanent) {
			log.Debug().Err(err).Str("branch", targetBranch).Msg("no Auto Mode config on target branch")
			return nil, nil
		}
		return nil, fmt.Errorf("could not read %s from %s: %w", ProjectConfigFilename, targetBranch, err)
	}
	return parseAutoModeConfig(b)
}

func parseAutoModeConfig(b []byte) (*AutoModeConfig, error) {
	var file struct {
		AutoMode *AutoModeConfig `yaml:"autoMode"`
	}
	if err := yaml.Unmarshal(b, &file); err != nil {
		return nil, fmt.Errorf("could not parse the Auto Mode section of %s: %v. %w", ProjectConfigFilename, err, utils.ErrPermanent)
	}
	if file.AutoMode == nil {
		return nil, nil
	}
	if err := file.AutoMode.validate(); err != nil {
		return nil, utils.CreatePermanentError(fmt.Errorf("invalid Auto Mode section in %s: %w", ProjectConfigFilename, err))
	}
	return file.AutoMode, nil
}

func (c *AutoModeConfig) validate() error {
	for i, a := range c.Authors {
		if a == nil || a.Username == "" {
			return fmt.Errorf("authors[%d] has no username", i)
		}
	}
	names := map[string]bool{}
	for i, class := range c.Classes {
		if class == nil {
			return fmt.Errorf("classes[%d] is empty", i)
		}
		if !classNameRegex.MatchString(class.Name) {
			return fmt.Errorf("classes[%d] name %q must match %s", i, class.Name, classNameRegex)
		}
		if names[class.Name] {
			return fmt.Errorf("classes[%d] repeats the name %q", i, class.Name)
		}
		names[class.Name] = true
		if len(class.Authors) == 0 || len(class.Paths) == 0 {
			return fmt.Errorf("class %q needs at least one author and one path", class.Name)
		}
		for _, p := range class.Paths {
			if !doublestar.ValidatePattern(p) {
				return fmt.Errorf("class %q has an invalid path pattern %q", class.Name, p)
			}
		}
	}
	return nil
}

// Author returns the Auto Mode author with username, or nil when the user is not
// on the list.
func (c *AutoModeConfig) Author(username string) *AutoModeAuthor {
	if c == nil {
		return nil
	}
	for _, a := range c.Authors {
		if strings.EqualFold(a.Username, username) {
			return a
		}
	}
	return nil
}

// ClassFor returns the change class of a merge request by author that changes
// changedFiles in commits. The first class whose authors include author and
// whose paths match every changed file wins. Anything else, including a
// commit that does not carry author's commit emails, is ClassNone.
func (c *AutoModeConfig) ClassFor(author *AutoModeAuthor, changedFiles []string, commits []vcs.MRCommit) string {
	if len(changedFiles) == 0 || !author.madeAll(commits) {
		return ClassNone
	}
	for _, class := range c.Classes {
		if slices.ContainsFunc(class.Authors, func(a string) bool { return strings.EqualFold(a, author.Username) }) &&
			class.matchesAll(changedFiles) {
			return class.Name
		}
	}
	return ClassNone
}

// madeAll reports whether every commit carries one of a's commit emails as both
// its author and its committer. Git takes these from whoever makes the commit,
// so this is evidence rather than proof: anyone who can push to the MR's branch
// can write a commit that claims them.
func (a *AutoModeAuthor) madeAll(commits []vcs.MRCommit) bool {
	if len(commits) == 0 {
		return false
	}
	owns := func(email string) bool {
		return slices.ContainsFunc(a.CommitEmails, func(e string) bool { return strings.EqualFold(e, email) })
	}
	for _, commit := range commits {
		if !owns(commit.AuthorEmail) || !owns(commit.CommitterEmail) {
			return false
		}
	}
	return true
}

func (class *AutoModeClass) matchesAll(files []string) bool {
	for _, f := range files {
		if !slices.ContainsFunc(class.Paths, func(p string) bool {
			match, err := doublestar.Match(p, f)
			return err == nil && match
		}) {
			return false
		}
	}
	return true
}

// ClassMarker is how a run message names its change class. Sentinel's class
// policy reads it from the run message.
func ClassMarker(class string) string {
	return fmt.Sprintf("[tfbuddy-class=%s]", class)
}

// runMessage is the message of every run TFBuddy creates for the MR. An Auto Mode
// run's message is built from fixed fields only and leaves the MR title out,
// so nothing a person or a bot writes can add a second marker or push the
// marker out of the message. It keeps the `MR [!<iid>]` prefix that other
// tools find TFBuddy's runs by.
func (t *TFCTrigger) runMessage(title string) (string, error) {
	if !t.cfg.AutoMode {
		return fmt.Sprintf("MR [!%d]: %s", t.GetMergeRequestIID(), title), nil
	}
	// One TFC token serves both modes, so Sentinel tells an Auto Mode run apart
	// only by its marker. Without one, the run would get only the global
	// policies.
	if !classNameRegex.MatchString(t.cfg.ChangeClass) {
		return "", fmt.Errorf("refusing to create an Auto Mode run without a valid change class (got %q). %w", t.cfg.ChangeClass, utils.ErrPermanent)
	}
	return fmt.Sprintf("MR [!%d] %s sha=%s author=%s", t.GetMergeRequestIID(), ClassMarker(t.cfg.ChangeClass), t.GetCommitSHA(), t.cfg.AutoModeAuthor), nil
}

// resolveAutoMode decides whether mr runs in Auto Mode and, when it does, records its
// change class on the trigger, so that every run created for it, inline or
// fanned out, carries the class. affected are the workspaces whose plans
// decide TFC/class. Lock and unlock create no run, so they skip it: `tfc
// unlock` must keep working when the Auto Mode config cannot be read.
func (t *TFCTrigger) resolveAutoMode(ctx context.Context, mr vcs.DetailedMR, affected []*TFCWorkspace) error {
	if !t.appCfg.AutoModeEnabled || t.GetVcsProvider() != "gitlab" ||
		(t.GetAction() != PlanAction && t.GetAction() != ApplyAction) {
		return nil
	}
	ctx, span := otel.Tracer(t.tracerName()).Start(ctx, "resolveAutoMode")
	defer span.End()

	autoMode, err := LoadAutoModeConfig(ctx, t.gl, t.GetProjectNameWithNamespace(), mr.GetTargetBranch())
	if err != nil {
		return err
	}
	author := autoMode.Author(mr.GetAuthor().GetUsername())
	if author == nil {
		return nil
	}
	files, err := t.gl.GetMergeRequestModifiedFiles(ctx, t.GetMergeRequestIID(), t.GetProjectNameWithNamespace())
	if err != nil {
		return fmt.Errorf("could not list the MR's changed files. %w", err)
	}
	commits, err := t.gl.GetMergeRequestCommits(ctx, t.GetMergeRequestIID(), t.GetProjectNameWithNamespace())
	if err != nil {
		return fmt.Errorf("could not list the MR's commits. %w", err)
	}

	class := autoMode.ClassFor(author, files, commits)
	if class == ClassNone {
		log.Info().Str("project", t.GetProjectNameWithNamespace()).Int("mergeRequestID", t.GetMergeRequestIID()).
			Str("author", author.Username).Msg("merge request fits no Auto Mode class, falls back to Manual Mode")
		return nil
	}
	t.cfg.AutoMode = true
	t.cfg.AutoModeAuthor = author.Username
	t.cfg.ChangeClass = class
	t.cfg.ClassWorkspaces = make([]string, 0, len(affected))
	for _, ws := range affected {
		t.cfg.ClassWorkspaces = append(t.cfg.ClassWorkspaces, ws.Name)
	}
	slices.Sort(t.cfg.ClassWorkspaces)
	log.Info().Str("project", t.GetProjectNameWithNamespace()).Int("mergeRequestID", t.GetMergeRequestIID()).
		Str("author", author.Username).Str("class", t.cfg.ChangeClass).Msg("merge request runs in Auto Mode")
	return nil
}

// postClassStatus publishes the MR's TFC/class status. Like a workspace status,
// it is a notification: a failure is logged and changes nothing else.
func (t *TFCTrigger) postClassStatus(ctx context.Context, state vcs.CommitState, description string) {
	ctx, cancel := context.WithTimeout(ctx, statusWriteBudget)
	defer cancel()
	if err := t.gl.SetClassStatus(ctx, vcs.ClassStatus{
		Project:         t.GetProjectNameWithNamespace(),
		CommitSHA:       t.GetCommitSHA(),
		MergeRequestIID: t.GetMergeRequestIID(),
		State:           state,
		Description:     description,
	}); err != nil {
		log.Error().Err(err).Str("class", t.cfg.ChangeClass).Msg("could not set class commit status")
	}
}
