package tfc_trigger

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/runstream"
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

// AutoModeConfig is the `autoMode` section of .tfbuddy.yaml. TFBuddy only ever
// reads it from the project's default branch, and only for a merge request
// that targets that branch, so an MR can neither add its author to the list
// nor widen a class, and neither can a branch nobody protects.
type AutoModeConfig struct {
	Authors []*AutoModeAuthor `yaml:"authors"`
	Classes []*AutoModeClass  `yaml:"classes"`
	// AutomaticApprovers are the users, such as an approval bot, whose
	// approval is automatic. For an MR that fell back to Manual Mode, neither
	// their approval nor an Auto Mode author's counts: `tfc apply` needs one
	// from anyone else.
	AutomaticApprovers []string `yaml:"automaticApprovers"`
	// RequiredJobs are CI jobs that must be in the head commit's pipeline,
	// and whose latest run must have succeeded, before an approval applies
	// an Auto Mode MR. A manual, skipped or allowed-to-fail job does not
	// count as succeeded. List the review jobs an automatic approver relies on.
	RequiredJobs []string `yaml:"requiredJobs"`
	// ProtectedDirs are more directory names that no Auto Mode merge request
	// may change, at any depth, on top of the built-in `.gitlab` directory and
	// `.gitlab-ci.yml` files. List the directories that configure the review
	// jobs Auto Mode relies on, such as an AI review's prompt directory.
	ProtectedDirs []string `yaml:"protectedDirs"`
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
// of Authors, every file it changes matches one of Paths, and every workspace
// it affects matches one of Workspaces.
type AutoModeClass struct {
	Name    string   `yaml:"name"`
	Authors []string `yaml:"authors"`
	Paths   []string `yaml:"paths"`
	// Workspaces are doublestar globs over TFC workspace names, such as
	// `services-*`. A class without any matches no merge request.
	Workspaces []string `yaml:"workspaces"`
}

// LoadAutoModeConfig reads the Auto Mode section of .tfbuddy.yaml from the
// project's default branch, for a merge request that targets targetBranch. It
// returns nil, and so puts no merge request in Auto Mode, when targetBranch is
// not the default branch, when the default branch has no .tfbuddy.yaml, or
// when the file has no Auto Mode section.
func LoadAutoModeConfig(ctx context.Context, gl vcs.GitClient, project, targetBranch string) (*AutoModeConfig, error) {
	cfg, _, err := loadDefaultBranchAutoModeConfig(ctx, gl, project, targetBranch, false)
	return cfg, err
}

// loadDefaultBranchAutoModeConfig reads the Auto Mode section of the
// project's default branch, and reports whether targetBranch is that branch.
// For any other target it reads nothing unless anyTarget is set: an MR into
// another branch never runs in Auto Mode, but it may have fallen back to
// Manual Mode before it was retargeted, and the fallback needs the config's
// automatic approvers.
func loadDefaultBranchAutoModeConfig(ctx context.Context, gl vcs.GitClient, project, targetBranch string, anyTarget bool) (*AutoModeConfig, bool, error) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "LoadAutoModeConfig")
	defer span.End()

	if targetBranch == "" {
		return nil, false, utils.CreatePermanentError(errors.New("cannot read the Auto Mode config without a target branch"))
	}
	settings, err := gl.GetProjectSettings(ctx, project)
	if err != nil {
		return nil, false, fmt.Errorf("could not read the project's default branch: %w", err)
	}
	if settings == nil {
		return nil, false, utils.CreatePermanentError(errors.New("could not read the project's default branch: no project settings"))
	}
	defaultBranch := settings.GetDefaultBranch()
	targetsDefault := defaultBranch != "" && targetBranch == defaultBranch
	if !targetsDefault {
		// Only the default branch is protected the way Auto Mode needs: an MR
		// into any other branch is a Manual Mode MR, whatever that branch's
		// .tfbuddy.yaml says.
		log.Debug().Str("project", project).Str("targetBranch", targetBranch).Str("defaultBranch", defaultBranch).
			Msg("merge request does not target the default branch, so it has no Auto Mode")
		if !anyTarget || defaultBranch == "" {
			return nil, false, nil
		}
	}
	b, err := gl.GetRepoFile(ctx, project, ProjectConfigFilename, defaultBranch)
	if err != nil {
		// GitLab refused the read, most likely because the branch has no
		// file. Treating that as "no Auto Mode" fails safe: the MR takes the
		// normal flow and nothing applies without a `tfc apply`.
		if errors.Is(err, utils.ErrPermanent) {
			log.Debug().Err(err).Str("branch", defaultBranch).Msg("no Auto Mode config on the default branch")
			return nil, targetsDefault, nil
		}
		return nil, false, fmt.Errorf("could not read %s from %s: %w", ProjectConfigFilename, defaultBranch, err)
	}
	cfg, err := parseAutoModeConfig(b)
	return cfg, targetsDefault, err
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
	for i, u := range c.AutomaticApprovers {
		if strings.TrimSpace(u) == "" {
			return fmt.Errorf("automaticApprovers[%d] is empty", i)
		}
	}
	for i, j := range c.RequiredJobs {
		if strings.TrimSpace(j) == "" {
			return fmt.Errorf("requiredJobs[%d] is empty", i)
		}
	}
	for i, d := range c.ProtectedDirs {
		if name := strings.Trim(d, "/"); name == "" || name == "." || name == ".." || strings.Contains(name, "/") {
			return fmt.Errorf("protectedDirs[%d] %q must be a single directory name", i, d)
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
		for _, p := range class.Workspaces {
			if !doublestar.ValidatePattern(p) {
				return fmt.Errorf("class %q has an invalid workspace pattern %q", class.Name, p)
			}
		}
	}
	return nil
}

// PersonApprover returns the first of approvers whose approval counts as a
// person's for an MR that fell back to Manual Mode: one who is neither an
// automatic approver nor an Auto Mode author. It returns "" when there is none.
func (c *AutoModeConfig) PersonApprover(approvers []string) string {
	for _, u := range approvers {
		if c.Author(u) == nil && !slices.ContainsFunc(c.AutomaticApprovers, func(a string) bool { return strings.EqualFold(a, u) }) {
			return u
		}
	}
	return ""
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

// autoModeDeniedDirs are directories, at any depth, that no Auto Mode merge
// request may change, whatever .tfbuddy.yaml says: they decide what CI runs,
// including the review jobs Auto Mode relies on. `protectedDirs` in
// .tfbuddy.yaml adds more.
var autoModeDeniedDirs = []string{".gitlab"}

// autoModeDeniedFile is the file name, at any depth, that no Auto Mode merge
// request may change, for the same reason.
const autoModeDeniedFile = ".gitlab-ci.yml"

// AutoModeDeniedPath reports whether file is one that always puts a merge
// request in Manual Mode: under a built-in denied directory or one of
// protectedDirs, or named `.gitlab-ci.yml`. It matches names
// case-insensitively, so a checkout on a case-insensitive file system cannot
// turn it into a denied path.
func AutoModeDeniedPath(file string, protectedDirs []string) bool {
	denied := func(part string) bool {
		match := func(d string) bool { return strings.EqualFold(strings.Trim(d, "/"), part) }
		return slices.ContainsFunc(autoModeDeniedDirs, match) || slices.ContainsFunc(protectedDirs, match)
	}
	parts := strings.Split(path.Clean(strings.TrimPrefix(file, "/")), "/")
	for i, part := range parts {
		if denied(part) {
			return true
		}
		if i == len(parts)-1 && strings.EqualFold(part, autoModeDeniedFile) {
			return true
		}
	}
	return false
}

// ClassFor returns the change class of a merge request by author that changes
// changedFiles in commits and affects workspaces. The first class whose
// authors include author, whose paths match every changed file and whose
// workspaces match every affected workspace wins. Anything else, including a
// commit that does not carry author's commit emails, a change to a denied
// path (see AutoModeDeniedPath) and a merge request that affects no
// workspace, is ClassNone, and then reason says why.
func (c *AutoModeConfig) ClassFor(author *AutoModeAuthor, changedFiles []string, commits []vcs.MRCommit, workspaces []string) (class, reason string) {
	if len(changedFiles) == 0 {
		return ClassNone, "it changes no files"
	}
	if len(workspaces) == 0 {
		return ClassNone, "it affects no workspace"
	}
	if why := author.notMadeBy(commits); why != "" {
		return ClassNone, why
	}
	if i := slices.IndexFunc(changedFiles, func(f string) bool { return AutoModeDeniedPath(f, c.ProtectedDirs) }); i >= 0 {
		return ClassNone, fmt.Sprintf("it changes `%s`, and Auto Mode never applies a change to CI or review config", changedFiles[i])
	}
	for _, class := range c.Classes {
		if slices.ContainsFunc(class.Authors, func(a string) bool { return strings.EqualFold(a, author.Username) }) &&
			matchAll(class.Paths, changedFiles) && matchAll(class.Workspaces, workspaces) {
			return class.Name, ""
		}
	}
	return ClassNone, fmt.Sprintf("its changed files and affected workspaces fit none of %s's Auto Mode classes", author.Username)
}

// notMadeBy returns why commits are not all a's, or "" when every commit
// carries one of a's commit emails as both its author and its committer. Git
// takes these from whoever makes the commit, so this is evidence rather than
// proof: anyone who can push to the MR's branch can write a commit that claims
// them.
func (a *AutoModeAuthor) notMadeBy(commits []vcs.MRCommit) string {
	if len(commits) == 0 {
		return "it has no commits"
	}
	owns := func(email string) bool {
		return slices.ContainsFunc(a.CommitEmails, func(e string) bool { return strings.EqualFold(e, email) })
	}
	for _, commit := range commits {
		if !owns(commit.AuthorEmail) || !owns(commit.CommitterEmail) {
			return fmt.Sprintf("commit %s was not authored and committed by %s's Auto Mode identity", shortSHA(commit.SHA), a.Username)
		}
	}
	return ""
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// matchAll reports whether every name matches one of patterns. No patterns
// match nothing, so a class that lists none fails closed.
func matchAll(patterns, names []string) bool {
	for _, name := range names {
		if !slices.ContainsFunc(patterns, func(p string) bool {
			match, err := doublestar.Match(p, name)
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
// decide TFC/class; they and triggered, the workspaces this trigger runs,
// must all be in the class. Lock and unlock create no run, so they skip it:
// `tfc unlock` must keep working when the Auto Mode config cannot be read.
func (t *TFCTrigger) resolveAutoMode(ctx context.Context, mr vcs.DetailedMR, triggered, affected []*TFCWorkspace) error {
	if !t.appCfg.AutoModeEnabled || t.GetVcsProvider() != "gitlab" ||
		(t.GetAction() != PlanAction && t.GetAction() != ApplyAction) {
		return nil
	}
	ctx, span := otel.Tracer(t.tracerName()).Start(ctx, "resolveAutoMode")
	defer span.End()

	// The default branch's config is read whatever the target, so that a
	// fallen-back MR retargeted to another branch still needs a person's
	// approval for `tfc apply`.
	autoMode, targetsDefault, err := loadDefaultBranchAutoModeConfig(ctx, t.gl, t.GetProjectNameWithNamespace(), mr.GetTargetBranch(), true)
	if err != nil {
		return err
	}
	author := autoMode.Author(mr.GetAuthor().GetUsername())
	if author == nil {
		if t.GetAction() == ApplyAction {
			// The author may have been removed from the config after the MR
			// fell back; its apply still needs a person's approval.
			return t.readFallbackOfUnlistedAuthor(autoMode)
		}
		return nil
	}
	t.autoModeConfig = autoMode
	// Once an MR has fallen back to Manual Mode it stays there, whatever its
	// target, author list, commits, files and plans look like now.
	manual, reason, err := t.runstream.IsAutoModeManual(t.autoModeRef())
	if err != nil {
		return fmt.Errorf("could not read whether the MR has fallen back to Manual Mode. %w", err)
	}
	if manual {
		t.fellBack = true
		log.Info().Str("project", t.GetProjectNameWithNamespace()).Int("mergeRequestID", t.GetMergeRequestIID()).
			Str("author", author.Username).Str("reason", reason).Msg("merge request has fallen back to Manual Mode")
		// Every commit is planned, so every commit of a fallen-back MR says
		// why it has no class, with a status that does not block the merge.
		if t.GetAction() == PlanAction {
			t.postClassStatus(ctx, vcs.CommitStateSkipped, ManualModeClassDescription(reason))
		}
		return nil
	}
	if !targetsDefault {
		return nil
	}
	// The class is decided from the MR's current files and commits, which
	// describe its head. A trigger for an older commit, such as a delayed
	// push event or comment, must not run that commit with the head's class.
	if head := mr.GetHeadSHA(); !strings.EqualFold(head, t.GetCommitSHA()) {
		return utils.CreatePermanentError(fmt.Errorf("refusing an Auto Mode %s of commit %s: the MR's head is now %s, and the class is decided from the head's changes",
			t.GetAction(), shortSHA(t.GetCommitSHA()), shortSHA(head)))
	}
	files, err := t.gl.GetMergeRequestModifiedFiles(ctx, t.GetMergeRequestIID(), t.GetProjectNameWithNamespace())
	if err != nil {
		return fmt.Errorf("could not list the MR's changed files. %w", err)
	}
	commits, err := t.gl.GetMergeRequestCommits(ctx, t.GetMergeRequestIID(), t.GetProjectNameWithNamespace())
	if err != nil {
		return fmt.Errorf("could not list the MR's commits. %w", err)
	}

	var workspaces []string
	for _, ws := range slices.Concat(affected, triggered) {
		if !slices.Contains(workspaces, ws.Name) {
			workspaces = append(workspaces, ws.Name)
		}
	}
	class, reason := autoMode.ClassFor(author, files, commits, workspaces)
	if class == ClassNone {
		log.Info().Str("project", t.GetProjectNameWithNamespace()).Int("mergeRequestID", t.GetMergeRequestIID()).
			Str("author", author.Username).Str("reason", reason).Msg("merge request fits no Auto Mode class, falls back to Manual Mode")
		// The mark keeps it there: a later push that makes the MR fit again
		// must not bring Auto Mode back. A plan decided here is already a
		// Manual Mode plan; anything else asks for one, since the commit's
		// plans so far carried the class marker.
		t.fellBack = true
		return t.fallBackToManualMode(ctx, reason, t.GetAction() != PlanAction)
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

// readFallbackOfUnlistedAuthor marks the trigger as fallen back when the MR,
// whose author autoMode does not list, has fallen back to Manual Mode anyway:
// its author was listed when it fell back.
func (t *TFCTrigger) readFallbackOfUnlistedAuthor(autoMode *AutoModeConfig) error {
	manual, reason, err := t.runstream.IsAutoModeManual(t.autoModeRef())
	if err != nil {
		return fmt.Errorf("could not read whether the MR has fallen back to Manual Mode. %w", err)
	}
	if manual {
		t.fellBack = true
		t.autoModeConfig = autoMode
		log.Info().Str("project", t.GetProjectNameWithNamespace()).Int("mergeRequestID", t.GetMergeRequestIID()).
			Str("reason", reason).Msg("merge request fell back to Manual Mode while its author was an Auto Mode author")
	}
	return nil
}

// AutoModeClassForMR returns the change class that a plan or apply of mr in
// project would run with right now, or ClassNone when it would run in Manual
// Mode, and the Auto Mode config it decided with. It decides through the same code as TriggerTFCEvents, so an approval
// and the apply it starts reach the same answer. The apply decides again, and
// refuses to run outside Auto Mode, so this is a filter, not the gate.
func AutoModeClassForMR(ctx context.Context, appCfg config.Config, gl vcs.GitClient, rs runstream.StreamClient, replanner ManualModeReplanner, project string, mr vcs.DetailedMR) (string, *AutoModeConfig, error) {
	ctx, span := otel.Tracer("GitlabHandler").Start(ctx, "AutoModeClassForMR")
	defer span.End()

	if !appCfg.AutoModeEnabled {
		return ClassNone, nil, nil
	}
	// Most MRs are people's: settle those without reading the MR's files.
	autoMode, err := LoadAutoModeConfig(ctx, gl, project, mr.GetTargetBranch())
	if err != nil {
		return ClassNone, nil, err
	}
	if autoMode.Author(mr.GetAuthor().GetUsername()) == nil {
		return ClassNone, autoMode, nil
	}

	t := &TFCTrigger{appCfg: appCfg, gl: gl, runstream: rs, replanner: replanner, cfg: &TFCTriggerOptions{
		Action:                   ApplyAction,
		Branch:                   mr.GetSourceBranch(),
		CommitSHA:                mr.GetHeadSHA(),
		ProjectNameWithNamespace: project,
		MergeRequestIID:          mr.GetInternalID(),
		TriggerSource:            MergeRequestEventTrigger,
		VcsProvider:              "gitlab",
	}}
	triggered, affected, err := t.getTriggeredWorkspacesForRequest(ctx, mr, false)
	if err != nil {
		return ClassNone, autoMode, fmt.Errorf("could not read triggered workspaces. %w", err)
	}
	if len(triggered) == 0 {
		return ClassNone, autoMode, nil
	}
	if err := t.resolveAutoMode(ctx, mr, triggered, affected); err != nil {
		return ClassNone, autoMode, err
	}
	if !t.cfg.AutoMode {
		return ClassNone, autoMode, nil
	}
	return t.cfg.ChangeClass, autoMode, nil
}

// requirePersonApproval refuses the apply of an MR that fell back to Manual
// Mode unless one of its current approvals is a person's. Automation such as
// an approval bot may have approved it while it was in Auto Mode, and as
// a code owner of the class files its approval alone can satisfy GitLab.
func (t *TFCTrigger) requirePersonApproval(ctx context.Context) error {
	if t.autoModeConfig == nil {
		// Without the config, an automatic approver cannot be told apart.
		return utils.CreatePermanentError(errors.New("refusing `tfc apply`: this MR fell back from Auto Mode to Manual Mode, and the default branch no longer has an Auto Mode config " +
			"to tell automatic approvers apart from people"))
	}
	approvals, err := t.gl.GetMergeRequestApprovals(ctx, t.GetMergeRequestIID(), t.GetProjectNameWithNamespace())
	if err != nil {
		return fmt.Errorf("could not read the MR's approvals to check for a person's: %w", err)
	}
	if approver := t.autoModeConfig.PersonApprover(approvals.GetApprovers()); approver != "" {
		log.Info().Str("project", t.GetProjectNameWithNamespace()).Int("mergeRequestID", t.GetMergeRequestIID()).
			Str("approver", approver).Msg("a person approved the Manual Mode MR")
		return nil
	}
	return utils.CreatePermanentError(errors.New("refusing `tfc apply`: this MR fell back from Auto Mode to Manual Mode, and none of its approvals is from a person. " +
		"Approvals from automatic approvers and Auto Mode authors do not count; a person must approve it"))
}

// postClassStatus publishes the MR's TFC/class status on this trigger's
// commit.
func (t *TFCTrigger) postClassStatus(ctx context.Context, state vcs.CommitState, description string) {
	postClassStatus(ctx, t.gl, vcs.ClassStatus{
		Project:         t.GetProjectNameWithNamespace(),
		CommitSHA:       t.GetCommitSHA(),
		MergeRequestIID: t.GetMergeRequestIID(),
		State:           state,
		Description:     description,
	})
}
