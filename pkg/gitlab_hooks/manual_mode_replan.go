package gitlab_hooks

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
	"go.opentelemetry.io/otel"
)

// manualModeReplanAction is the action of the merge request event TFBuddy
// publishes itself to plan an MR that fell back to Manual Mode again. GitLab
// never sends it. The event goes through the hooks stream like a push, so the
// plan runs in the MR event worker, not in the run-event or webhook callback
// that asked for it, which acks at once.
const manualModeReplanAction = "tfbuddy_manual_mode_replan"

// ensure the handler can ask for Manual Mode re-plans
var _ tfc_trigger.ManualModeReplanner = (*GitlabHooksHandler)(nil)

// manualModeReplanMsg is the hooks stream message that asks for a Manual Mode
// plan of commitSHA, the head of MR mrIID in project.
func manualModeReplanMsg(project string, mrIID int, commitSHA string) *MergeRequestEventMsg {
	event := &gogitlab.MergeEvent{}
	event.Project.PathWithNamespace = project
	// ID is part of the hooks stream message ID; with the SHA it makes a
	// duplicate request for the same commit a duplicate message.
	event.ObjectAttributes.ID = mrIID
	event.ObjectAttributes.IID = mrIID
	event.ObjectAttributes.Action = manualModeReplanAction
	event.ObjectAttributes.LastCommit.ID = commitSHA
	event.ObjectAttributes.Source = &gogitlab.Repository{PathWithNamespace: project}
	event.ObjectAttributes.Target = &gogitlab.Repository{PathWithNamespace: project}
	return &MergeRequestEventMsg{
		Payload: event,
		// Anchors the workspace dispatch claim: one re-plan per commit.
		DeliveryID: "tfbuddy-manual-mode-replan/" + commitSHA,
	}
}

// RequestManualModeReplan publishes a Manual Mode re-plan of commitSHA to the
// MR event stream and returns.
func (h *GitlabHooksHandler) RequestManualModeReplan(ctx context.Context, project string, mrIID int, commitSHA string) error {
	if h.mrStream == nil {
		return fmt.Errorf("the merge request event stream is not configured")
	}
	if _, err := h.mrStream.Publish(ctx, manualModeReplanMsg(project, mrIID, commitSHA)); err != nil {
		return fmt.Errorf("could not publish the Manual Mode re-plan: %w", err)
	}
	return nil
}

// processManualModeReplan plans the head commit of an MR that fell back to
// Manual Mode. The MR is already marked as Manual Mode, so its plans carry no
// class marker. It does nothing when the commit is no longer the MR's head:
// the push that moved it plans the new head, in Manual Mode too.
func (w *GitlabEventWorker) processManualModeReplan(ctx context.Context, msg *MergeRequestEventMsg) error {
	ctx, span := otel.Tracer("GitlabHandler").Start(ctx, "processManualModeReplan")
	defer span.End()

	proj := msg.Payload.Project.PathWithNamespace
	mrIID := msg.Payload.ObjectAttributes.IID
	sha := msg.Payload.ObjectAttributes.LastCommit.ID
	logger := log.With().Str("project", proj).Int("mergeRequestID", mrIID).Str("commitSHA", sha).Logger()

	mr, err := w.gl.GetMergeRequest(ctx, mrIID, proj)
	if err != nil {
		return fmt.Errorf("could not read MergeRequest data from VCS API: %w", err)
	}
	if mr.GetState() != "opened" {
		logger.Info().Str("state", mr.GetState()).Msg("not planning a merge request that is not open again")
		return nil
	}
	if head := mr.GetHeadSHA(); !strings.EqualFold(head, sha) {
		logger.Info().Str("headSHA", head).Msg("not planning an old commit again; the new head is planned by its own push")
		return nil
	}
	cfg, err := tfc_trigger.NewTFCTriggerConfig(&tfc_trigger.TFCTriggerOptions{
		Action:                   tfc_trigger.PlanAction,
		Branch:                   mr.GetSourceBranch(),
		CommitSHA:                sha,
		ProjectNameWithNamespace: proj,
		MergeRequestIID:          mrIID,
		TriggerSource:            tfc_trigger.MergeRequestEventTrigger,
		VcsProvider:              "gitlab",
		DeliveryID:               msg.DeliveryID,
	})
	if err != nil {
		return err
	}
	logger.Info().Msg("planning a merge request that fell back to Manual Mode again")
	executed, err := w.newTrigger(cfg).TriggerTFCEvents(ctx)
	logErroredWorkspaces(proj, mrIID, executed)
	return err
}

// newTrigger builds a trigger for cfg with the worker's Manual Mode
// re-planner.
func (w *GitlabEventWorker) newTrigger(cfg *tfc_trigger.TFCTriggerOptions) tfc_trigger.Trigger {
	create := w.triggerCreation
	if create == nil {
		create = tfc_trigger.NewTFCTrigger
	}
	trigger := create(w.cfg, w.gl, w.tfc, w.runstream, cfg)
	if w.replanner != nil {
		trigger.SetManualModeReplanner(w.replanner)
	}
	return trigger
}
