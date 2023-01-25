package tfc_trigger

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/hashicorp/go-tfe"
	"github.com/rs/zerolog/log"

	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/utils"
	"github.com/zapier/tfbuddy/pkg/vcs"
)

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
	cfg       *TFCTriggerOptions
	gl        vcs.GitClient
	tfc       tfc_api.ApiClient
	runstream runstream.StreamClient
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
	Workspace                string `short:"w" long:"workspace" description:"A specific terraform Workspace to use" required:"false"`
	TFVersion                string `short:"v" long:"tf_version" description:"A specific terraform version to use" required:"false"`
}

func NewTFCTriggerConfig(opts *TFCTriggerOptions) (*TFCTriggerOptions, error) {
	err := opts.validate()
	if err != nil {
		return nil, err
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
	gl vcs.GitClient,
	tfc tfc_api.ApiClient,
	runstream runstream.StreamClient,
	cfg *TFCTriggerOptions,
) Trigger {

	return &TFCTrigger{
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

// predefined errors
var (
	ErrWorkspaceNotDefined = errors.New("the workspace is not defined in " + ProjectConfigFilename)
	ErrNoChangesDetected   = errors.New("no changes detected for configured Terraform directories")
	ErrWorkspaceLocked     = errors.New("workspace is already locked")
	ErrWorkspaceUnlocked   = errors.New("workspace is already unlocked")
)

func FindLockingMR(tags []string, thisMR string) string {
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
func (t *TFCTrigger) handleError(err error, msg string) error {
	log.Error().Err(err).Msg(msg)
	if err := t.gl.CreateMergeRequestComment(t.GetMergeRequestIID(), t.GetProjectNameWithNamespace(), fmt.Sprintf("Error: %s: %v", msg, err)); err != nil {
		log.Error().Err(err).Msg("could not post error to Gitlab MR")
	}
	return err
}

// / postUpdate puts a message on a relevant MR
func (t *TFCTrigger) postUpdate(msg string) error {
	return t.gl.CreateMergeRequestComment(t.GetMergeRequestIID(), t.GetProjectNameWithNamespace(), msg)

}

func (t *TFCTrigger) getLockingMR(workspace string) string {
	tags, err := t.tfc.GetTagsByQuery(context.Background(), workspace, tfPrefix)
	if err != nil {
		log.Error().Err(err)
	}
	if len(tags) == 0 {
		log.Info().Msg("no tags returned")
		return ""
	}
	lockingMR := FindLockingMR(tags, fmt.Sprintf("%d", t.GetMergeRequestIID()))
	return lockingMR
}

func (t *TFCTrigger) getTriggeredWorkspaces(modifiedFiles []string) ([]*TFCWorkspace, error) {
	cfg, err := getProjectConfigFile(t.gl, t)
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

func (t *TFCTrigger) getModifiedWorkspaceBetweenMergeBaseTargetBranch(mr vcs.MR, repo vcs.GitRepo) (map[string]struct{}, error) {
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
		// use the same logic to find triggeredWorkspaces based on files modified between when the source branch was
		// forked and the current HEAD of the target branch
		targetBranchWorkspaces, err := t.getTriggeredWorkspaces(targetModifiedFiles)
		if err != nil {
			return modifiedWSMap, fmt.Errorf("could not find modified workspaces for target branch. %w", err)
		}
		for _, ws := range targetBranchWorkspaces {
			modifiedWSMap[ws.Name] = struct{}{}
		}
	}
	return modifiedWSMap, err
}
func (t *TFCTrigger) getTriggeredWorkspacesForRequest(mr vcs.MR) ([]*TFCWorkspace, error) {

	mrModifiedFiles, err := t.gl.GetMergeRequestModifiedFiles(mr.GetInternalID(), t.GetProjectNameWithNamespace())
	if err != nil {
		return nil, fmt.Errorf("failed to get a list of modified files. %w", err)
	}
	log.Debug().Strs("modifiedFiles", mrModifiedFiles).Msg("modified files")
	return t.getTriggeredWorkspaces(mrModifiedFiles)

}

// cloneGitRepo will clone the git repo for a specific MR and returns the temp path to be cleaned up later
func (t *TFCTrigger) cloneGitRepo(mr vcs.MR) (vcs.GitRepo, error) {
	safeProj := strings.ReplaceAll(t.GetProjectNameWithNamespace(), "/", "-")
	cloneDir, err := ioutil.TempDir("", fmt.Sprintf("%s-%d-*", safeProj, t.GetMergeRequestIID()))
	if err != nil {
		return nil, fmt.Errorf("could not create tmp directory. %w", err)
	}
	repo, err := t.gl.CloneMergeRequest(t.GetProjectNameWithNamespace(), mr, cloneDir)
	if err != nil {
		return nil, utils.CreatePermanentError(err)
	}
	return repo, nil
}
func (t *TFCTrigger) TriggerTFCEvents() (*TriggeredTFCWorkspaces, error) {
	mr, err := t.gl.GetMergeRequest(t.GetMergeRequestIID(), t.GetProjectNameWithNamespace())
	if err != nil {
		return nil, fmt.Errorf("could not read MergeRequest data from Gitlab API. %w", err)
	}
	triggeredWorkspaces, err := t.getTriggeredWorkspacesForRequest(mr)
	if err != nil {
		return nil, fmt.Errorf("could not read triggered workspaces. %w", err)
	}
	workspaceStatus := &TriggeredTFCWorkspaces{
		Errored:  make([]*ErroredWorkspace, 0),
		Executed: make([]string, 0),
	}
	if len(triggeredWorkspaces) > 0 {

		repo, err := t.cloneGitRepo(mr)
		if err != nil {
			return nil, err
		}
		defer os.Remove(repo.GetLocalDirectory())

		modifiedWSMap, err := t.getModifiedWorkspaceBetweenMergeBaseTargetBranch(mr, repo)
		if err != nil {
			err = t.postUpdate(":warning: Could not identify modified workspaces on target branch. Please review the plan carefully for unrelated changes.")
			if err != nil {
				log.Error().Err(err).Msg("could not update MR with message")
			}
		}
		for _, cfgWS := range triggeredWorkspaces {
			// check allow / deny lists
			if !isWorkspaceAllowed(cfgWS.Name, cfgWS.Organization) {
				log.Info().Str("ws", cfgWS.Name).Msg("Ignoring workspace, because of allow/deny list.")
				workspaceStatus.Errored = append(workspaceStatus.Errored, &ErroredWorkspace{
					Name:  cfgWS.Name,
					Error: "Ignoring workspace, because of allow/deny list.",
				})
				continue
			}
			if _, ok := modifiedWSMap[cfgWS.Name]; ok {
				//found in modified target
				log.Info().Str("ws", cfgWS.Name).Msg("Ignoring workspace, because it is modified in the target branch.")
				workspaceStatus.Errored = append(workspaceStatus.Errored, &ErroredWorkspace{
					Name:  cfgWS.Name,
					Error: "Ignoring workspace, because it is modified in the target branch. Please rebase/merge target branch to resolve this.",
				})
				continue
			}
			if err := t.triggerRunForWorkspace(cfgWS, mr, repo.GetLocalDirectory()); err != nil {
				log.Error().Err(err).Msg("could not trigger Run for Workspace")
				workspaceStatus.Errored = append(workspaceStatus.Errored, &ErroredWorkspace{
					Name:  cfgWS.Name,
					Error: fmt.Sprintf("could not trigger Run for Workspace. %v", err),
				})
				continue
			}
			workspaceStatus.Executed = append(workspaceStatus.Executed, cfgWS.Name)
		}

	} else if t.GetTriggerSource() == CommentTrigger {
		log.Error().Err(ErrNoChangesDetected)
		t.postUpdate(ErrNoChangesDetected.Error())
		return nil, nil

	} else {
		log.Debug().Msg("No Terraform changes found in changeset.")
		return nil, nil
	}

	return workspaceStatus, nil
}

func (t *TFCTrigger) TriggerCleanupEvent() error {
	mr, err := t.gl.GetMergeRequest(t.GetMergeRequestIID(), t.GetProjectNameWithNamespace())
	if err != nil {
		return fmt.Errorf("could not read MergeRequest data from Gitlab API. %w", err)
	}
	var wsNames []string
	cfg, err := getProjectConfigFile(t.gl, t)
	if err != nil {
		return fmt.Errorf("ignoring cleanup trigger for project, missing .tfbuddy.yaml. %w", err)
	}
	tag := fmt.Sprintf("%s-%d", tfPrefix, mr.GetInternalID())
	for _, cfgWS := range cfg.Workspaces {
		ws, err := t.tfc.GetWorkspaceByName(context.Background(),
			cfgWS.Organization,
			cfgWS.Name)
		if err != nil {
			t.handleError(err, "error getting workspace")
		}
		tags, err := t.tfc.GetTagsByQuery(context.Background(),
			ws.ID,
			tag,
		)
		if err != nil {
			t.handleError(err, "error getting tags")
		}
		if len(tags) != 0 {
			err = t.tfc.RemoveTagsByQuery(context.Background(), ws.ID, tag)
			if err != nil {
				t.handleError(err, "Error removing locking tag from workspace")
				continue
			}
			wsNames = append(wsNames, cfgWS.Name)
		}
	}
	_, err = t.gl.CreateMergeRequestDiscussion(mr.GetInternalID(),
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

func (t *TFCTrigger) triggerRunForWorkspace(cfgWS *TFCWorkspace, mr vcs.DetailedMR, cloneDir string) error {
	org := cfgWS.Organization
	wsName := cfgWS.Name

	// retrieve TFC workspace details, so we can sanity check this request.
	ws, err := t.tfc.GetWorkspaceByName(context.Background(), org, wsName)
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
		if err != nil {
			return fmt.Errorf("error modifying the TFC lock on the workspace. %w", err)
		}
		_, err := t.gl.CreateMergeRequestDiscussion(mr.GetInternalID(),
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
		lockingMR := t.getLockingMR(ws.ID)
		if ws.Locked {
			return fmt.Errorf("refusing to Apply changes to a locked workspace. %w", err)
		} else if lockingMR != "" {
			return fmt.Errorf("workspace is locked by another MR! %s", lockingMR)
		} else {
			err = t.tfc.AddTags(context.Background(),
				ws.ID,
				tfPrefix,
				fmt.Sprintf("%d", t.GetMergeRequestIID()),
			)
			if err != nil {
				return fmt.Errorf("error adding tags to workspace. %w", err)
			}
		}
	}
	// create a new Merge Request discussion thread where status updates will be nested
	disc, err := t.gl.CreateMergeRequestDiscussion(mr.GetInternalID(),
		t.GetProjectNameWithNamespace(),
		fmt.Sprintf("Starting TFC %v for Workspace: `%s/%s`.", t.GetAction(), org, wsName),
	)
	if err != nil {
		return fmt.Errorf("could not create MR discussion thread for TFC run status updates. %w", err)
	}
	t.SetMergeRequestDiscussionID(disc.GetDiscussionID())
	if len(disc.GetMRNotes()) > 0 {
		t.SetMergeRequestRootNoteID(disc.GetMRNotes()[0].GetNoteID())
	} else {
		log.Debug().Msg("No MR Notes found")
	}

	// create new TFC run
	run, err := t.tfc.CreateRunFromSource(&tfc_api.ApiRunOptions{
		IsApply:      isApply,
		Path:         pkgDir,
		Message:      fmt.Sprintf("MR [!%d]: %s", t.GetMergeRequestIID(), mr.GetTitle()),
		Organization: org,
		Workspace:    wsName,
		TFVersion:    t.cfg.TFVersion,
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

	return t.publishRunToStream(run)
}

func (t *TFCTrigger) publishRunToStream(run *tfe.Run) error {
	rmd := &runstream.TFRunMetadata{
		RunID:                                run.ID,
		Organization:                         run.Workspace.Organization.Name,
		Workspace:                            run.Workspace.Name,
		Source:                               "merge_request",
		Action:                               t.GetAction().String(),
		CommitSHA:                            t.GetCommitSHA(),
		MergeRequestProjectNameWithNamespace: t.GetProjectNameWithNamespace(),
		MergeRequestIID:                      t.GetMergeRequestIID(),
		DiscussionID:                         t.GetMergeRequestDiscussionID(),
		RootNoteID:                           t.GetMergeRequestRootNoteID(),
		VcsProvider:                          t.GetVcsProvider(),
	}
	err := t.runstream.AddRunMeta(rmd)
	if err != nil {
		return fmt.Errorf("could not publish Run metadata to event stream, updates may not be posted to MR. %w", err)
	}

	if run.ConfigurationVersion.Speculative {
		// TFC doesn't send Notification webhooks for speculative plans, so we need to poll for updates.
		task := t.runstream.NewTFRunPollingTask(rmd, 1*time.Second)
		err := task.Schedule()

		if err != nil {
			return fmt.Errorf("failed to create TFC plan polling task. Updates may not be posted to MR. %w", err)
		}

	}

	return nil
}
