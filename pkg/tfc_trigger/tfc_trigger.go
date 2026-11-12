package tfc_trigger

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"

	"github.com/hashicorp/go-tfe"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/internal/config"

	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/utils"
	"github.com/zapier/tfbuddy/pkg/vcs"
)

// MRCommentTargetBranchEvalFailed is the MR-level warning when analysis cannot
// determine whether the target branch modified workspace-relevant paths.
const MRCommentTargetBranchEvalFailed = ":warning: Could not identify modified workspaces on target branch. Please review the plan carefully for unrelated changes."

const (
	ApplyAction TriggerAction = iota
	DestroyAction
	LockAction
	PlanAction
	RefreshAction
	UnlockAction
	InvalidAction
)

const tfPrefix = "tfbuddylock"

// tagRegex matches a lock held by a merge request. It is anchored so that an
// external holder containing "tfbuddylock-<n>" is never read as MR <n>.
var tagRegex = regexp.MustCompile(fmt.Sprintf("^%s-(\\d+)$", tfPrefix))

// externalLockRegex matches a lock held by something other than a merge
// request, such as automation that applies through the TFC API. The holder
// starts with a letter, so no tag matches both this and tagRegex, and uses only
// characters TFC allows in a tag (which it also lowercases).
var externalLockRegex = regexp.MustCompile(fmt.Sprintf("^%s-([a-z][a-z0-9_:-]*)$", tfPrefix))

func (a TriggerAction) String() string {
	switch a {
	case ApplyAction:
		return "apply"
	case DestroyAction:
		return "destroy"
	case LockAction:
		return "lock"
	case PlanAction:
		return "plan"
	case RefreshAction:
		return "refresh"
	case UnlockAction:
		return "unlock"
	default:
		return "invalid"
	}
}

func CheckTriggerAction(action string) TriggerAction {
	switch strings.ToLower(action) {
	case "plan":
		return PlanAction
	case "apply":
		return ApplyAction
	case "destroy":
		return DestroyAction
	case "lock":
		return LockAction
	case "unlock":
		return UnlockAction
	case "refresh":
		return RefreshAction
	default:
		return InvalidAction
	}
}

const (
	CommentTrigger TriggerSource = iota
	MergeRequestEventTrigger
)

type TFCTrigger struct {
	appCfg    config.Config
	cfg       *TFCTriggerOptions
	gl        vcs.GitClient
	tfc       tfc_api.ApiClient
	runstream runstream.StreamClient
	// replanner asks for the Manual Mode re-plan of an Auto Mode MR whose
	// class check fails.
	replanner ManualModeReplanner
	// autoModeConfig is the Auto Mode config resolveAutoMode read for an
	// Auto Mode author's MR, and fellBack whether that MR is in Manual Mode
	// for good.
	autoModeConfig *AutoModeConfig
	fellBack       bool
}

// SetManualModeReplanner sets what asks for the Manual Mode re-plan of an Auto
// Mode MR that falls back.
func (t *TFCTrigger) SetManualModeReplanner(r ManualModeReplanner) {
	t.replanner = r
}

type TFCTriggerOptions struct {
	Action                   TriggerAction
	Branch                   string
	CommitSHA                string
	ProjectNameWithNamespace string
	MergeRequestIID          int
	MergeRequestDiscussionID string
	MergeRequestRootNoteID   int64
	TriggerSource            TriggerSource
	VcsProvider              string
	// DeliveryID is the upstream webhook delivery ID (X-GitHub-Delivery /
	// X-Gitlab-Event-UUID). It identifies the trigger for the workspace
	// dispatch claim and the auto-merge apply generation, so a redelivery of
	// the same webhook is recognised while a new push or comment is not.
	DeliveryID string
	// AutoMergeGeneration groups every workspace run dispatched by one apply command.
	AutoMergeGeneration string
	// AutoMergeSequence is the monotonically increasing GitLab note ID.
	AutoMergeSequence int64
	Workspace         string `short:"w" long:"workspace" description:"A specific terraform Workspace to use" required:"false"`
	TFVersion         string `short:"v" long:"tf_version" description:"A specific terraform version to use" required:"false"`
	Target            string `short:"t" long:"target" description:"A specific terraform target to use" required:"false"`
	AllowEmptyRun     bool   `short:"e" long:"allow_empty_run" description:"A specific terraform AllowEmptyRun" required:"false"`

	// Auto Mode marks a merge request whose author runs in Auto Mode, AutoModeAuthor is
	// that author, ChangeClass is its change class and ClassWorkspaces the
	// workspaces whose plans decide TFC/class. TFBuddy sets all four from the
	// target branch's .tfbuddy.yaml; they have no flags, so no comment can set
	// them.
	AutoMode        bool
	AutoModeAuthor  string
	ChangeClass     string
	ClassWorkspaces []string
	// ClassPlan marks an Auto Mode plan of every affected workspace: its runs
	// decide TFC/class.
	ClassPlan bool
	// AutoModeApproval marks an apply that an Auto Mode approval started. It
	// must run in Auto Mode or not at all: the trigger decides the mode again,
	// and refuses the apply when the MR no longer fits a class.
	AutoModeApproval bool
}

func NewTFCTriggerConfig(opts *TFCTriggerOptions) (*TFCTriggerOptions, error) {
	err := opts.validate()
	if err != nil {
		return nil, utils.CreatePermanentError(err)
	}

	return opts, nil
}

func (tfcOpts *TFCTriggerOptions) validate() error {
	if tfcOpts == nil {
		return errors.New("cannot pass nil trigger options")
	}

	return nil
}

func NewTFCTrigger(
	appCfg config.Config,
	gl vcs.GitClient,
	tfc tfc_api.ApiClient,
	runstream runstream.StreamClient,
	cfg *TFCTriggerOptions,
) Trigger {

	return &TFCTrigger{
		appCfg:    appCfg,
		gl:        gl,
		tfc:       tfc,
		cfg:       cfg,
		runstream: runstream,
	}
}

// metrics
var (
	tfcRunsStarted = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tfbuddy_tfc_runs_started",
		Help: "Count of all TFC runs that have been triggered",
	},
		[]string{
			"organization",
			"workspace",
			"runType",
		},
	)
	autoMergeResults = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tfbuddy_auto_merge_results",
		Help: "Count of terminal results for eligible auto-merge changes",
	}, []string{"provider", "result"})
)

func init() {
	r := prometheus.DefaultRegisterer
	r.MustRegister(tfcRunsStarted)
	r.MustRegister(autoMergeResults)
}

func (t *TFCTrigger) SetMergeRequestRootNoteID(id int64) {
	t.cfg.MergeRequestRootNoteID = id
}
func (t *TFCTrigger) SetMergeRequestDiscussionID(mrDiscID string) {
	t.cfg.MergeRequestDiscussionID = mrDiscID
}
func (t *TFCTrigger) GetAction() TriggerAction {
	return t.cfg.Action
}
func (t *TFCTrigger) GetBranch() string {
	return t.cfg.Branch
}
func (t *TFCTrigger) GetCommitSHA() string {
	return t.cfg.CommitSHA
}
func (t *TFCTrigger) GetProjectNameWithNamespace() string {
	return t.cfg.ProjectNameWithNamespace
}
func (t *TFCTrigger) GetMergeRequestIID() int {
	return t.cfg.MergeRequestIID
}
func (t *TFCTrigger) GetMergeRequestDiscussionID() string {
	return t.cfg.MergeRequestDiscussionID
}
func (t *TFCTrigger) GetMergeRequestRootNoteID() int64 {
	return t.cfg.MergeRequestRootNoteID
}
func (t *TFCTrigger) GetTriggerSource() TriggerSource {
	return t.cfg.TriggerSource
}
func (t *TFCTrigger) GetVcsProvider() string {
	return t.cfg.VcsProvider
}
func (t *TFCTrigger) GetWorkspace() string {
	return t.cfg.Workspace
}

// tracerName derives the OpenTelemetry tracer name from VcsProvider so spans
// are correctly labeled regardless of which stream worker invoked this trigger.
func (t *TFCTrigger) tracerName() string {
	switch t.cfg.VcsProvider {
	case "github":
		return "GithubHandler"
	case "gitlab":
		return "GitlabHandler"
	default:
		return "TFCTrigger"
	}
}

// predefined errors
var (
	ErrWorkspaceNotDefined = errors.New("the workspace is not defined in " + ProjectConfigFilename)
	ErrNoChangesDetected   = errors.New("no changes detected for configured Terraform directories")
	ErrWorkspaceLocked     = errors.New("workspace is already locked")
	ErrWorkspaceUnlocked   = errors.New("workspace is already unlocked")
)

func FindLockingMR(ctx context.Context, tags []string, thisMR string) string {
	ctx, span := otel.Tracer("TFC").Start(ctx, "FindLockingMR")
	defer span.End()

	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		log.Debug().Msg(fmt.Sprintf("processing tag: '%s'", tag))
		matched := tagRegex.MatchString(tag)

		if !matched {
			log.Debug().Msg(fmt.Sprintf("did not match format for tag: %s != %s", tag, tagRegex.String()))
			continue
		}

		matches := tagRegex.FindStringSubmatch(tag)
		lockingMR := matches[1]

		if lockingMR == thisMR {
			log.Debug().Msg("disregarding lock for this MR")
			continue
		}
		return lockingMR
	}
	return ""
}

// FindExternalLock returns the holder of the first external lock in tags: a
// tfbuddylock-<holder> tag whose holder is not an MR number. TFBuddy never
// acquires or releases these itself; it only refuses to apply while one exists.
func FindExternalLock(tags []string) string {
	for _, tag := range tags {
		if matches := externalLockRegex.FindStringSubmatch(strings.TrimSpace(tag)); matches != nil {
			return matches[1]
		}
	}
	return ""
}

// findLockingMRs returns every MR other than thisMR that holds a lock tag.
func findLockingMRs(tags []string, thisMR string) []string {
	var lockingMRs []string
	for _, tag := range tags {
		matches := tagRegex.FindStringSubmatch(strings.TrimSpace(tag))
		if matches == nil || matches[1] == thisMR || slices.Contains(lockingMRs, matches[1]) {
			continue
		}
		lockingMRs = append(lockingMRs, matches[1])
	}
	return lockingMRs
}

// foreignLockHolder describes the first lock in tags that this MR does not
// hold, or returns "" if there is none.
func foreignLockHolder(ctx context.Context, tags []string, thisMR string) string {
	if lockingMR := FindLockingMR(ctx, tags, thisMR); lockingMR != "" {
		return "MR " + lockingMR
	}
	return FindExternalLock(tags)
}

// handleError both logs an error and reports it back to the Merge Request via an MR comment.
// the returned error is identical to the input parameter as a convenience
func (t *TFCTrigger) handleError(ctx context.Context, err error, msg string) error {
	ctx, span := otel.Tracer("TFC").Start(ctx, "handleError")
	defer span.End()
	span.RecordError(err)

	log.Error().Err(err).Msg(msg)
	if err := t.gl.CreateMergeRequestComment(ctx, t.GetMergeRequestIID(), t.GetProjectNameWithNamespace(), fmt.Sprintf("Error: %s: %v", msg, err)); err != nil {
		log.Error().Err(err).Msg("could not post error to Gitlab MR")
	}
	return err
}

// / postUpdate puts a message on a relevant MR
func (t *TFCTrigger) postUpdate(ctx context.Context, msg string) error {
	return t.gl.CreateMergeRequestComment(ctx, t.GetMergeRequestIID(), t.GetProjectNameWithNamespace(), msg)

}

// getLockTags returns the workspace's tfbuddylock-* tags.
func (t *TFCTrigger) getLockTags(ctx context.Context, workspace string) ([]string, error) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "getLockTags")
	defer span.End()

	tags, err := t.tfc.GetTagsByQuery(ctx, workspace, tfPrefix)
	if err != nil {
		log.Error().Err(err).Str("workspace", workspace).Msg("could not read lock tags")
		return nil, err
	}
	if len(tags) == 0 {
		log.Info().Msg("no tags returned")
	}
	return tags, nil
}

// releaseStaleMRLock removes lockingMR's lock tag if that MR has merged or
// closed, and otherwise returns an error naming the MR that holds the workspace.
func (t *TFCTrigger) releaseStaleMRLock(ctx context.Context, workspaceID, lockingMR string) error {
	lockingMRIID, convErr := strconv.Atoi(lockingMR)
	if convErr != nil {
		return fmt.Errorf("workspace is locked by another MR! %s", lockingMR)
	}
	lockingMRDetails, mrErr := t.gl.GetMergeRequest(ctx, lockingMRIID, t.GetProjectNameWithNamespace())
	if mrErr != nil {
		return fmt.Errorf("workspace is locked by another MR! %s", lockingMR)
	}
	state := lockingMRDetails.GetState()
	if state != "merged" && state != "closed" {
		return fmt.Errorf("workspace is locked by another MR! %s", lockingMRDetails.GetWebURL())
	}
	log.Warn().Str("workspace", workspaceID).Str("lockingMR", lockingMR).Str("state", state).
		Msg("auto-cleaning stale lock from merged/closed MR")
	// Remove the exact tag. A tag query matches substrings, so it would also
	// delete tfbuddylock-<n>0 or an external lock whose name contains this tag.
	tag := fmt.Sprintf("%s-%s", tfPrefix, lockingMR)
	if removeErr := t.tfc.RemoveTagsByName(ctx, workspaceID, []string{tag}); removeErr != nil {
		return fmt.Errorf("failed to auto-clean stale lock tag from MR %s: %w", lockingMRDetails.GetWebURL(), removeErr)
	}
	return nil
}

// getTriggeredWorkspaces resolves the workspaces modifiedFiles trigger.
// discoverRefs returns the branches discover rules list directories on; it is
// only called when the config has discover rules.
func (t *TFCTrigger) getTriggeredWorkspaces(ctx context.Context, modifiedFiles []string, discoverRefs func() []string) ([]*TFCWorkspace, []*TFCWorkspace, error) {
	ctx, span := otel.Tracer(t.tracerName()).Start(ctx, "getTriggeredWorkspaces")
	defer span.End()

	cfg, err := getProjectConfigFile(ctx, t.gl, t)
	if err != nil {
		if t.GetTriggerSource() == CommentTrigger {
			return nil, nil, fmt.Errorf("could not read .tfbuddy.yml file for this repo. %w", err)
		}
		// we got a webhook for a repo that has not enabled TFBuddy yet. Ignore.
		log.Debug().Msg("ignoring TFC trigger for project, missing .tfbuddy.yaml")
		return nil, nil, nil
	}

	var listDirs dirLister
	if len(cfg.Discover) > 0 {
		refs := discoverRefs()
		if len(refs) > 1 {
			triggered, err := t.workspacesAcrossRefs(ctx, cfg, modifiedFiles, refs)
			return triggered, triggered, err
		}
		listDirs = t.repoDirLister(refs[0])
		if err := cfg.discoverWorkspaces(ctx, listDirs, modifiedFiles); err != nil {
			return nil, nil, fmt.Errorf("could not discover workspaces. %w", err)
		}
	}

	affectedWorkspaces := cfg.triggeredWorkspaces(modifiedFiles)
	var triggeredWorkspaces []*TFCWorkspace
	if t.GetWorkspace() != "" {
		findProvidedWS := func() *TFCWorkspace {
			var providedWS *TFCWorkspace
			for _, ws := range cfg.Workspaces {
				log.Debug().Msg(ws.Name)
				if t.GetWorkspace() == ws.Name {
					providedWS = ws
				}
			}
			return providedWS
		}
		providedWS := findProvidedWS()
		if providedWS == nil && len(cfg.Discover) > 0 {
			// The MR's changes do not reach the named workspace, so only a
			// listing of every directory the rules could match can find it.
			if err := cfg.discoverAllWorkspaces(ctx, listDirs); err != nil {
				return nil, nil, fmt.Errorf("could not discover workspaces. %w", err)
			}
			providedWS = findProvidedWS()
		}
		if providedWS == nil {
			log.Warn().Str("workspace_arg", t.GetWorkspace()).Msg("provided workspace not configured for project")
			return nil, nil, utils.CreatePermanentError(ErrWorkspaceNotDefined)
		}
		triggeredWorkspaces = append(triggeredWorkspaces, providedWS)
	} else {
		triggeredWorkspaces = affectedWorkspaces
	}
	return triggeredWorkspaces, affectedWorkspaces, nil
}

type ErroredWorkspace struct {
	Name  string
	Error string
}
type TriggeredTFCWorkspaces struct {
	Errored  []*ErroredWorkspace
	Executed []string
}

func (t *TFCTrigger) getModifiedWorkspacesOnTargetBranch(ctx context.Context, mr vcs.MR, repo vcs.GitRepo, triggeredWorkspaces []*TFCWorkspace) (map[string]struct{}, error) {
	ctx, span := otel.Tracer(t.tracerName()).Start(ctx, "getModifiedWorkspacesOnTargetBranch")
	defer span.End()

	modifiedWSMap := make(map[string]struct{}, 0)
	// update the cloned repo to have the latest commits from target branch (usually main)
	err := repo.FetchUpstreamBranch(mr.GetTargetBranch())
	if err != nil {
		return modifiedWSMap, fmt.Errorf("could not fetch target branch %s. %w", mr.GetTargetBranch(), err)
	}
	// find merge base. This is the common commit between the source branch and the target branch. This is usually the commit a branch was forked from.
	// An Auto Mode run checks out the commit its run message names, which may
	// be behind the source branch's tip, so it diverges from that commit.
	source := mr.GetSourceBranch()
	if t.cfg.AutoMode {
		source = t.GetCommitSHA()
	}
	commonSHA, err := repo.GetMergeBase(source, mr.GetTargetBranch())
	if err != nil {
		return nil, fmt.Errorf("could not find merge base. %w", err)
	}
	log.Debug().Msgf("got common hash %s for source branch %s and target branch %s", commonSHA, mr.GetSourceBranch(), mr.GetTargetBranch())
	// got a merge base and two up to date branches. We can grab the target diffs now
	// we find files modified between the merge base and the HEAD of the target branch
	targetModifiedFiles, err := repo.GetModifiedFileNamesBetweenCommits(commonSHA, mr.GetTargetBranch())
	if err != nil {
		return modifiedWSMap, fmt.Errorf("could not find file diffs between %s and %s. %w", commonSHA, mr.GetTargetBranch(), err)
	}
	log.Debug().Msgf("%+v files modified between merge base and target branch (%s)", targetModifiedFiles, mr.GetTargetBranch())
	// if there's no modified files we can assume it's safe to continue
	if len(targetModifiedFiles) > 0 {
		// For each workspace the MR triggers, check if any files changed on the
		// target branch fall within that workspace's Dir (prefix match) or match
		// its TriggerDirs (glob match). This avoids the suffix-based matching in
		// workspaceForDir which can produce false positives across services.
		for _, ws := range triggeredWorkspaces {
			if hasChangesForWorkspace(ws, targetModifiedFiles) {
				log.Debug().Str("ws", ws.Name).Str("dir", ws.Dir).Msg("workspace has changes on target branch")
				modifiedWSMap[ws.Name] = struct{}{}
			}
		}
	}
	return modifiedWSMap, err
}

func (t *TFCTrigger) repoDirLister(ref string) dirLister {
	return func(ctx context.Context, dir string) ([]string, error) {
		return t.gl.ListRepoDirectories(ctx, t.GetProjectNameWithNamespace(), dir, ref)
	}
}

// workspacesAcrossRefs resolves the workspaces modifiedFiles trigger on each
// of refs separately and returns them together, once per workspace. Each
// branch is checked for duplicate names on its own: a move that keeps a
// workspace's name is valid on either branch, but looks like two directories
// sharing it when both are combined. A branch whose discovery fails is
// skipped, so cleanup still releases what the others find.
func (t *TFCTrigger) workspacesAcrossRefs(ctx context.Context, cfg *ProjectConfig, modifiedFiles, refs []string) ([]*TFCWorkspace, error) {
	var triggered []*TFCWorkspace
	seen := map[string]bool{}
	var firstErr error
	discovered := 0
	for _, ref := range refs {
		refCfg := cfg.clone()
		if err := refCfg.discoverWorkspaces(ctx, t.repoDirLister(ref), modifiedFiles); err != nil {
			log.Warn().Err(err).Str("ref", ref).Msg("could not discover workspaces on branch")
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		discovered++
		for _, ws := range refCfg.triggeredWorkspaces(modifiedFiles) {
			key := ws.Organization + "/" + ws.Name
			if !seen[key] {
				seen[key] = true
				triggered = append(triggered, ws)
			}
		}
	}
	if discovered == 0 {
		return nil, fmt.Errorf("could not discover workspaces. %w", firstErr)
	}
	return triggered, nil
}

// getTriggeredWorkspacesForRequest resolves the workspaces mr's changes
// trigger. Discover rules list directories on the MR's source branch, so a
// plan covers the directories the MR adds and none it deletes. Cleanup also
// discovers on the target branch: the source branch is often deleted on
// merge, and finding extra workspaces there is harmless because only this
// MR's own lock tag is removed.
func (t *TFCTrigger) getTriggeredWorkspacesForRequest(ctx context.Context, mr vcs.MR, forCleanup bool) ([]*TFCWorkspace, []*TFCWorkspace, error) {
	ctx, span := otel.Tracer(t.tracerName()).Start(ctx, "getTriggeredWorkspacesForRequest")
	defer span.End()

	mrModifiedFiles, err := t.gl.GetMergeRequestModifiedFiles(ctx, mr.GetInternalID(), t.GetProjectNameWithNamespace())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get a list of modified files. %w", err)
	}
	log.Debug().Str("project", t.GetProjectNameWithNamespace()).Int("mergeRequestID", mr.GetInternalID()).Strs("modifiedFiles", mrModifiedFiles).Msg("modified files")
	discoverRefs := func() []string {
		refs := []string{mr.GetSourceBranch()}
		if forCleanup && mr.GetTargetBranch() != mr.GetSourceBranch() {
			refs = append(refs, mr.GetTargetBranch())
		}
		return refs
	}
	return t.getTriggeredWorkspaces(ctx, mrModifiedFiles, discoverRefs)

}

// errAutoModeCommitUnavailable marks an Auto Mode run whose commit the clone
// does not contain.
var errAutoModeCommitUnavailable = errors.New("the clone does not contain the commit the run is for")

// checkoutAutoModeCommit checks out, for an Auto Mode run, the commit its run
// message names (sha=), rather than run whatever the source branch's tip is
// by the time TFBuddy clones it. It fails closed: an Auto Mode run is never
// created from any other commit.
func (t *TFCTrigger) checkoutAutoModeCommit(repo vcs.GitRepo) error {
	if !t.cfg.AutoMode {
		return nil
	}
	if err := repo.CheckoutCommit(t.GetCommitSHA()); err != nil {
		// utils.CreatePermanentError would drop the wrapped sentinel.
		return fmt.Errorf("refusing an Auto Mode %s: could not check out commit %s, which the run is for (%v): %w. %w",
			t.GetAction(), t.GetCommitSHA(), err, errAutoModeCommitUnavailable, utils.ErrPermanent)
	}
	return nil
}

// cloneGitRepo will clone the git repo for a specific MR and returns the temp path to be cleaned up later
func (t *TFCTrigger) cloneGitRepo(ctx context.Context, mr vcs.MR) (vcs.GitRepo, error) {
	ctx, span := otel.Tracer(t.tracerName()).Start(ctx, "cloneGitRepo")
	defer span.End()

	safeProj := strings.ReplaceAll(t.GetProjectNameWithNamespace(), "/", "-")
	cloneDir, err := os.MkdirTemp("", fmt.Sprintf("%s-%d-*", safeProj, t.GetMergeRequestIID()))
	if err != nil {
		return nil, fmt.Errorf("could not create tmp directory. %w", err)
	}
	repo, err := t.gl.CloneMergeRequest(ctx, t.GetProjectNameWithNamespace(), mr, cloneDir)
	if err != nil {
		return nil, utils.CreatePermanentError(err)
	}
	return repo, nil
}

func (t *TFCTrigger) ensureAutoMergeState(workspaces []*TFCWorkspace) error {
	if t.GetVcsProvider() == "" || !t.gl.SupportsAggregateAutoMerge() ||
		(t.GetAction() != PlanAction && t.GetAction() != ApplyAction) || len(workspaces) == 0 {
		return nil
	}

	workspaceKeys := make([]string, 0, len(workspaces))
	for _, workspace := range workspaces {
		workspaceKeys = append(workspaceKeys, runstream.AutoMergeWorkspaceKey(workspace.Organization, workspace.Name))
	}

	return t.runstream.EnsureAutoMergeState(runstream.NewAutoMergeState(
		t.GetVcsProvider(),
		t.GetProjectNameWithNamespace(),
		t.GetMergeRequestIID(),
		t.GetCommitSHA(),
		autoMergeEligible(t.appCfg, workspaces),
		workspaceKeys,
	))
}

func autoMergeEligible(appCfg config.Config, workspaces []*TFCWorkspace) bool {
	if !appCfg.AllowAutoMerge || len(workspaces) == 0 {
		return false
	}
	for _, workspace := range workspaces {
		if !workspace.AutoMerge || workspace.Mode != "apply-before-merge" ||
			!isWorkspaceAllowed(appCfg, workspace.Name, workspace.Organization) {
			return false
		}
	}
	return true
}

func (t *TFCTrigger) beginAutoMergeApply(workspaces []*TFCWorkspace) error {
	if t.GetVcsProvider() == "" || !t.gl.SupportsAggregateAutoMerge() ||
		t.GetAction() != ApplyAction || len(workspaces) == 0 {
		return nil
	}
	if t.cfg.AutoMergeGeneration == "" {
		switch {
		case t.cfg.DeliveryID != "":
			t.cfg.AutoMergeGeneration = t.cfg.DeliveryID
		case t.cfg.AutoMergeSequence > 0:
			t.cfg.AutoMergeGeneration = fmt.Sprintf("gitlab-note-%d", t.cfg.AutoMergeSequence)
		default:
			if t.cfg.AutoMode {
				// Auto Mode must merge what it applies.
				return errors.New("an Auto Mode apply needs auto-merge, and this apply has no stable GitLab delivery or note ID")
			}
			log.Warn().Msg("auto-merge disabled for apply without a stable GitLab delivery or note ID")
			return nil
		}
	}

	workspaceKeys := make([]string, 0, len(workspaces))
	for _, workspace := range workspaces {
		workspaceKeys = append(workspaceKeys, runstream.AutoMergeWorkspaceKey(workspace.Organization, workspace.Name))
	}
	return t.runstream.BeginAutoMergeApply(runstream.AutoMergeRef{
		VcsProvider:  t.GetVcsProvider(),
		Project:      t.GetProjectNameWithNamespace(),
		MergeRequest: t.GetMergeRequestIID(),
		CommitSHA:    t.GetCommitSHA(),
		Generation:   t.cfg.AutoMergeGeneration,
		Sequence:     t.cfg.AutoMergeSequence,
	}, workspaceKeys)
}

// TriggerTFCEvents dispatches one run per touched workspace. The clone and
// target-branch evaluation happen once per delivery, and every workspace is
// then run from that one checkout.
func (t *TFCTrigger) TriggerTFCEvents(ctx context.Context) (*TriggeredTFCWorkspaces, error) {
	ctx, span := otel.Tracer(t.tracerName()).Start(ctx, "TriggerTFCEvents")
	defer span.End()

	mr, err := t.gl.GetMergeRequest(ctx, t.GetMergeRequestIID(), t.GetProjectNameWithNamespace())
	if err != nil {
		return nil, fmt.Errorf("could not read MergeRequest data from VCS API: %w", err)
	}
	if t.cfg.AutoModeApproval && !strings.EqualFold(mr.GetHeadSHA(), t.GetCommitSHA()) {
		// The MR moved on after its approval was checked. The approval and
		// the gates were for another commit, so nothing runs.
		return nil, utils.CreatePermanentError(fmt.Errorf("refusing the apply an approval started: it is for commit %s, but the MR's head is now %s",
			shortSHA(t.GetCommitSHA()), shortSHA(mr.GetHeadSHA())))
	}
	triggeredWorkspaces, affectedWorkspaces, err := t.getTriggeredWorkspacesForRequest(ctx, mr, false)
	if err != nil {
		return nil, fmt.Errorf("could not read triggered workspaces. %w", err)
	}
	if len(triggeredWorkspaces) == 0 {
		if t.GetTriggerSource() == CommentTrigger {
			log.Error().Err(ErrNoChangesDetected).Msg("No Terraform changes found in changeset.")
			t.postUpdate(ctx, ErrNoChangesDetected.Error())
		} else {
			log.Debug().Msg("No Terraform changes found in changeset.")
		}
		return &TriggeredTFCWorkspaces{}, nil
	}

	if err := t.resolveAutoMode(ctx, mr, triggeredWorkspaces, affectedWorkspaces); err != nil {
		t.postWorkspaceStatusForAll(ctx, triggeredWorkspaces, vcs.CommitStateFailed,
			fmt.Sprintf("could not decide whether this MR runs in Auto Mode: %v", err))
		return nil, fmt.Errorf("could not decide whether this MR runs in Auto Mode: %w", err)
	}
	if t.cfg.AutoModeApproval && (!t.cfg.AutoMode || t.GetAction() != ApplyAction) {
		// The approval handler decided that the MR runs in Auto Mode, but this
		// decision, made again just now, did not. An approval is not a person
		// asking for a Manual Mode apply, so nothing runs.
		err := errors.New("refusing the apply an approval started: the MR does not run in Auto Mode")
		t.postWorkspaceStatusForAll(ctx, triggeredWorkspaces, vcs.CommitStateFailed, err.Error())
		return nil, utils.CreatePermanentError(err)
	}
	if t.fellBack && t.GetAction() == ApplyAction {
		if err := t.requirePersonApproval(ctx); err != nil {
			t.postWorkspaceStatusForAll(ctx, triggeredWorkspaces, vcs.CommitStateFailed,
				"refused: a fallen-back Auto Mode MR needs a person's approval")
			return nil, err
		}
	}
	if t.cfg.AutoMode && t.GetAction() == ApplyAction && !autoMergeEligible(t.appCfg, affectedWorkspaces) {
		// Auto Mode must merge what it applies, or live state stays ahead of the
		// target branch with nobody watching.
		err := errors.New("refusing an Auto Mode apply: auto-merge must be enabled, and every affected workspace must use mode apply-before-merge with autoMerge enabled")
		t.postWorkspaceStatusForAll(ctx, triggeredWorkspaces, vcs.CommitStateFailed, err.Error())
		return nil, utils.CreatePermanentError(err)
	}

	repo, err := t.cloneGitRepo(ctx, mr)
	if err != nil {
		t.postWorkspaceStatusForAll(ctx, triggeredWorkspaces, vcs.CommitStateFailed,
			fmt.Sprintf("could not clone the repository: %v", err))
		return nil, err
	}
	defer func() {
		if err := os.RemoveAll(repo.GetLocalDirectory()); err != nil {
			log.Error().Err(err).Str("path", repo.GetLocalDirectory()).Msg("could not remove cloned repository directory")
		}
	}()
	if err := t.checkoutAutoModeCommit(repo); err != nil {
		t.postWorkspaceStatusForAll(ctx, triggeredWorkspaces, vcs.CommitStateFailed, err.Error())
		return nil, err
	}

	blocked, err := t.getModifiedWorkspacesOnTargetBranch(ctx, mr, repo, triggeredWorkspaces)
	if err != nil && t.cfg.AutoMode {
		// Auto Mode has nobody reading a warning, so a divergence check that
		// cannot run blocks every workspace instead.
		t.postWorkspaceStatusForAll(ctx, triggeredWorkspaces, vcs.CommitStateFailed,
			"blocked: could not check the target branch for changes to workspace paths")
		if t.GetAction() == PlanAction {
			if ferr := t.fallBackToManualMode(ctx, "TFBuddy could not check the target branch for changes to workspace paths", true); ferr != nil {
				log.Error().Err(ferr).Msg("could not fall back to Manual Mode")
			}
		}
		return nil, utils.CreatePermanentError(fmt.Errorf("could not check the target branch for an Auto Mode MR: %w", err))
	}
	if err != nil {
		if err := t.postUpdate(ctx, MRCommentTargetBranchEvalFailed); err != nil {
			log.Error().Err(err).Msg("could not update MR with message")
		}
	}

	if err := t.ensureAutoMergeState(affectedWorkspaces); err != nil {
		t.postWorkspaceStatusForAll(ctx, triggeredWorkspaces, vcs.CommitStateFailed,
			fmt.Sprintf("could not initialize auto-merge state: %v", err))
		return nil, fmt.Errorf("could not initialize auto-merge state: %w", err)
	}
	if err := t.beginAutoMergeApply(triggeredWorkspaces); err != nil {
		t.postWorkspaceStatusForAll(ctx, triggeredWorkspaces, vcs.CommitStateFailed,
			fmt.Sprintf("could not initialize apply generation: %v", err))
		return nil, fmt.Errorf("could not initialize apply generation: %w", err)
	}

	classPlan := t.cfg.AutoMode && t.GetAction() == PlanAction && t.GetWorkspace() == ""
	classPlansStarted := false
	if classPlan {
		// Every class workspace is pending until its run reports, so a plan
		// that is still running keeps TFC/class pending, and one that never
		// starts fails it below. A redelivery of this trigger keeps the
		// plans it began: its dispatch claims start no new run, so the runs
		// registered so far are the ones that count.
		started, err := t.runstream.BeginClassPlans(t.classPlanRef(), t.cfg.ChangeClass, t.cfg.ClassWorkspaces, t.dispatchTrigger())
		if err != nil {
			t.postWorkspaceStatusForAll(ctx, triggeredWorkspaces, vcs.CommitStateFailed,
				fmt.Sprintf("could not initialize the class plans: %v", err))
			return nil, fmt.Errorf("could not initialize the class plans: %w", err)
		}
		t.cfg.ClassPlan = true
		classPlansStarted = started
		if started {
			t.postClassStatus(ctx, vcs.CommitStatePending, fmt.Sprintf("class %s: waiting for plans", t.cfg.ChangeClass))
		}
	}

	status := t.dispatchWorkspaces(ctx, triggeredWorkspaces, blocked, mr, repo)
	if t.cfg.AutoMode && t.GetAction() == ApplyAction && len(status.Errored) > 0 {
		// A workspace whose apply never started sends no run event, so no
		// run event can put the MR in Manual Mode for it. Without this, a
		// retry would be another Auto Mode apply.
		names := make([]string, 0, len(status.Errored))
		for _, ws := range status.Errored {
			names = append(names, "`"+ws.Name+"`")
		}
		if err := t.fallBackToManualMode(ctx, fmt.Sprintf("its apply of %s could not be started", strings.Join(names, ", ")), true); err != nil {
			log.Error().Err(err).Msg("could not fall back to Manual Mode")
		}
	}
	if classPlan {
		// A workspace that did not plan never counts as a clean one, and no
		// run event will arrive for it to fail the class later.
		notPlanned, finished := t.recordUnplannedWorkspaces(status)
		if len(notPlanned) > 0 {
			if err := t.fallBackToManualMode(ctx, fmt.Sprintf("%s did not plan", strings.Join(notPlanned, ", ")), true); err != nil {
				log.Error().Err(err).Msg("could not fall back to Manual Mode")
			}
		}
		if finished == nil && !classPlansStarted {
			// A redelivery: its first delivery may have finished the class
			// without reporting it, and no run event will come to report it.
			// (A delivery that also recorded its fallback is planned in
			// Manual Mode instead, which sets TFC/class to skipped.)
			state, err := t.runstream.GetClassPlans(t.classPlanRef())
			switch {
			case errors.Is(err, runstream.ErrClassPlansNotFound):
			case err != nil:
				return status, fmt.Errorf("could not read the class plans: %w", err)
			case state.Finished && !state.Reported:
				finished = state
			}
		}
		if finished != nil {
			if err := ReportClassPlans(ctx, t.gl, t.runstream, finished); err != nil {
				// The redelivery reports the finished class.
				return status, fmt.Errorf("could not report the class plans: %w", err)
			}
		}
	}
	return status, nil
}

// SkipTFCEvents discovers the same workspaces TriggerTFCEvents would trigger
// and posts a pending TFC/plan/<ws> and TFC/apply/<ws> commit status for each
// one, without ever cloning the repository or creating a Terraform Cloud run.
//
// It exists for callers that intentionally decline to plan a commit (e.g. a
// skip-plan label) but still need every affected workspace to carry a
// visible, per-workspace status: posting one synthetic status for the whole
// merge request would let a merge gate that checks per-workspace statuses
// pass newly-added workspaces straight through, unreviewed.
func (t *TFCTrigger) SkipTFCEvents(ctx context.Context, reason string) (*TriggeredTFCWorkspaces, error) {
	ctx, span := otel.Tracer(t.tracerName()).Start(ctx, "SkipTFCEvents")
	defer span.End()

	mr, err := t.gl.GetMergeRequest(ctx, t.GetMergeRequestIID(), t.GetProjectNameWithNamespace())
	if err != nil {
		return nil, fmt.Errorf("could not read MergeRequest data from VCS API: %w", err)
	}
	triggeredWorkspaces, _, err := t.getTriggeredWorkspacesForRequest(ctx, mr, false)
	if err != nil {
		return nil, fmt.Errorf("could not read triggered workspaces: %w", err)
	}
	if len(triggeredWorkspaces) == 0 {
		log.Debug().Msg("No Terraform changes found in changeset.")
		return &TriggeredTFCWorkspaces{}, nil
	}

	status := &TriggeredTFCWorkspaces{
		Errored:  make([]*ErroredWorkspace, 0),
		Executed: make([]string, 0),
	}
	for _, ws := range triggeredWorkspaces {
		if !isWorkspaceAllowed(t.appCfg, ws.Name, ws.Organization) {
			log.Info().Str("ws", ws.Name).Msg("Ignoring workspace, because of allow/deny list.")
			t.postWorkspaceStatus(ctx, ws, vcs.CommitStateSkipped,
				"skipped: excluded by TFBuddy configuration")
			status.Errored = append(status.Errored, &ErroredWorkspace{
				Name:  ws.Name,
				Error: "Ignoring workspace, because of allow/deny list.",
			})
			continue
		}
		t.postPendingStatusForAction(ctx, ws, PlanAction, reason)
		t.postPendingStatusForAction(ctx, ws, ApplyAction, reason)
		status.Executed = append(status.Executed, ws.Name)
	}
	return status, nil
}

// postPendingStatusForAction posts a pending TFC/<action>/<ws> commit status
// for one workspace, for the given action rather than t.GetAction(): unlike
// postWorkspaceStatus, SkipTFCEvents needs to post both a plan and an apply
// status per workspace regardless of which action this trigger was built
// for. Failures are logged, never propagated, matching postWorkspaceStatus.
func (t *TFCTrigger) postPendingStatusForAction(ctx context.Context, ws *TFCWorkspace, action TriggerAction, description string) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, statusWriteBudget)
		defer cancel()
	}
	if err := t.gl.SetWorkspaceStatus(ctx, vcs.WorkspaceStatus{
		Project:         t.GetProjectNameWithNamespace(),
		CommitSHA:       t.GetCommitSHA(),
		MergeRequestIID: t.GetMergeRequestIID(),
		Workspace:       ws.Name,
		Action:          action.String(),
		State:           vcs.CommitStatePending,
		Description:     description,
	}); err != nil {
		log.Error().Err(err).Str("ws", ws.Name).Str("action", action.String()).
			Msg("could not set workspace commit status")
	}
}

// postWorkspaceStatusForAll publishes the same terminal status for every
// triggered workspace. It covers merge-request-wide failures, which happen
// after the triggered set is known but before any workspace is dispatched:
// cloneGitRepo wraps its error with utils.CreatePermanentError, so that
// delivery is ACKed and never redelivered, and without this the merge request
// event path would disclose nothing at all.
func (t *TFCTrigger) postWorkspaceStatusForAll(ctx context.Context, workspaces []*TFCWorkspace, state vcs.CommitState, description string) {
	// These writes are back to back with no work between them, so one budget
	// covers the batch.
	ctx, cancel := context.WithTimeout(ctx, statusWriteBudget)
	defer cancel()
	for _, ws := range workspaces {
		t.postWorkspaceStatus(ctx, ws, state, description)
	}
}

// statusWriteBudget bounds one status write on the synchronous webhook path,
// so a slow VCS API cannot hold up the dispatch of the remaining workspaces.
//
// Giving up is cheap: a status posted without a pipeline ID still lands, on
// the implicit "external" pipeline. The budget is per write rather than
// shared across the dispatch loop, because a workspace's dispatch can take
// minutes and a shared deadline would expire before later workspaces got to
// report at all.
const statusWriteBudget = 3 * time.Second

// postWorkspaceStatus publishes a terminal TFC/<action>/<workspace> commit
// status. Every workspace TFBuddy declines to run must leave one behind,
// otherwise it vanishes from the merge request pipeline with no trace.
//
// Failures are logged, never propagated: a commit status is a notification,
// and losing one must not change what TFBuddy does with the workspace.
func (t *TFCTrigger) postWorkspaceStatus(ctx context.Context, ws *TFCWorkspace, state vcs.CommitState, description string) {
	action := t.GetAction()
	// Lock and unlock have no pipeline meaning; a TFC/lock/<ws> job would be
	// noise that never resolves.
	if action != PlanAction && action != ApplyAction {
		return
	}
	// Only impose the budget when the caller has not already set one, so a
	// batch (postWorkspaceStatusForAll) is bounded as a whole rather than per
	// workspace.
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, statusWriteBudget)
		defer cancel()
	}
	if err := t.gl.SetWorkspaceStatus(ctx, vcs.WorkspaceStatus{
		Project:         t.GetProjectNameWithNamespace(),
		CommitSHA:       t.GetCommitSHA(),
		MergeRequestIID: t.GetMergeRequestIID(),
		Workspace:       ws.Name,
		Action:          action.String(),
		State:           state,
		Description:     description,
	}); err != nil {
		log.Error().Err(err).Str("ws", ws.Name).Str("state", string(state)).
			Msg("could not set workspace commit status")
	}
}

func (t *TFCTrigger) dispatchWorkspaces(ctx context.Context, workspaces []*TFCWorkspace, blocked map[string]struct{}, mr vcs.DetailedMR, repo vcs.GitRepo) *TriggeredTFCWorkspaces {
	status := &TriggeredTFCWorkspaces{
		Errored:  make([]*ErroredWorkspace, 0),
		Executed: make([]string, 0),
	}
	for _, ws := range workspaces {
		if !isWorkspaceAllowed(t.appCfg, ws.Name, ws.Organization) {
			log.Info().Str("ws", ws.Name).Msg("Ignoring workspace, because of allow/deny list.")
			t.postWorkspaceStatus(ctx, ws, vcs.CommitStateSkipped,
				"skipped: excluded by TFBuddy configuration")
			status.Errored = append(status.Errored, &ErroredWorkspace{
				Name:  ws.Name,
				Error: "Ignoring workspace, because of allow/deny list.",
			})
			continue
		}
		if _, ok := blocked[ws.Name]; ok {
			log.Info().Str("ws", ws.Name).Str("dir", ws.Dir).Strs("triggerDirs", ws.TriggerDirs).
				Msg("Blocking workspace: relevant paths modified on target branch.")
			t.postWorkspaceStatus(ctx, ws, vcs.CommitStateFailed,
				"blocked: target branch modified workspace paths; rebase to resolve")
			status.Errored = append(status.Errored, &ErroredWorkspace{
				Name:  ws.Name,
				Error: fmt.Sprintf("Blocked: workspace-relevant paths (dir: '%s', triggerDirs: %v) have been modified on the target branch since this branch diverged. Please rebase/merge the target branch to resolve this.", ws.Dir, ws.TriggerDirs),
			})
			continue
		}
		if err := t.triggerRunForWorkspace(ctx, ws, mr, repo.GetLocalDirectory()); err != nil {
			log.Error().Err(err).Str("ws", ws.Name).Msg("could not trigger Run for Workspace")
			err = fmt.Errorf("could not trigger Run for Workspace. %w", err)
			t.postWorkspaceStatus(ctx, ws, vcs.CommitStateFailed, err.Error())
			status.Errored = append(status.Errored, &ErroredWorkspace{Name: ws.Name, Error: err.Error()})
			continue
		}
		status.Executed = append(status.Executed, ws.Name)
	}
	return status
}

func (t *TFCTrigger) TriggerCleanupEvent(ctx context.Context) error {
	ctx, span := otel.Tracer(t.tracerName()).Start(ctx, "TriggerCleanupEvent")
	defer span.End()

	mr, err := t.gl.GetMergeRequest(ctx, t.GetMergeRequestIID(), t.GetProjectNameWithNamespace())
	if err != nil {
		return fmt.Errorf("could not read MergeRequest data from VCS API: %w", err)
	}
	t.recordAutoMergeResult(mr)
	t.forgetAutoModeState(mr)
	triggeredWorkspaces, _, err := t.getTriggeredWorkspacesForRequest(ctx, mr, true)
	if err != nil {
		return fmt.Errorf("could not determine workspaces for merge cleanup. %w", err)
	}
	if len(triggeredWorkspaces) == 0 {
		return nil
	}
	var wsNames []string
	tag := fmt.Sprintf("%s-%d", tfPrefix, mr.GetInternalID())
	for _, cfgWS := range triggeredWorkspaces {
		ws, err := t.tfc.GetWorkspaceByName(ctx,
			cfgWS.Organization,
			cfgWS.Name)
		if err != nil {
			t.handleError(ctx, err, "error getting workspace")
			continue
		}
		tags, err := t.tfc.GetTagsByQuery(ctx,
			ws.ID,
			tag,
		)
		if err != nil {
			t.handleError(ctx, err, "error getting tags")
			continue
		}
		// TFC's tag query is a substring match, so a query for "tfbuddylock-5"
		// also returns "tfbuddylock-50" (locking MR #50). Filter for an exact
		// match before treating the workspace as one this MR locked.
		hasOurTag := false
		for _, name := range tags {
			if name == tag {
				hasOurTag = true
				break
			}
		}
		if !hasOurTag {
			continue
		}
		if err := t.tfc.RemoveTagsByName(ctx, ws.ID, []string{tag}); err != nil {
			t.handleError(ctx, err, "Error removing locking tag from workspace")
			continue
		}
		wsNames = append(wsNames, cfgWS.Name)
	}
	if len(wsNames) == 0 {
		return nil
	}
	_, err = t.gl.CreateMergeRequestDiscussion(ctx, mr.GetInternalID(),
		t.GetProjectNameWithNamespace(),
		fmt.Sprintf("Released locks for workspaces: %s", strings.Join(wsNames, ",")),
	)
	if err != nil {
		return fmt.Errorf("could not create MR discussion thread for TFC run status updates. %w", err)
	}
	return nil
}

// forgetAutoModeState drops a merged MR's Auto Mode state. A closed MR keeps
// it: reopening it must not bring Auto Mode back.
func (t *TFCTrigger) forgetAutoModeState(mr vcs.DetailedMR) {
	if !t.appCfg.AutoModeEnabled || t.GetVcsProvider() != "gitlab" || mr.GetState() != "merged" {
		return
	}
	if err := t.runstream.DeleteAutoModeMRState(t.autoModeRef()); err != nil {
		log.Warn().Err(err).Str("project", t.GetProjectNameWithNamespace()).Int("mergeRequestID", t.GetMergeRequestIID()).
			Msg("could not delete a merged MR's Auto Mode state")
	}
}

func (t *TFCTrigger) recordAutoMergeResult(mr vcs.DetailedMR) {
	if t.GetVcsProvider() == "" || !t.gl.SupportsAggregateAutoMerge() {
		return
	}
	ref := runstream.AutoMergeRef{
		VcsProvider:  t.GetVcsProvider(),
		Project:      t.GetProjectNameWithNamespace(),
		MergeRequest: t.GetMergeRequestIID(),
		CommitSHA:    t.GetCommitSHA(),
	}
	result, finalized, err := t.runstream.FinalizeAutoMerge(ref, mr.GetState() == "merged")
	if errors.Is(err, runstream.ErrAutoMergeStateNotFound) {
		return
	}
	if err != nil {
		log.Error().Err(err).
			Str("project", t.GetProjectNameWithNamespace()).
			Int("mergeRequestID", t.GetMergeRequestIID()).
			Msg("could not finalize auto-merge result")
		return
	}
	if finalized {
		autoMergeResults.WithLabelValues(t.GetVcsProvider(), string(result)).Inc()
	}
}

func (t *TFCTrigger) LockUnlockWorkspace(ws *tfe.Workspace, mr vcs.DetailedMR, lock bool) error {

	wsLocked := ws.Locked

	// workspace isn't in the state we desire
	if lock && !wsLocked ||
		!lock && wsLocked {
		err := t.tfc.LockUnlockWorkspace(
			context.Background(),
			ws.ID,
			fmt.Sprintf("tfbuddy locked for MR %d by %s at %s", mr.GetInternalID(), mr.GetAuthor().GetUsername(), mr.GetWebURL()),
			fmt.Sprintf("mr-%d", mr.GetInternalID()),
			lock,
		)
		return err
	}

	if lock {
		return ErrWorkspaceLocked
	}

	return ErrWorkspaceUnlocked
}

// dispatchTrigger identifies the webhook delivery or merge request comment
// that asked for this dispatch. It is empty when neither is known.
func (t *TFCTrigger) dispatchTrigger() string {
	if t.cfg.DeliveryID != "" {
		return "delivery:" + t.cfg.DeliveryID
	}
	if t.GetTriggerSource() == CommentTrigger && t.cfg.AutoMergeSequence > 0 {
		return fmt.Sprintf("note:%d", t.cfg.AutoMergeSequence)
	}
	return ""
}

// claimWorkspaceDispatch reports whether this trigger should dispatch ws. It
// is false when an earlier delivery of the same trigger already dispatched ws,
// which happens when JetStream redelivers a hook event: without the claim,
// the redelivered event would start a second run for every workspace.
//
// A claim is taken before the run is created and is never released, so each
// trigger dispatches a workspace at most once. A delivery that dies between
// the claim and the run leaves that workspace without a run; a new `tfc plan`
// comment is a new trigger and runs it. When the claim cannot be recorded the
// workspace is dispatched anyway, as it was before claims existed.
func (t *TFCTrigger) claimWorkspaceDispatch(ws *TFCWorkspace) bool {
	trigger := t.dispatchTrigger()
	if trigger == "" || t.runstream == nil {
		return true
	}
	claimed, err := t.runstream.ClaimWorkspaceDispatch(runstream.WorkspaceDispatchClaim{
		VcsProvider:  t.GetVcsProvider(),
		Project:      t.GetProjectNameWithNamespace(),
		MergeRequest: t.GetMergeRequestIID(),
		CommitSHA:    t.GetCommitSHA(),
		Action:       t.GetAction().String(),
		Trigger:      trigger,
		Organization: ws.Organization,
		Workspace:    ws.Name,
	})
	if err != nil {
		log.Warn().Err(err).Str("ws", ws.Name).Str("trigger", trigger).
			Msg("could not record workspace dispatch; dispatching without duplicate protection")
		return true
	}
	if !claimed {
		log.Info().Str("ws", ws.Name).Str("trigger", trigger).
			Msg("skipping workspace already dispatched by an earlier delivery of this trigger")
	}
	return claimed
}

func (t *TFCTrigger) triggerRunForWorkspace(ctx context.Context, cfgWS *TFCWorkspace, mr vcs.DetailedMR, cloneDir string) error {
	ctx, span := otel.Tracer("TFC").Start(ctx, "triggerRunForWorkspace")
	defer span.End()

	org := cfgWS.Organization
	wsName := cfgWS.Name

	if !t.claimWorkspaceDispatch(cfgWS) {
		return nil
	}

	// retrieve TFC workspace details, so we can sanity check this request.
	ws, err := t.tfc.GetWorkspaceByName(ctx, org, wsName)
	if err != nil {
		return fmt.Errorf("could not get Workspace from TFC API. %w", err)
	}

	// Check if workspace allows API driven runs
	if ws.VCSRepo != nil && t.GetAction() == ApplyAction {
		return fmt.Errorf("cannot trigger apply for VCS workspace. TFC workspace is configured with a VCS backend, must merge to trigger an Apply")
	}

	// if the run is a lock or unlock call that function and return.
	// the context in the repo isn't necessary
	if t.GetAction() == LockAction || t.GetAction() == UnlockAction {
		err = t.LockUnlockWorkspace(ws, mr, t.GetAction() == LockAction)
		// For unlock, treat ErrWorkspaceUnlocked as non-fatal so we still
		// clean up stale tfbuddylock-* tags even when the TFC lock is already released.
		if err != nil && !(t.GetAction() == UnlockAction && errors.Is(err, ErrWorkspaceUnlocked)) {
			return fmt.Errorf("error modifying the TFC lock on the workspace. %w", err)
		}
		// On unlock, also remove any tfbuddylock tags for this workspace
		if t.GetAction() == UnlockAction {
			if removeErr := t.tfc.RemoveTagsByQuery(ctx, ws.ID, tfPrefix); removeErr != nil {
				log.Warn().Err(removeErr).Str("workspace", wsName).Msg("failed to remove tfbuddylock tags during unlock")
			}
		}
		_, err := t.gl.CreateMergeRequestDiscussion(ctx, mr.GetInternalID(),
			t.GetProjectNameWithNamespace(),
			fmt.Sprintf("Successfully %sed Workspace `%s/%s`", t.GetAction(), org, wsName),
		)
		if err != nil {
			return fmt.Errorf("error posting successful lock modification status. %w", err)
		}
		return nil
	}

	pkgDir := filepath.Join(cloneDir, cfgWS.Dir)
	if ws.WorkingDirectory != "" {
		// The TFC workspace is configured with a working directory, so we need to send it the whole repo.
		pkgDir = cloneDir
	}

	isApply := false
	if t.GetAction() == ApplyAction {
		isApply = true
	} else if t.GetAction() != PlanAction {
		// Unsupported action — retrying won't change it. Mark permanent so
		// the worker ACKs and stops redelivering.
		return fmt.Errorf("run action was not apply or plan. %w", utils.ErrPermanent)
	}
	// Build the message before taking the lock, so a run TFBuddy refuses to
	// create never holds the workspace.
	message, err := t.runMessage(mr.GetTitle())
	if err != nil {
		return err
	}
	// If the workspace is locked tell the user and don't queue a run
	// Otherwise, TFC wil queue an apply, which might put them out of order
	if isApply {
		thisMR := fmt.Sprintf("%d", t.GetMergeRequestIID())
		// An unreadable lock state is not an unlocked one: refuse rather than
		// apply over a lock we could not see.
		lockTags, err := t.getLockTags(ctx, ws.ID)
		if err != nil {
			return fmt.Errorf("could not read the workspace lock tags. %w", err)
		}
		lockingMR := FindLockingMR(ctx, lockTags, thisMR)
		externalLock := FindExternalLock(lockTags)
		if ws.Locked {
			// Surface the tag-based locking MR too if we have one, so the user
			// has something actionable to investigate alongside the TFC lock.
			//
			// Wrap with utils.ErrPermanent so the gitlab_hooks worker ACKs the
			// note event instead of letting JetStream redeliver every 4s. The
			// previous behavior (using a stale nil `err` in %w) both produced
			// the "%!w(<nil>)" cosmetic glitch AND left the error retryable,
			// which is what created the 30+ duplicate "locked workspace"
			// comment storms observed on PEN-4703 (see MR !2224).
			if lockingMR != "" {
				return fmt.Errorf("refusing to Apply changes to a locked workspace (also tagged by MR %s). %w", lockingMR, utils.ErrPermanent)
			}
			if externalLock != "" {
				return fmt.Errorf("refusing to Apply changes to a locked workspace (also locked by %s). %w", externalLock, utils.ErrPermanent)
			}
			return fmt.Errorf("refusing to Apply changes to a locked workspace. %w", utils.ErrPermanent)
		} else if externalLock != "" {
			// Held by automation applying through the TFC API. Its holder removes
			// the tag when it is done; TFBuddy never auto-cleans it, because there
			// is no MR whose state says the lock is stale.
			return fmt.Errorf("refusing to Apply changes: workspace is locked by %s (tag `%s-%s`). Retry once it is released, or clear it with `tfc unlock -w %s`. %w",
				externalLock, tfPrefix, externalLock, wsName, utils.ErrPermanent)
		}
		// Every other MR's lock must be stale for this MR to proceed; checking
		// only the first would let an open MR's lock behind a stale one through.
		for _, foreignMR := range findLockingMRs(lockTags, thisMR) {
			if err := t.releaseStaleMRLock(ctx, ws.ID, foreignMR); err != nil {
				return err
			}
		}
		// Acquire the tag-based lock for the current MR
		err = t.tfc.AddTags(ctx,
			ws.ID,
			tfPrefix,
			thisMR,
		)
		if err != nil {
			return fmt.Errorf("error adding tags to workspace. %w", err)
		}
		// Tags are not an atomic lock: another writer can pass the same checks
		// before either tag lands. Every foreign lock seen above was either
		// released as stale or refused, so a foreign lock on the re-read belongs
		// to a writer that acquired concurrently: back off so the two writers
		// never both apply.
		//
		// Backing off keeps the tag. Every apply attempt for this MR shares it,
		// so removing it here could drop the lock another attempt's run is
		// relying on. Like after any apply, the MR holds the workspace until it
		// merges or closes, or until `tfc unlock` clears it.
		currentTags, err := t.getLockTags(ctx, ws.ID)
		if err != nil {
			return fmt.Errorf("could not verify the workspace lock after acquiring it. %w", err)
		}
		if contender := foreignLockHolder(ctx, currentTags, thisMR); contender != "" {
			return fmt.Errorf("workspace was locked by %s while this MR was acquiring the lock. Retry once it is released, or clear both locks with `tfc unlock -w %s`. %w",
				contender, wsName, utils.ErrPermanent)
		}
	}
	// create a new Merge Request discussion thread where status updates will be nested
	disc, err := t.gl.CreateMergeRequestDiscussion(ctx, mr.GetInternalID(),
		t.GetProjectNameWithNamespace(),
		fmt.Sprintf("Starting TFC %v for Workspace: `%s/%s`.\n", t.GetAction(), org, wsName)+
			utils.FormatTFBuddyMarker(wsName, t.GetAction().String()),
	)
	if err != nil {
		return fmt.Errorf("could not create MR discussion thread for TFC run status updates. %w", err)
	}
	// Keep these local rather than on t.cfg: each workspace has its own
	// discussion, and t.cfg is shared by every workspace of the trigger.
	discussionID := disc.GetDiscussionID()
	var rootNoteID int64
	if len(disc.GetMRNotes()) > 0 {
		rootNoteID = disc.GetMRNotes()[0].GetNoteID()
	} else {
		log.Debug().Msg("No MR Notes found")
	}

	// create new TFC run
	run, err := t.tfc.CreateRunFromSource(ctx, &tfc_api.ApiRunOptions{
		IsApply:       isApply,
		Path:          pkgDir,
		Message:       message,
		Organization:  org,
		Workspace:     wsName,
		TFVersion:     t.cfg.TFVersion,
		Target:        t.cfg.Target,
		AllowEmptyRun: t.cfg.AllowEmptyRun,
	})
	if err != nil {
		return fmt.Errorf("could not create TFC run. %w", err)
	}

	tfcRunsStarted.WithLabelValues(org, wsName, t.GetAction().String()).Inc()
	runstream.RecordRunCreated(t.GetVcsProvider(), t.GetAction().String())
	log.Debug().
		Str("RunID", run.ID).
		Str("Org", org).
		Str("WS", wsName).
		Bool("speculative", run.ConfigurationVersion.Speculative).
		Msg("created TFC run")

	return t.publishRunToStream(ctx, run, cfgWS, discussionID, rootNoteID)
}

func (t *TFCTrigger) publishRunToStream(ctx context.Context, run *tfe.Run, cfgWS *TFCWorkspace, discussionID string, rootNoteID int64) error {
	ctx, span := otel.Tracer("TFC").Start(ctx, "publishRunToStream")
	defer span.End()

	rmd := &runstream.TFRunMetadata{
		RunID:                                run.ID,
		Organization:                         run.Workspace.Organization.Name,
		Workspace:                            run.Workspace.Name,
		Source:                               "merge_request",
		Action:                               t.GetAction().String(),
		CommitSHA:                            t.GetCommitSHA(),
		MergeRequestProjectNameWithNamespace: t.GetProjectNameWithNamespace(),
		MergeRequestIID:                      t.GetMergeRequestIID(),
		DiscussionID:                         discussionID,
		RootNoteID:                           rootNoteID,
		VcsProvider:                          t.GetVcsProvider(),
		AutoMerge:                            cfgWS.AutoMerge,
		AutoMergeGeneration:                  t.cfg.AutoMergeGeneration,
		AutoMergeSequence:                    t.cfg.AutoMergeSequence,
	}
	if t.cfg.AutoMode {
		rmd.ChangeClass = t.cfg.ChangeClass
		rmd.ClassWorkspaces = t.cfg.ClassWorkspaces
	}
	//disable Auto Merge and log if the mode is not apply-before-merge
	if cfgWS.Mode != "apply-before-merge" && cfgWS.AutoMerge {
		log.Info().Str("RunID", run.ID).
			Str("Org", run.Workspace.Organization.Name).
			Str("WS", run.Workspace.Name).Msg("auto-merge cannot be enabled because the 'apply-before-merge' mode is not in use")
		rmd.AutoMerge = false
	}
	if cfgWS.AutoMerge && !t.appCfg.AllowAutoMerge {
		log.Info().Str("RunID", run.ID).
			Str("Org", run.Workspace.Organization.Name).
			Str("WS", run.Workspace.Name).Msg("auto-merge cannot be enabled since the feature is globally disabled")
		rmd.AutoMerge = false
	}
	if t.cfg.ClassPlan && t.GetAction() == PlanAction {
		// Before the metadata, so that the run's events, which wait for the
		// metadata, always find the run registered.
		err := t.runstream.RegisterClassPlanRun(t.classPlanRef(), run.Workspace.Name, run.ID, t.dispatchTrigger())
		switch {
		case errors.Is(err, runstream.ErrClassPlansSuperseded):
			// Another trigger planned the commit again meanwhile; its own
			// runs decide TFC/class.
			log.Info().Str("RunID", run.ID).Str("WS", run.Workspace.Name).
				Msg("class plans were begun again by another trigger; this run does not count")
		case err != nil:
			return fmt.Errorf("could not register the run for the class plans. %w", err)
		}
	}
	// waitForTFRunMetadata reads this back before any run event is routed, so
	// without it nothing will ever report on the run.
	err := t.runstream.AddRunMeta(rmd)
	if err != nil {
		return fmt.Errorf("could not publish Run metadata to event stream, updates may not be posted to MR. %w", err)
	}
	if t.GetVcsProvider() == "gitlab" && t.GetAction() == ApplyAction && t.cfg.Target == "" && t.cfg.AutoMergeGeneration != "" {
		if err := t.runstream.RegisterAutoMergeRun(runstream.AutoMergeRefForRun(rmd)); err != nil {
			// Not a dispatch failure: the run is live and TFC webhooks will
			// report its status. Only auto-merge is lost, and that fails safe —
			// RecordAutoMergeSuccess never claims a merge for an unregistered run.
			log.Error().Err(err).Str("RunID", run.ID).Str("WS", run.Workspace.Name).
				Msg("could not register Run for auto-merge coordination")
			if _, rerr := t.gl.AddMergeRequestDiscussionReply(ctx, t.GetMergeRequestIID(), t.GetProjectNameWithNamespace(), discussionID,
				":warning: Auto-merge is unavailable for this apply. The run is unaffected; merge manually once it succeeds."); rerr != nil {
				log.Error().Err(rerr).Str("RunID", run.ID).Msg("could not post auto-merge warning to MR")
			}
		}
	}

	if run.ConfigurationVersion.Speculative {
		// TFC doesn't send Notification webhooks for speculative plans, so we need to poll for updates.
		task := t.runstream.NewTFRunPollingTask(rmd, 1*time.Second)
		err := task.Schedule(ctx)

		if err != nil {
			// TFC sends no notifications for speculative plans, so with no
			// poller nothing will ever report on the run.
			return fmt.Errorf("failed to create TFC plan polling task. Updates may not be posted to MR. %w", err)
		}

	}

	return nil
}
