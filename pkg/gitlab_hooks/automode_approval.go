package gitlab_hooks

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/utils"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.opentelemetry.io/otel"
)

// automodeApprovalAction is the merge request webhook action GitLab sends once
// every approval rule is satisfied. GitLab also sends "approval" for each
// single approval; TFBuddy ignores that one, because the last approval sends
// both and the apply must start only once.
const automodeApprovalAction = "approved"

// automodeApplyBlockedMessage is posted on an automode MR whose approval did not
// start an apply. Its argument is the reason.
const automodeApplyBlockedMessage = ":no_entry: Automode did not apply this MR on approval: %s. Nothing was applied. Once that is resolved, approve the MR again or comment `tfc apply`."

// processAutomodeApproval starts the `tfc apply` path for an automode merge request
// once it is approved. Every other merge request, and every automode merge
// request while automode is off, is left to the normal flow without a comment.
func (w *GitlabEventWorker) processAutomodeApproval(ctx context.Context, msg *MergeRequestEventMsg) error {
	ctx, span := otel.Tracer("GitlabHandler").Start(ctx, "processAutomodeApproval")
	defer span.End()

	event := msg.Payload
	proj := event.Project.PathWithNamespace
	mrIID := event.ObjectAttributes.IID
	headSHA := event.ObjectAttributes.LastCommit.ID
	if !w.cfg.AutomodeEnabled {
		log.Debug().Str("project", proj).Int("mergeRequestID", mrIID).Msg("automode is disabled, ignoring approval")
		return nil
	}

	// The author comes from the API: the event's user is whoever approved.
	mr, err := w.gl.GetMergeRequest(ctx, mrIID, proj)
	if err != nil {
		return fmt.Errorf("could not read MergeRequest data from VCS API: %w", err)
	}
	automode, err := tfc_trigger.LoadAutomodeConfig(ctx, w.gl, proj, mr.GetTargetBranch())
	if err != nil {
		return err
	}
	if automode.Author(mr.GetAuthor().GetUsername()) == nil {
		return nil
	}

	if reason := w.automodeApplyBlocker(ctx, mr, proj, headSHA); reason != "" {
		log.Info().Str("project", proj).Int("mergeRequestID", mrIID).Str("reason", reason).
			Msg("automode approval did not start an apply")
		w.postAutomodeMessage(ctx, proj, mrIID, fmt.Sprintf(automodeApplyBlockedMessage, reason))
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
	})
	if err != nil {
		return err
	}
	trigger := w.triggerCreation(w.cfg, w.gl, w.tfc, w.runstream, cfg)
	if w.workspaceStream != nil {
		trigger.SetWorkspaceStream(w.workspaceStream)
	}
	log.Info().Str("project", proj).Int("mergeRequestID", mrIID).Str("commitSHA", headSHA).
		Msg("automode approval: starting apply")
	executed, err := trigger.TriggerTFCEvents(ctx)
	if err != nil {
		// Permanent, so a redelivery cannot post the same comment again; a
		// retry is a `tfc apply` comment, as for a failed apply.
		w.postAutomodeMessage(ctx, proj, mrIID, fmt.Sprintf(":no_entry: Automode apply could not be run because: %s", err.Error()))
		return utils.CreatePermanentError(err)
	}
	if executed != nil {
		for _, ws := range executed.Errored {
			w.postAutomodeMessage(ctx, proj, mrIID, fmt.Sprintf(":no_entry: %s could not be run because: %s", ws.Name, ws.Error))
		}
	}
	return nil
}

// automodeApplyBlocker returns why an approved automode merge request must not
// apply yet, or "" when it may. Lock and divergence checks are not here: the
// apply path itself runs them, failing closed for automode.
func (w *GitlabEventWorker) automodeApplyBlocker(ctx context.Context, mr vcs.DetailedMR, proj, headSHA string) string {
	switch {
	case mr.GetState() != "opened":
		return "the MR is not open"
	case mr.IsDraft():
		return "the MR is a Draft"
	case mr.HasConflicts():
		return "the MR has conflicts"
	}

	mrIID := mr.GetInternalID()
	approvals, err := w.gl.GetMergeRequestApprovalState(ctx, mrIID, proj)
	if err != nil {
		return fmt.Sprintf("could not read the MR's approvals: %v", err)
	}
	versions, err := w.gl.GetMergeRequestVersions(ctx, mrIID, proj)
	if err != nil {
		return fmt.Sprintf("could not read the MR's pushes: %v", err)
	}
	if reason := approvalAfterPushBlocker(approvals, versions, headSHA); reason != "" {
		return reason
	}

	statuses, err := w.gl.GetCommitJobStatuses(ctx, proj, headSHA)
	if err != nil {
		return fmt.Sprintf("could not read the head commit's statuses: %v", err)
	}
	if reason := planStatusBlocker(statuses); reason != "" {
		return reason
	}

	// Unlike `tfc apply`, automode always needs a green pipeline: the Async Agent
	// review runs in it, and no person is checking it instead.
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
	return ""
}

// approvalAfterPushBlocker returns why approvals do not cover headSHA, or "".
// The newest diff version must be headSHA, and every approval must be newer
// than the push that created it, because approvals survive pushes. An
// approval GitLab gives no time for counts as older.
func approvalAfterPushBlocker(approvals *vcs.MRApprovalState, versions []vcs.MRVersion, headSHA string) string {
	if approvals == nil || !approvals.Approved || len(approvals.ApprovedBy) == 0 {
		return "the MR is not approved"
	}
	var pushed time.Time
	var newestHead string
	for _, v := range versions {
		if v.CreatedAt.After(pushed) {
			pushed, newestHead = v.CreatedAt, v.HeadSHA
		}
	}
	if newestHead != headSHA {
		return "the MR has a newer push than the approved commit"
	}
	var stale []string
	for _, a := range approvals.ApprovedBy {
		if a.ApprovedAt == nil || !a.ApprovedAt.After(pushed) {
			stale = append(stale, a.Username)
		}
	}
	if len(stale) > 0 {
		return fmt.Sprintf("the approval by %s was given before the head commit was pushed", strings.Join(stale, ", "))
	}
	return ""
}

// planStatusBlocker returns why TFBuddy's statuses on the head commit do not
// allow an automode apply, or "". Every TFC/plan/<workspace> status must be
// green, and TFC/class must have passed.
func planStatusBlocker(statuses []vcs.CommitJobStatus) string {
	planPrefix := vcs.CommitStatusPrefix + "plan/"
	var plans int
	var notGreen []string
	class := ""
	for _, s := range statuses {
		switch {
		case s.GetName() == vcs.ClassStatusName:
			class = s.GetStatus()
		case strings.HasPrefix(s.GetName(), planPrefix):
			plans++
			if s.GetStatus() != "success" {
				notGreen = append(notGreen, fmt.Sprintf("%s (%s)", s.GetName(), s.GetStatus()))
			}
		}
	}
	switch {
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

func (w *GitlabEventWorker) postAutomodeMessage(ctx context.Context, proj string, mrIID int, msg string) {
	if err := w.gl.CreateMergeRequestComment(ctx, mrIID, proj, msg); err != nil {
		log.Error().Err(err).Str("project", proj).Int("mergeRequestID", mrIID).Msg("could not post automode message to MR")
	}
}
