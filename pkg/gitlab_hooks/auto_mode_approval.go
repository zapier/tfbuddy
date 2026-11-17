package gitlab_hooks

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/utils"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.opentelemetry.io/otel"
)

// autoModeApprovalAction is the merge request webhook action GitLab sends once
// every approval rule is satisfied. GitLab also sends "approval" for each
// single approval; TFBuddy ignores that one, because the last approval sends
// both and the apply must start only once.
const autoModeApprovalAction = "approved"

// autoModeApplyBlockedMessage is posted on an Auto Mode MR whose approval did not
// start an apply. Its argument is the reason.
const autoModeApplyBlockedMessage = ":no_entry: Auto Mode did not apply this MR on approval: %s. Nothing was applied. Once that is resolved, approve the MR again or comment `tfc apply`."

// processAutoModeApproval starts the `tfc apply` path for an Auto Mode merge request
// once it is approved. Every other merge request, and every Auto Mode merge
// request while Auto Mode is off, is left to the normal flow without a comment.
func (w *GitlabEventWorker) processAutoModeApproval(ctx context.Context, msg *MergeRequestEventMsg) error {
	ctx, span := otel.Tracer("GitlabHandler").Start(ctx, "processAutoModeApproval")
	defer span.End()

	event := msg.Payload
	proj := event.Project.PathWithNamespace
	mrIID := event.ObjectAttributes.IID
	headSHA := event.ObjectAttributes.LastCommit.ID
	if !w.cfg.AutoModeEnabled {
		log.Debug().Str("project", proj).Int("mergeRequestID", mrIID).Msg("Auto Mode is disabled, ignoring approval")
		return nil
	}

	// The author comes from the API: the event's user is whoever approved.
	mr, err := w.gl.GetMergeRequest(ctx, mrIID, proj)
	if err != nil {
		return fmt.Errorf("could not read MergeRequest data from VCS API: %w", err)
	}
	// A delayed event can name a commit the MR has since moved past. Its
	// statuses and pipeline are not the head's, and the head's own approval
	// sends its own event, so this one starts nothing and says nothing.
	if head := mr.GetHeadSHA(); !strings.EqualFold(head, headSHA) {
		log.Info().Str("project", proj).Int("mergeRequestID", mrIID).Str("commitSHA", headSHA).Str("headSHA", head).
			Msg("ignoring an approval of a commit that is no longer the MR's head")
		return nil
	}
	class, autoMode, err := tfc_trigger.AutoModeClassForMR(ctx, w.cfg, w.gl, w.runstream, w.replanner, proj, mr)
	if err != nil {
		return err
	}
	if class == tfc_trigger.ClassNone {
		// A person's MR, or one that fits no class: a Manual Mode MR, which a
		// person applies.
		return nil
	}

	if reason := w.autoModeApplyBlocker(ctx, mr, proj, headSHA, class, autoMode.RequiredJobs); reason != "" {
		log.Info().Str("project", proj).Int("mergeRequestID", mrIID).Str("reason", reason).
			Msg("Auto Mode approval did not start an apply")
		w.postAutoModeMessage(ctx, proj, mrIID, fmt.Sprintf(autoModeApplyBlockedMessage, reason))
		return nil
	}

	cfg, err := tfc_trigger.NewTFCTriggerConfig(&tfc_trigger.TFCTriggerOptions{
		Action:                   tfc_trigger.ApplyAction,
		Branch:                   mr.GetSourceBranch(),
		CommitSHA:                headSHA,
		ProjectNameWithNamespace: proj,
		MergeRequestIID:          mrIID,
		TriggerSource:            tfc_trigger.MergeRequestEventTrigger,
		VcsProvider:              "gitlab",
		// The delivery ID is the auto-merge generation of this apply.
		DeliveryID: msg.DeliveryID,
		// The apply decides the mode again and runs only in Auto Mode.
		AutoModeApproval: true,
	})
	if err != nil {
		return err
	}
	trigger := w.newTrigger(cfg)
	log.Info().Str("project", proj).Int("mergeRequestID", mrIID).Str("commitSHA", headSHA).Str("class", class).
		Msg("Auto Mode approval: starting apply")
	executed, err := trigger.TriggerTFCEvents(ctx)
	if err != nil {
		// Permanent, so a redelivery cannot post the same comment again; a
		// retry is a `tfc apply` comment, as for a failed apply.
		w.postAutoModeMessage(ctx, proj, mrIID, fmt.Sprintf(":no_entry: Auto Mode apply could not be run because: %s", err.Error()))
		return utils.CreatePermanentError(err)
	}
	if executed != nil {
		for _, ws := range executed.Errored {
			w.postAutoModeMessage(ctx, proj, mrIID, fmt.Sprintf(":no_entry: %s could not be run because: %s", ws.Name, ws.Error))
		}
	}
	return nil
}

// autoModeTakeoverReason is the Manual Mode reason of an Auto Mode MR that a
// person took over with `tfc apply`. Its argument is why the Auto Mode gate
// refused the apply.
const autoModeTakeoverReason = "a person commented `tfc apply` while the Auto Mode gate did not pass (%s)"

// takeOverAutoModeMR decides how a `tfc apply` comment on an Auto Mode MR runs.
// When the Auto Mode gate passes, the apply runs in Auto Mode, like one an
// approval starts. When it does not, the person who commented takes the MR
// over: it falls back to Manual Mode until it merges, so the apply that
// follows is a Manual Mode apply, without the class marker, and like any
// fallen-back MR's it needs a person's approval. A person can always fix and
// apply an MR that automation could not.
//
// It returns an error when the MR's mode cannot be decided or the fallback
// cannot be recorded: the apply must then not run, since it would run in
// Auto Mode past a gate that refused it.
func (w *GitlabEventWorker) takeOverAutoModeMR(ctx context.Context, proj string, mrIID int) error {
	if !w.cfg.AutoModeEnabled {
		return nil
	}
	ctx, span := otel.Tracer("GitlabHandler").Start(ctx, "takeOverAutoModeMR")
	defer span.End()

	mr, err := w.gl.GetMergeRequest(ctx, mrIID, proj)
	if err != nil {
		return fmt.Errorf("could not read MergeRequest data from VCS API: %w", err)
	}
	class, autoMode, err := tfc_trigger.AutoModeClassForMR(ctx, w.cfg, w.gl, w.runstream, w.replanner, proj, mr)
	if err != nil {
		return fmt.Errorf("could not decide whether this MR runs in Auto Mode: %w", err)
	}
	if class == tfc_trigger.ClassNone {
		// A person's MR, or one already in Manual Mode.
		return nil
	}
	reason := w.autoModeApplyBlocker(ctx, mr, proj, mr.GetHeadSHA(), class, autoMode.RequiredJobs)
	if reason == "" {
		return nil
	}
	log.Info().Str("project", proj).Int("mergeRequestID", mrIID).Str("class", class).Str("reason", reason).
		Msg("a person took over an Auto Mode MR with `tfc apply`")
	return tfc_trigger.FallBackToManualMode(ctx, tfc_trigger.AutoModeDeps{
		AppCfg: w.cfg, GL: w.gl, TFC: w.tfc, RS: w.runstream, Replanner: w.replanner,
	}, tfc_trigger.ManualModeFallback{
		Project:         proj,
		MergeRequestIID: mrIID,
		CommitSHA:       mr.GetHeadSHA(),
		Class:           class,
		Reason:          fmt.Sprintf(autoModeTakeoverReason, reason),
		// The apply that follows plans without the class.
		Replan: false,
	})
}

// autoModeApplyBlocker returns why an approved Auto Mode merge request must not
// apply yet, or "" when it may. class is the change class it would apply with,
// and requiredJobs are the CI jobs that must have succeeded. Lock and
// divergence checks are not here: the apply path itself runs them, failing
// closed for Auto Mode.
func (w *GitlabEventWorker) autoModeApplyBlocker(ctx context.Context, mr vcs.DetailedMR, proj, headSHA, class string, requiredJobs []string) string {
	switch {
	case mr.GetState() != "opened":
		return "the MR is not open"
	case mr.IsDraft():
		return "the MR is a Draft"
	case mr.HasConflicts():
		return "the MR has conflicts"
	}

	mrIID := mr.GetInternalID()
	// The repo resets approvals on push, so an approval always covers the head
	// commit.
	approvals, err := w.gl.GetMergeRequestApprovals(ctx, mrIID, proj)
	if err != nil {
		return fmt.Sprintf("could not read the MR's approvals: %v", err)
	}
	if !approvals.IsApproved() {
		return "the MR is not approved"
	}

	statuses, err := w.gl.GetCommitJobStatuses(ctx, proj, headSHA)
	if err != nil {
		return fmt.Sprintf("could not read the head commit's statuses: %v", err)
	}
	// Anyone with Developer access can post a status with any name, so only
	// the ones TFBuddy's own account posted count.
	tfbuddy, err := w.gl.GetAuthenticatedUsername(ctx)
	if err != nil {
		return fmt.Sprintf("could not read TFBuddy's GitLab account: %v", err)
	}
	if reason := planStatusBlocker(statuses, tfbuddy); reason != "" {
		return reason
	}
	if reason := w.classPlansBlocker(proj, mrIID, headSHA, class); reason != "" {
		return reason
	}

	// Unlike `tfc apply`, Auto Mode always needs a green pipeline: the review
	// jobs run in it, and no person is checking it instead.
	pipeline, err := w.autoModePipelineForCommit(ctx, proj, mr, headSHA)
	if err != nil {
		return fmt.Sprintf("could not read the head commit's pipelines: %v", err)
	}
	if pipeline == nil {
		return "the head commit has no CI pipeline of this MR or its source branch"
	}
	if blocking := blockingJobs(statuses, pipeline.GetID()); len(blocking) > 0 {
		return fmt.Sprintf("%s has not succeeded, still waiting on: %s", vcs.DescribePipeline(pipeline), strings.Join(blocking, ", "))
	}
	return w.requiredJobsBlocker(ctx, proj, pipeline, requiredJobs)
}

// autoModePipelineForCommit returns the CI pipeline of headSHA that the Auto
// Mode gate judges, or nil when there is none: the newest one GitLab ran for
// this MR's own changes, a merge request pipeline of this MR or, without one,
// a push pipeline of its source branch. No other pipeline of the commit
// counts. One a person starts by hand, through the API, a trigger or a
// schedule can carry variables that person set, and the pipeline of another
// MR or branch with the same commit, which anyone with Developer access can
// open, reviews other changes.
func (w *GitlabEventWorker) autoModePipelineForCommit(ctx context.Context, proj string, mr vcs.DetailedMR, headSHA string) (vcs.ProjectPipeline, error) {
	pipelines, err := w.gl.GetPipelinesForCommit(ctx, proj, headSHA)
	if err != nil {
		return nil, err
	}
	mrRefs := fmt.Sprintf("refs/merge-requests/%d/", mr.GetInternalID())
	candidates := make([]vcs.ProjectPipeline, 0, len(pipelines))
	for _, p := range pipelines {
		switch {
		case p.GetSource() == vcs.PipelineSourceMergeRequestEvent && strings.HasPrefix(p.GetRef(), mrRefs):
		case p.GetSource() == vcs.PipelineSourcePush && p.GetRef() == mr.GetSourceBranch():
		default:
			continue
		}
		candidates = append(candidates, p)
	}
	return vcs.SelectPipelineForCommit(candidates), nil
}

// requiredJobsBlocker returns why the required CI jobs of pipeline do not
// allow an Auto Mode apply, or "". The pipeline gate above lets manual,
// skipped and allowed-to-fail jobs through, so a review job that did not run
// would pass it; a required job must be in the pipeline, and its latest run
// must have succeeded. The jobs come from the pipeline jobs API, which lists
// only jobs the pipeline ran, so a commit status with a job's name, which
// anyone with Developer access can post, cannot stand in for one.
func (w *GitlabEventWorker) requiredJobsBlocker(ctx context.Context, proj string, pipeline vcs.ProjectPipeline, requiredJobs []string) string {
	if len(requiredJobs) == 0 {
		return ""
	}
	jobs, err := w.gl.GetPipelineJobs(ctx, proj, pipeline.GetID())
	if err != nil {
		return fmt.Sprintf("could not list the jobs of %s: %v", vcs.DescribePipeline(pipeline), err)
	}
	var missing, notSucceeded []string
	for _, name := range requiredJobs {
		found := false
		for _, job := range jobs {
			if job.GetName() != name {
				continue
			}
			found = true
			if job.GetStatus() != "success" {
				notSucceeded = append(notSucceeded, fmt.Sprintf("%s (%s)", name, job.GetStatus()))
			}
		}
		if !found {
			missing = append(missing, name)
		}
	}
	switch {
	case len(missing) > 0:
		return fmt.Sprintf("%s has no required job %s", vcs.DescribePipeline(pipeline), strings.Join(missing, ", "))
	case len(notSucceeded) > 0:
		return fmt.Sprintf("a required job in %s has not succeeded: %s", vcs.DescribePipeline(pipeline), strings.Join(notSucceeded, ", "))
	}
	return ""
}

// classPlansBlocker returns why the recorded class plans of the head commit do
// not allow an Auto Mode apply with class, or "". TFC/class on GitLab is only
// what TFBuddy last wrote there: a write that failed, or a late one from plans
// that were begun again since, can leave it green. The recorded plans are the
// result it reports, so the gate reads them too.
func (w *GitlabEventWorker) classPlansBlocker(proj string, mrIID int, headSHA, class string) string {
	state, err := w.runstream.GetClassPlans(tfc_trigger.ClassPlanRef(proj, mrIID, headSHA))
	switch {
	case errors.Is(err, runstream.ErrClassPlansNotFound):
		return "the head commit has no recorded class plans"
	case err != nil:
		return fmt.Sprintf("could not read the head commit's class plans: %v", err)
	case state.Class != class:
		return fmt.Sprintf("the head commit was planned with class %s, not %s", state.Class, class)
	case !state.Finished:
		return "the head commit's class plans have not all finished"
	case !state.Passed():
		return fmt.Sprintf("the class plan for %s did not pass", strings.Join(state.FailedWorkspaces(), ", "))
	}
	return ""
}

// planStatusBlocker returns why TFBuddy's statuses on the head commit do not
// allow an Auto Mode apply, or "". Every TFC/plan/<workspace> status must be
// green, and TFC/class must have passed. Only statuses that tfbuddy, TFBuddy's
// own GitLab account, posted count: a TFC/* status anyone else posts is
// ignored, and a name that only someone else posted blocks the apply.
func planStatusBlocker(statuses []vcs.CommitJobStatus, tfbuddy string) string {
	if tfbuddy == "" {
		return "TFBuddy's GitLab account is unknown, so its statuses cannot be told apart from anyone else's"
	}
	planPrefix := vcs.CommitStatusPrefix + "plan/"
	var plans int
	var notGreen []string
	class := ""
	ours := map[string]bool{}
	var foreign []string
	for _, s := range statuses {
		name := s.GetName()
		if name != vcs.ClassStatusName && !strings.HasPrefix(name, planPrefix) {
			continue
		}
		if !strings.EqualFold(s.GetAuthor(), tfbuddy) {
			foreign = append(foreign, name)
			continue
		}
		ours[name] = true
		if name == vcs.ClassStatusName {
			class = s.GetStatus()
			continue
		}
		plans++
		if s.GetStatus() != "success" {
			notGreen = append(notGreen, fmt.Sprintf("%s (%s)", name, s.GetStatus()))
		}
	}
	var foreignOnly []string
	for _, name := range foreign {
		if !ours[name] && !slices.Contains(foreignOnly, name) {
			foreignOnly = append(foreignOnly, name)
		}
	}
	slices.Sort(foreignOnly)
	switch {
	case len(foreignOnly) > 0:
		return fmt.Sprintf("%s on the head commit was not posted by TFBuddy", strings.Join(foreignOnly, ", "))
	case plans == 0:
		return "the head commit has no plans"
	case len(notGreen) > 0:
		return "not every plan for the head commit is green: " + strings.Join(notGreen, ", ")
	case class == "":
		return fmt.Sprintf("%s is not set on the head commit", vcs.ClassStatusName)
	case class != "success":
		return fmt.Sprintf("%s is %s", vcs.ClassStatusName, class)
	}
	return ""
}

func (w *GitlabEventWorker) postAutoModeMessage(ctx context.Context, proj string, mrIID int, msg string) {
	if err := w.gl.CreateMergeRequestComment(ctx, mrIID, proj, msg); err != nil {
		log.Error().Err(err).Str("project", proj).Int("mergeRequestID", mrIID).Msg("could not post Auto Mode message to MR")
	}
}
