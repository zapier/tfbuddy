package gitlab_hooks

import (
	"context"
	"fmt"
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
	autoMode, err := tfc_trigger.LoadAutoModeConfig(ctx, w.gl, proj, mr.GetTargetBranch())
	if err != nil {
		return err
	}
	author := autoMode.Author(mr.GetAuthor().GetUsername())
	if author == nil {
		return nil
	}
	files, err := w.gl.GetMergeRequestModifiedFiles(ctx, mrIID, proj)
	if err != nil {
		return fmt.Errorf("could not list the MR's changed files: %w", err)
	}
	commits, err := w.gl.GetMergeRequestCommits(ctx, mrIID, proj)
	if err != nil {
		return fmt.Errorf("could not list the MR's commits: %w", err)
	}
	if autoMode.ClassFor(author, files, commits) == tfc_trigger.ClassNone {
		// It fits no class, so it is a Manual Mode MR: a person applies it.
		return nil
	}

	if reason := w.autoModeApplyBlocker(ctx, mr, proj, headSHA); reason != "" {
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
	})
	if err != nil {
		return err
	}
	trigger := w.triggerCreation(w.cfg, w.gl, w.tfc, w.runstream, cfg)
	if w.workspaceStream != nil {
		trigger.SetWorkspaceStream(w.workspaceStream)
	}
	log.Info().Str("project", proj).Int("mergeRequestID", mrIID).Str("commitSHA", headSHA).
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
// apply yet, or "" when it may. Lock and divergence checks are not here: the
// apply path itself runs them, failing closed for Auto Mode.
func (w *GitlabEventWorker) autoModeApplyBlocker(ctx context.Context, mr vcs.DetailedMR, proj, headSHA string) string {
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
	if reason := planStatusBlocker(statuses); reason != "" {
		return reason
	}

	// Unlike `tfc apply`, Auto Mode always needs a green pipeline: the Async Agent
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

// planStatusBlocker returns why TFBuddy's statuses on the head commit do not
// allow an Auto Mode apply, or "". Every TFC/plan/<workspace> status must be
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

func (w *GitlabEventWorker) postAutoModeMessage(ctx context.Context, proj string, mrIID int, msg string) {
	if err := w.gl.CreateMergeRequestComment(ctx, mrIID, proj, msg); err != nil {
		log.Error().Err(err).Str("project", proj).Int("mergeRequestID", mrIID).Msg("could not post Auto Mode message to MR")
	}
}
