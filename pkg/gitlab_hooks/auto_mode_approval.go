package gitlab_hooks

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/rs/zerolog/log"
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

	if reason := w.autoModeApplyBlocker(ctx, mr, proj, headSHA, autoMode.RequiredJobs); reason != "" {
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

// autoModeApplyBlocker returns why an approved Auto Mode merge request must not
// apply yet, or "" when it may. requiredJobs are the CI jobs that must have
// succeeded. Lock and divergence checks are not here: the apply path itself
// runs them, failing closed for Auto Mode.
func (w *GitlabEventWorker) autoModeApplyBlocker(ctx context.Context, mr vcs.DetailedMR, proj, headSHA string, requiredJobs []string) string {
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

	// Unlike `tfc apply`, Auto Mode always needs a green pipeline: the review
	// jobs run in it, and no person is checking it instead.
	pipeline, err := w.ciPipelineForCommit(ctx, proj, headSHA)
	if err != nil {
		return fmt.Sprintf("could not read the head commit's pipelines: %v", err)
	}
	if pipeline == nil {
		return "the head commit has no CI pipeline"
	}
	if blocking := blockingJobs(statuses, pipeline.GetID()); len(blocking) > 0 {
		return fmt.Sprintf("%s has not succeeded, still waiting on: %s", vcs.DescribePipeline(pipeline), strings.Join(blocking, ", "))
	}
	return w.requiredJobsBlocker(ctx, proj, pipeline, requiredJobs)
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
