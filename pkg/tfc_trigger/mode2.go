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

// ClassReviewed is the change class of a Mode 2 merge request that fits no
// configured class. Only Sentinel's global policies hold its runs.
const ClassReviewed = "reviewed"

// classNameRegex limits class names to what a Sentinel policy can read back
// out of a run message without ambiguity.
var classNameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Mode2Config is the `mode2` section of .tfbuddy.yaml. TFBuddy only ever reads
// it from the merge request's target branch, so an MR can neither add its
// author to the list nor widen a class.
type Mode2Config struct {
	Authors []*Mode2Author `yaml:"authors"`
	Classes []*Mode2Class  `yaml:"classes"`
}

// Mode2Author is a GitLab user whose merge requests run in Mode 2.
type Mode2Author struct {
	// Username is matched against the merge request author read from the
	// GitLab API, never a note or commit author, which anyone can set.
	Username string `yaml:"username"`
	// CommitEmails are the git author and committer emails this user's commits
	// carry. A commit with any other email makes the merge request Reviewed.
	CommitEmails []string `yaml:"commitEmails"`
}

// Mode2Class is a change class: a merge request has it when its author is one
// of Authors and every file it changes matches one of Paths.
type Mode2Class struct {
	Name    string   `yaml:"name"`
	Authors []string `yaml:"authors"`
	Paths   []string `yaml:"paths"`
}

// LoadMode2Config reads the mode2 section of .tfbuddy.yaml from targetBranch.
// It returns nil, and so puts no merge request in Mode 2, when the branch has
// no .tfbuddy.yaml or the file has no mode2 section.
func LoadMode2Config(ctx context.Context, gl vcs.GitClient, project, targetBranch string) (*Mode2Config, error) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "LoadMode2Config")
	defer span.End()

	if targetBranch == "" {
		return nil, utils.CreatePermanentError(errors.New("cannot read the Mode 2 config without a target branch"))
	}
	b, err := gl.GetRepoFile(ctx, project, ProjectConfigFilename, targetBranch)
	if err != nil {
		// GitLab refused the read, most likely because the branch has no
		// file. Treating that as "no Mode 2" fails safe: the MR takes the
		// normal flow and nothing applies without a `tfc apply`.
		if errors.Is(err, utils.ErrPermanent) {
			log.Debug().Err(err).Str("branch", targetBranch).Msg("no Mode 2 config on target branch")
			return nil, nil
		}
		return nil, fmt.Errorf("could not read %s from %s: %w", ProjectConfigFilename, targetBranch, err)
	}
	return parseMode2Config(b)
}

func parseMode2Config(b []byte) (*Mode2Config, error) {
	var file struct {
		Mode2 *Mode2Config `yaml:"mode2"`
	}
	if err := yaml.Unmarshal(b, &file); err != nil {
		return nil, fmt.Errorf("could not parse the mode2 section of %s: %v. %w", ProjectConfigFilename, err, utils.ErrPermanent)
	}
	if file.Mode2 == nil {
		return nil, nil
	}
	if err := file.Mode2.validate(); err != nil {
		return nil, utils.CreatePermanentError(fmt.Errorf("invalid mode2 section in %s: %w", ProjectConfigFilename, err))
	}
	return file.Mode2, nil
}

func (c *Mode2Config) validate() error {
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
		if class.Name == ClassReviewed {
			return fmt.Errorf("classes[%d] cannot be named %q, TFBuddy gives that class to every other change", i, ClassReviewed)
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

// Author returns the Mode 2 author with username, or nil when the user is not
// on the list.
func (c *Mode2Config) Author(username string) *Mode2Author {
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
// commit that does not carry author's commit emails, is ClassReviewed.
func (c *Mode2Config) ClassFor(author *Mode2Author, changedFiles []string, commits []vcs.MRCommit) string {
	if len(changedFiles) == 0 || !author.madeAll(commits) {
		return ClassReviewed
	}
	for _, class := range c.Classes {
		if slices.ContainsFunc(class.Authors, func(a string) bool { return strings.EqualFold(a, author.Username) }) &&
			class.matchesAll(changedFiles) {
			return class.Name
		}
	}
	return ClassReviewed
}

// madeAll reports whether every commit carries one of a's commit emails as both
// its author and its committer. Git takes these from whoever makes the commit,
// so this is evidence rather than proof: anyone who can push to the MR's branch
// can write a commit that claims them.
func (a *Mode2Author) madeAll(commits []vcs.MRCommit) bool {
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

func (class *Mode2Class) matchesAll(files []string) bool {
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

// runMessage is the message of every run TFBuddy creates for the MR. A Mode 2
// run's message is built from fixed fields only and leaves the MR title out,
// so nothing a person or a bot writes can add a second marker or push the
// marker out of the message. It keeps the `MR [!<iid>]` prefix that other
// tools find TFBuddy's runs by.
func (t *TFCTrigger) runMessage(title string) (string, error) {
	if !t.cfg.Mode2 {
		return fmt.Sprintf("MR [!%d]: %s", t.GetMergeRequestIID(), title), nil
	}
	// One TFC token serves both modes, so Sentinel tells a Mode 2 run apart
	// only by its marker. Without one, the run would get only the global
	// policies.
	if !classNameRegex.MatchString(t.cfg.ChangeClass) {
		return "", fmt.Errorf("refusing to create a Mode 2 run without a valid change class (got %q). %w", t.cfg.ChangeClass, utils.ErrPermanent)
	}
	return fmt.Sprintf("MR [!%d] %s sha=%s actor=%s", t.GetMergeRequestIID(), ClassMarker(t.cfg.ChangeClass), t.GetCommitSHA(), t.cfg.Mode2Actor), nil
}

// resolveMode2 decides whether mr runs in Mode 2 and, when it does, records its
// change class on the trigger, so that every run created for it, inline or
// fanned out, carries the class. affected are the workspaces whose plans
// decide TFC/class. Lock and unlock create no run, so they skip it: `tfc
// unlock` must keep working when the Mode 2 config cannot be read.
func (t *TFCTrigger) resolveMode2(ctx context.Context, mr vcs.DetailedMR, affected []*TFCWorkspace) error {
	if !t.appCfg.Mode2Enabled || t.GetVcsProvider() != "gitlab" ||
		(t.GetAction() != PlanAction && t.GetAction() != ApplyAction) {
		return nil
	}
	ctx, span := otel.Tracer(t.tracerName()).Start(ctx, "resolveMode2")
	defer span.End()

	mode2, err := LoadMode2Config(ctx, t.gl, t.GetProjectNameWithNamespace(), mr.GetTargetBranch())
	if err != nil {
		return err
	}
	author := mode2.Author(mr.GetAuthor().GetUsername())
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

	t.cfg.Mode2 = true
	t.cfg.Mode2Actor = author.Username
	t.cfg.ChangeClass = mode2.ClassFor(author, files, commits)
	t.cfg.ClassWorkspaces = make([]string, 0, len(affected))
	for _, ws := range affected {
		t.cfg.ClassWorkspaces = append(t.cfg.ClassWorkspaces, ws.Name)
	}
	slices.Sort(t.cfg.ClassWorkspaces)
	log.Info().Str("project", t.GetProjectNameWithNamespace()).Int("mergeRequestID", t.GetMergeRequestIID()).
		Str("author", author.Username).Str("class", t.cfg.ChangeClass).Msg("merge request runs in Mode 2")
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
