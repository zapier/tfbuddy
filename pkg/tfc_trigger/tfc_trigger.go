package tfc_trigger

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

var tagRegex = regexp.MustCompile(fmt.Sprintf("%s\\-(\\d+)", tfPrefix))

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
	// workspaceStream, when set, fans out one message per workspace instead
	// of running them inline. Nil keeps the legacy synchronous behavior.
	workspaceStream WorkspacePublisher
}

type WorkspacePublisher interface {
	Publish(ctx context.Context, msg *WorkspaceTriggerMsg) error
}

func (t *TFCTrigger) SetWorkspaceStream(s WorkspacePublisher) {
	t.workspaceStream = s
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
	// X-Gitlab-Event-UUID). Used as the JetStream dedup anchor so retriggers
	// are not silently dropped within the dedup window.
	DeliveryID    string
	Workspace     string `short:"w" long:"workspace" description:"A specific terraform Workspace to use" required:"false"`
	TFVersion     string `short:"v" long:"tf_version" description:"A specific terraform version to use" required:"false"`
	Target        string `short:"t" long:"target" description:"A specific terraform target to use" required:"false"`
	AllowEmptyRun bool   `short:"e" long:"allow_empty_run" description:"A specific terraform AllowEmptyRun" required:"false"`
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
)

func init() {
	r := prometheus.DefaultRegisterer
	r.MustRegister(tfcRunsStarted)
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

func (t *TFCTrigger) getLockingMR(ctx context.Context, workspace string) string {
	ctx, span := otel.Tracer("TFC").Start(ctx, "getLockingMR")
	defer span.End()

	tags, err := t.tfc.GetTagsByQuery(ctx, workspace, tfPrefix)
	if err != nil {
		log.Error().Err(err)
	}
	if len(tags) == 0 {
		log.Info().Msg("no tags returned")
		return ""
	}
	lockingMR := FindLockingMR(ctx, tags, fmt.Sprintf("%d", t.GetMergeRequestIID()))
	return lockingMR
}

func (t *TFCTrigger) getTriggeredWorkspaces(ctx context.Context, modifiedFiles []string) ([]*TFCWorkspace, error) {
	ctx, span := otel.Tracer(t.tracerName()).Start(ctx, "getTriggeredWorkspaces")
	defer span.End()

	cfg, err := getProjectConfigFile(ctx, t.gl, t)
	if err != nil {
		if t.GetTriggerSource() == CommentTrigger {
			return nil, fmt.Errorf("could not read .tfbuddy.yml file for this repo. %w", err)
		}
		// we got a webhook for a repo that has not enabled TFBuddy yet. Ignore.
		log.Debug().Msg("ignoring TFC trigger for project, missing .tfbuddy.yaml")
		return nil, nil
	}

	var triggeredWorkspaces []*TFCWorkspace
	if t.GetWorkspace() != "" {
		var providedWS *TFCWorkspace
		for _, ws := range cfg.Workspaces {
			log.Debug().Msg(ws.Name)
			if t.GetWorkspace() == ws.Name {
				providedWS = ws
			}
		}
		if providedWS == nil {
			log.Warn().Str("workspace_arg", t.GetWorkspace()).Msg("provided workspace not configured for project")
			return nil, utils.CreatePermanentError(ErrWorkspaceNotDefined)
		}
		triggeredWorkspaces = append(triggeredWorkspaces, providedWS)
	} else {
		// check the MR modified files list against the .tfbuddy.yaml configured directories
		triggeredWorkspaces = cfg.triggeredWorkspaces(modifiedFiles)
	}
	return triggeredWorkspaces, nil
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
	commonSHA, err := repo.GetMergeBase(mr.GetSourceBranch(), mr.GetTargetBranch())
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
func (t *TFCTrigger) getTriggeredWorkspacesForRequest(ctx context.Context, mr vcs.MR) ([]*TFCWorkspace, error) {
	ctx, span := otel.Tracer(t.tracerName()).Start(ctx, "getTriggeredWorkspacesForRequest")
	defer span.End()

	mrModifiedFiles, err := t.gl.GetMergeRequestModifiedFiles(ctx, mr.GetInternalID(), t.GetProjectNameWithNamespace())
	if err != nil {
		return nil, fmt.Errorf("failed to get a list of modified files. %w", err)
	}
	log.Debug().Str("project", t.GetProjectNameWithNamespace()).Int("mergeRequestID", mr.GetInternalID()).Strs("modifiedFiles", mrModifiedFiles).Msg("modified files")
	return t.getTriggeredWorkspaces(ctx, mrModifiedFiles)

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

// TriggerTFCEvents dispatches one run per touched workspace. The clone and
// target-branch evaluation happen once per delivery so the fan-out path
// doesn't redo MR-level work in every worker.
func (t *TFCTrigger) TriggerTFCEvents(ctx context.Context) (*TriggeredTFCWorkspaces, error) {
	ctx, span := otel.Tracer(t.tracerName()).Start(ctx, "TriggerTFCEvents")
	defer span.End()

	mr, err := t.gl.GetMergeRequest(ctx, t.GetMergeRequestIID(), t.GetProjectNameWithNamespace())
	if err != nil {
		return nil, fmt.Errorf("could not read MergeRequest data from VCS API: %w", err)
	}
	triggeredWorkspaces, err := t.getTriggeredWorkspacesForRequest(ctx, mr)
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

	repo, err := t.cloneGitRepo(ctx, mr)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := os.RemoveAll(repo.GetLocalDirectory()); err != nil {
			log.Error().Err(err).Str("path", repo.GetLocalDirectory()).Msg("could not remove cloned repository directory")
		}
	}()

	blocked, err := t.getModifiedWorkspacesOnTargetBranch(ctx, mr, repo, triggeredWorkspaces)
	if err != nil {
		if err := t.postUpdate(ctx, MRCommentTargetBranchEvalFailed); err != nil {
			log.Error().Err(err).Msg("could not update MR with message")
		}
	}

	dispatch := t.runInline(mr, repo)
	if t.workspaceStream != nil {
		dispatch = t.enqueue
	}
	return t.dispatchWorkspaces(ctx, triggeredWorkspaces, blocked, dispatch), nil
}

// workspaceDispatchFn runs (inline) or enqueues (fan-out) a single workspace.
// Returning an error puts the workspace into status.Errored verbatim.
type workspaceDispatchFn func(ctx context.Context, ws *TFCWorkspace) error

func (t *TFCTrigger) dispatchWorkspaces(ctx context.Context, workspaces []*TFCWorkspace, blocked map[string]struct{}, dispatch workspaceDispatchFn) *TriggeredTFCWorkspaces {
	status := &TriggeredTFCWorkspaces{
		Errored:  make([]*ErroredWorkspace, 0),
		Executed: make([]string, 0),
	}
	for _, ws := range workspaces {
		if !isWorkspaceAllowed(t.appCfg, ws.Name, ws.Organization) {
			log.Info().Str("ws", ws.Name).Msg("Ignoring workspace, because of allow/deny list.")
			status.Errored = append(status.Errored, &ErroredWorkspace{
				Name:  ws.Name,
				Error: "Ignoring workspace, because of allow/deny list.",
			})
			continue
		}
		if _, ok := blocked[ws.Name]; ok {
			log.Info().Str("ws", ws.Name).Str("dir", ws.Dir).Strs("triggerDirs", ws.TriggerDirs).
				Msg("Blocking workspace: relevant paths modified on target branch.")
			status.Errored = append(status.Errored, &ErroredWorkspace{
				Name:  ws.Name,
				Error: fmt.Sprintf("Blocked: workspace-relevant paths (dir: '%s', triggerDirs: %v) have been modified on the target branch since this branch diverged. Please rebase/merge the target branch to resolve this.", ws.Dir, ws.TriggerDirs),
			})
			continue
		}
		if err := dispatch(ctx, ws); err != nil {
			status.Errored = append(status.Errored, &ErroredWorkspace{Name: ws.Name, Error: err.Error()})
			continue
		}
		status.Executed = append(status.Executed, ws.Name)
	}
	return status
}

func (t *TFCTrigger) runInline(mr vcs.DetailedMR, repo vcs.GitRepo) workspaceDispatchFn {
	return func(ctx context.Context, ws *TFCWorkspace) error {
		if err := t.triggerRunForWorkspace(ctx, ws, mr, repo.GetLocalDirectory()); err != nil {
			log.Error().Err(err).Str("ws", ws.Name).Msg("could not trigger Run for Workspace")
			return fmt.Errorf("could not trigger Run for Workspace. %w", err)
		}
		return nil
	}
}

func (t *TFCTrigger) enqueue(ctx context.Context, ws *TFCWorkspace) error {
	msg := &WorkspaceTriggerMsg{Opts: *t.cfg, Workspace: *ws}
	if err := t.workspaceStream.Publish(ctx, msg); err != nil {
		log.Error().Err(err).Str("ws", ws.Name).Msg("could not enqueue workspace trigger")
		return fmt.Errorf("could not enqueue workspace trigger. %w", err)
	}
	return nil
}

func (t *TFCTrigger) TriggerCleanupEvent(ctx context.Context) error {
	ctx, span := otel.Tracer(t.tracerName()).Start(ctx, "TriggerCleanupEvent")
	defer span.End()

	mr, err := t.gl.GetMergeRequest(ctx, t.GetMergeRequestIID(), t.GetProjectNameWithNamespace())
	if err != nil {
		return fmt.Errorf("could not read MergeRequest data from VCS API: %w", err)
	}
	triggeredWorkspaces, err := t.getTriggeredWorkspacesForRequest(ctx, mr)
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

func (t *TFCTrigger) triggerRunForWorkspace(ctx context.Context, cfgWS *TFCWorkspace, mr vcs.DetailedMR, cloneDir string) error {
	ctx, span := otel.Tracer("TFC").Start(ctx, "triggerRunForWorkspace")
	defer span.End()

	org := cfgWS.Organization
	wsName := cfgWS.Name

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
		return fmt.Errorf("run action was not apply or plan. %w", err)
	}
	// If the workspace is locked tell the user and don't queue a run
	// Otherwise, TFC wil queue an apply, which might put them out of order
	if isApply {
		lockingMR := t.getLockingMR(ctx, ws.ID)
		if ws.Locked {
			// Surface the tag-based locking MR too if we have one, so the user
			// has something actionable to investigate alongside the TFC lock.
			if lockingMR != "" {
				return fmt.Errorf("refusing to Apply changes to a locked workspace (also tagged by MR %s). %w", lockingMR, err)
			}
			return fmt.Errorf("refusing to Apply changes to a locked workspace. %w", err)
		} else if lockingMR != "" {
			// Check if locking MR is already merged/closed (stale lock)
			lockingMRIID, convErr := strconv.Atoi(lockingMR)
			if convErr == nil {
				lockingMRDetails, mrErr := t.gl.GetMergeRequest(ctx, lockingMRIID, t.GetProjectNameWithNamespace())
				if mrErr == nil {
					state := lockingMRDetails.GetState()
					if state == "merged" || state == "closed" {
						log.Warn().Str("workspace", ws.ID).Str("lockingMR", lockingMR).Str("state", state).
							Msg("auto-cleaning stale lock from merged/closed MR")
						tag := fmt.Sprintf("%s-%s", tfPrefix, lockingMR)
						if removeErr := t.tfc.RemoveTagsByQuery(ctx, ws.ID, tag); removeErr != nil {
							return fmt.Errorf("failed to auto-clean stale lock tag from MR %s: %w", lockingMRDetails.GetWebURL(), removeErr)
						}
						// Stale lock removed — fall through to acquire lock for current MR
					} else {
						return fmt.Errorf("workspace is locked by another MR! %s", lockingMRDetails.GetWebURL())
					}
				} else {
					return fmt.Errorf("workspace is locked by another MR! %s", lockingMR)
				}
			} else {
				return fmt.Errorf("workspace is locked by another MR! %s", lockingMR)
			}
		}
		// Acquire the tag-based lock for the current MR
		err = t.tfc.AddTags(ctx,
			ws.ID,
			tfPrefix,
			fmt.Sprintf("%d", t.GetMergeRequestIID()),
		)
		if err != nil {
			return fmt.Errorf("error adding tags to workspace. %w", err)
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
	// Keep these local — concurrent fan-out workers must not share state via t.cfg.
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
		Message:       fmt.Sprintf("MR [!%d]: %s", t.GetMergeRequestIID(), mr.GetTitle()),
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
	err := t.runstream.AddRunMeta(rmd)
	if err != nil {
		return fmt.Errorf("could not publish Run metadata to event stream, updates may not be posted to MR. %w", err)
	}

	if run.ConfigurationVersion.Speculative {
		// TFC doesn't send Notification webhooks for speculative plans, so we need to poll for updates.
		task := t.runstream.NewTFRunPollingTask(rmd, 1*time.Second)
		err := task.Schedule(ctx)

		if err != nil {
			return fmt.Errorf("failed to create TFC plan polling task. Updates may not be posted to MR. %w", err)
		}

	}

	return nil
}
