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
	"github.com/zapier/tfbuddy/pkg/vcs"
)

const (
	ApplyAction TriggerAction = iota
	DestroyAction
	LockAction
	PlanAction
	RefreshAction
	UnlockAction
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

const (
	CommentTrigger TriggerSource = iota
	MergeRequestEventTrigger
)

type TFCTrigger struct {
	cfg       TriggerConfig
	gl        vcs.GitClient
	tfc       tfc_api.ApiClient
	runstream runstream.StreamClient
}

type TFCTriggerConfig struct {
	Action                   TriggerAction
	Branch                   string
	CommitSHA                string
	ProjectNameWithNamespace string
	MergeRequestIID          int
	MergeRequestDiscussionID string
	MergeRequestRootNoteID   int64
	TriggerSource            TriggerSource
	VcsProvider              string
	Workspace                string
}

func NewTFCTrigger(
	gl vcs.GitClient,
	tfc tfc_api.ApiClient,
	runstream runstream.StreamClient,
	cfg TriggerConfig,
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
func (t *TFCTrigger) GetConfig() TriggerConfig {
	return t.cfg
}
func (tC *TFCTriggerConfig) SetMergeRequestRootNoteID(id int64) {
	tC.MergeRequestRootNoteID = id
}
func (tC *TFCTriggerConfig) SetAction(action TriggerAction) {
	tC.Action = action
}
func (tC *TFCTriggerConfig) SetWorkspace(workspace string) {
	tC.Workspace = workspace
}
func (tC *TFCTriggerConfig) SetMergeRequestDiscussionID(mrDiscID string) {
	tC.MergeRequestDiscussionID = mrDiscID
}
func (tC *TFCTriggerConfig) GetAction() TriggerAction {
	return tC.Action
}
func (tC *TFCTriggerConfig) GetBranch() string {
	return tC.Branch
}
func (tC *TFCTriggerConfig) GetCommitSHA() string {
	return tC.CommitSHA
}
func (tC *TFCTriggerConfig) GetProjectNameWithNamespace() string {
	return tC.ProjectNameWithNamespace
}
func (tC *TFCTriggerConfig) GetMergeRequestIID() int {
	return tC.MergeRequestIID
}
func (tC *TFCTriggerConfig) GetMergeRequestDiscussionID() string {
	return tC.MergeRequestDiscussionID
}
func (tC *TFCTriggerConfig) GetMergeRequestRootNoteID() int64 {
	return tC.MergeRequestRootNoteID
}
func (tC *TFCTriggerConfig) GetTriggerSource() TriggerSource {
	return tC.TriggerSource
}
func (tC *TFCTriggerConfig) GetVcsProvider() string {
	return tC.VcsProvider
}
func (tC *TFCTriggerConfig) GetWorkspace() string {
	return tC.Workspace
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
	if err := t.gl.CreateMergeRequestComment(t.cfg.GetMergeRequestIID(), t.cfg.GetProjectNameWithNamespace(), fmt.Sprintf("Error: %s: %v", msg, err)); err != nil {
		log.Error().Err(err).Msg("could not post error to Gitlab MR")
	}
	return err
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
	lockingMR := FindLockingMR(tags, fmt.Sprintf("%d", t.cfg.GetMergeRequestIID()))
	return lockingMR
}

func (t *TFCTrigger) getTriggeredWorkspaces(modifiedFiles []string) ([]*TFCWorkspace, error) {
	cfg, err := getProjectConfigFile(t.gl, t)
	if err != nil {
		if t.cfg.GetTriggerSource() == CommentTrigger {
			return nil, t.handleError(err, "could not read .tfbuddy.yml file for this repo")
		}
		// we got a webhook for a repo that has not enabled TFBuddy yet. Ignore.
		log.Debug().Msg("ignoring TFC trigger for project, missing .tfbuddy.yaml")
		return nil, nil
	}

	var triggeredWorkspaces []*TFCWorkspace
	if t.cfg.GetWorkspace() != "" {
		var providedWS *TFCWorkspace
		for _, ws := range cfg.Workspaces {
			log.Debug().Msg(ws.Name)
			if t.cfg.GetWorkspace() == ws.Name {
				providedWS = ws
			}
		}
		if providedWS == nil {
			log.Warn().Str("workspace_arg", t.cfg.GetWorkspace()).Msg("provided workspace not configured for project")
			return nil, t.handleError(ErrWorkspaceNotDefined, t.cfg.GetWorkspace())
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

func (t *TFCTrigger) getModifiedWorkspaceBetweenMergeBaseTargetBranch(mr vcs.MR, repo vcs.GitRepo) (map[string]byte, error) {
	modifiedWSMap := make(map[string]byte, 0)
	// update the cloned repo to have the latest commits from target branch (usually main)
	err := repo.FetchUpstreamBranch(mr.GetTargetBranch())
	if err != nil {
		return modifiedWSMap, t.handleError(err, fmt.Sprintf("could not fetch target branch %s", mr.GetTargetBranch()))
	}
	// find merge base. This is the common commit between the source branch and the target branch. This is usually the commit a branch was forked from.
	commonSHA, err := repo.GetMergeBase(mr.GetSourceBranch(), mr.GetTargetBranch())
	if err != nil {
		return nil, t.handleError(err, "could not find merge base")
	}
	log.Info().Msgf("got common %s", commonSHA)
	// got a merge base and two up to date branches. We can grab the target diffs now
	// we find files modified between the merge base and the HEAD of the target branch
	targetModifiedFiles, err := repo.GetModifiedFileNamesBetweenCommits(commonSHA, mr.GetTargetBranch())
	if err != nil {
		return modifiedWSMap, t.handleError(err, fmt.Sprintf("could not find file diffs between %s and %s", commonSHA, mr.GetTargetBranch()))
	}
	log.Debug().Msgf("%+v files modified between merge base and target branch", targetModifiedFiles)
	// if there's no modified files we can assume it's safe to continue
	if len(targetModifiedFiles) > 0 {
		// use the same logic to find triggeredWorkspaces based on files modified between when the source branch was
		// forked and the current HEAD of the target branch
		targetBranchWorkspaces, err := t.getTriggeredWorkspaces(targetModifiedFiles)
		if err != nil {
			return modifiedWSMap, t.handleError(err, "could not find modified workspaces for target branch")
		}
		for _, ws := range targetBranchWorkspaces {
			modifiedWSMap[ws.Name] = 0
		}
	}
	return modifiedWSMap, err
}
func (t *TFCTrigger) getTriggeredWorkspacesForRequest(mr vcs.MR) ([]*TFCWorkspace, error) {

	mrModifiedFiles, err := t.gl.GetMergeRequestModifiedFiles(mr.GetInternalID(), t.cfg.GetProjectNameWithNamespace())
	if err != nil {
		return nil, t.handleError(err, "failed to get a list of modified files")
	}
	log.Debug().Strs("modifiedFiles", mrModifiedFiles).Msg("modified files")
	return t.getTriggeredWorkspaces(mrModifiedFiles)

}

// cloneGitRepo will clone the git repo for a specific MR and returns the temp path to be cleaned up later
func (t *TFCTrigger) cloneGitRepo(mr vcs.MR) (vcs.GitRepo, error) {
	safeProj := strings.ReplaceAll(t.cfg.GetProjectNameWithNamespace(), "/", "-")
	cloneDir, err := ioutil.TempDir("", fmt.Sprintf("%s-%d-*", safeProj, t.cfg.GetMergeRequestIID()))
	if err != nil {
		return nil, t.handleError(err, "could not create tmp directory")
	}
	repo, err := t.gl.CloneMergeRequest(t.cfg.GetProjectNameWithNamespace(), mr, cloneDir)
	if err != nil {
		return nil, t.handleError(err, "could not clone repo")
	}
	return repo, nil
}
func (t *TFCTrigger) TriggerTFCEvents() (*TriggeredTFCWorkspaces, error) {
	mr, err := t.gl.GetMergeRequest(t.cfg.GetMergeRequestIID(), t.cfg.GetProjectNameWithNamespace())
	if err != nil {
		return nil, t.handleError(err, "could not read MergeRequest data from Gitlab API")
	}
	triggeredWorkspaces, err := t.getTriggeredWorkspacesForRequest(mr)
	if err != nil {
		return nil, t.handleError(err, "could not read triggered workspaces")
	}
	workspaceStatus := &TriggeredTFCWorkspaces{
		Errored:  make([]*ErroredWorkspace, 0),
		Executed: make([]string, 0),
	}
	if len(triggeredWorkspaces) > 0 {

		repo, err := t.cloneGitRepo(mr)
		if err != nil {
			return nil, t.handleError(err, "could not clone repo")
		}
		defer os.Remove(repo.GetLocalDirectory())

		modifiedWSMap, err := t.getModifiedWorkspaceBetweenMergeBaseTargetBranch(mr, repo)
		if err != nil {
			// this could just log and continue since the function will always return a valid lookup map
			return nil, t.handleError(err, "could not identify modified workspaces on target branch")
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
					Error: "could not trigger Run for Workspace",
				})
				continue
			}
			workspaceStatus.Executed = append(workspaceStatus.Executed, cfgWS.Name)
		}

	} else if t.cfg.GetTriggerSource() == CommentTrigger {
		return nil, t.handleError(ErrNoChangesDetected, "")

	} else {
		log.Debug().Msg("No Terraform changes found in changeset.")
		return nil, nil
	}

	return workspaceStatus, nil
}

func (t *TFCTrigger) TriggerCleanupEvent() error {
	mr, err := t.gl.GetMergeRequest(t.cfg.GetMergeRequestIID(), t.cfg.GetProjectNameWithNamespace())
	if err != nil {
		return t.handleError(err, "could not read MergeRequest data from Gitlab API")
	}
	var wsNames []string
	cfg, err := getProjectConfigFile(t.gl, t)
	if err != nil {
		return t.handleError(err, "ignoring cleanup trigger for project, missing .tfbuddy.yaml")
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
		}
		// record workspace even if there are not tags since we could have cleared them earlier (same event can be called multiple times)
		wsNames = append(wsNames, cfgWS.Name)
	}
	_, err = t.gl.CreateMergeRequestDiscussion(mr.GetInternalID(),
		t.cfg.GetProjectNameWithNamespace(),
		fmt.Sprintf("Released locks for workspaces: %s", strings.Join(wsNames, ",")),
	)
	if err != nil {
		return t.handleError(err, "could not create MR discussion thread for TFC run status updates")
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
		return t.handleError(err, "could not get Workspace from TFC API")
	}

	// Check if workspace allows API driven runs
	if ws.VCSRepo != nil && t.cfg.GetAction() == ApplyAction {
		return t.handleError(
			fmt.Errorf("cannot trigger apply for VCS workspace"),
			"TFC workspace is configured with a VCS backend, must merge to trigger an Apply.",
		)
	}

	// if the run is a lock or unlock call that function and return.
	// the context in the repo isn't necessary
	if t.cfg.GetAction() == LockAction || t.cfg.GetAction() == UnlockAction {
		err = t.LockUnlockWorkspace(ws, mr, t.cfg.GetAction() == LockAction)
		if err != nil {
			return t.handleError(err, "Error modifying the TFC lock on the workspace")
		}
		_, err := t.gl.CreateMergeRequestDiscussion(mr.GetInternalID(),
			t.cfg.GetProjectNameWithNamespace(),
			fmt.Sprintf("Successfully %sed Workspace `%s/%s`", t.cfg.GetAction(), org, wsName),
		)
		if err != nil {
			return t.handleError(err, "Error posting successful lock modification status")
		}
		return nil
	}

	pkgDir := filepath.Join(cloneDir, cfgWS.Dir)
	if ws.WorkingDirectory != "" {
		// The TFC workspace is configured with a working directory, so we need to send it the whole repo.
		pkgDir = cloneDir
	}

	isApply := false
	if t.cfg.GetAction() == ApplyAction {
		isApply = true
	} else if t.cfg.GetAction() != PlanAction {
		return t.handleError(nil, "Run action was not apply or plan")
	}
	// If the workspace is locked tell the user and don't queue a run
	// Otherwise, TFC wil queue an apply, which might put them out of order
	if isApply {
		lockingMR := t.getLockingMR(ws.ID)
		if ws.Locked {
			return t.handleError(nil, "Refusing to Apply changes to a locked workspace")
		} else if lockingMR != "" {
			return t.handleError(nil, fmt.Sprintf("Workspace is locked by another MR! %s", lockingMR))
		} else {
			err = t.tfc.AddTags(context.Background(),
				ws.ID,
				tfPrefix,
				fmt.Sprintf("%d", t.cfg.GetMergeRequestIID()),
			)
			if err != nil {
				return t.handleError(err, "Error adding tags to workspace")
			}
		}
	}
	// create a new Merge Request discussion thread where status updates will be nested
	disc, err := t.gl.CreateMergeRequestDiscussion(mr.GetInternalID(),
		t.cfg.GetProjectNameWithNamespace(),
		fmt.Sprintf("Starting TFC %v for Workspace: `%s/%s`.", t.cfg.GetAction(), org, wsName),
	)
	if err != nil {
		return t.handleError(err, "could not create MR discussion thread for TFC run status updates")
	}
	t.GetConfig().SetMergeRequestDiscussionID(disc.GetDiscussionID())
	if len(disc.GetMRNotes()) > 0 {
		t.GetConfig().SetMergeRequestRootNoteID(disc.GetMRNotes()[0].GetNoteID())
	} else {
		log.Debug().Msg("No MR Notes found")
	}

	// create new TFC run
	run, err := t.tfc.CreateRunFromSource(&tfc_api.ApiRunOptions{
		IsApply:      isApply,
		Path:         pkgDir,
		Message:      fmt.Sprintf("MR [!%d]: %s", t.cfg.GetMergeRequestIID(), mr.GetTitle()),
		Organization: org,
		Workspace:    wsName,
	})
	if err != nil {
		return t.handleError(err, "could not create TFC run")
	}

	tfcRunsStarted.WithLabelValues(org, wsName, t.cfg.GetAction().String()).Inc()
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
		Action:                               t.cfg.GetAction().String(),
		CommitSHA:                            t.cfg.GetCommitSHA(),
		MergeRequestProjectNameWithNamespace: t.cfg.GetProjectNameWithNamespace(),
		MergeRequestIID:                      t.cfg.GetMergeRequestIID(),
		DiscussionID:                         t.cfg.GetMergeRequestDiscussionID(),
		RootNoteID:                           t.cfg.GetMergeRequestRootNoteID(),
		VcsProvider:                          t.cfg.GetVcsProvider(),
	}
	err := t.runstream.AddRunMeta(rmd)
	if err != nil {
		return t.handleError(err, "Could not publish Run metadata to event stream, updates may not be posted to MR")
	}

	if run.ConfigurationVersion.Speculative {
		// TFC doesn't send Notification webhooks for speculative plans, so we need to poll for updates.
		task := t.runstream.NewTFRunPollingTask(rmd, 1*time.Second)
		err := task.Schedule()

		if err != nil {
			return t.handleError(err, "Failed to create TFC plan polling task. Updates may not be posted to MR")
		}

	}

	return nil
}
