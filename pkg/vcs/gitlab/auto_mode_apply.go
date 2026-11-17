package gitlab

import (
	"context"
	"fmt"

	"github.com/hashicorp/go-tfe"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.opentelemetry.io/otel"
)

// discardSoftFailedAutoModeApply handles an Auto Mode apply whose plan
// soft-failed a Sentinel policy. TFBuddy never overrides a policy, so it puts
// the MR in Manual Mode until it merges, discards the run, fails
// TFC/apply/<workspace>, and says so on the MR. A person can then review the
// MR and, in Manual Mode, decide about the policy in TFC.
//
// The fallback is recorded first: once the run is discarded its events are
// stale, so no retry could record it. When it cannot be recorded, the run is
// left waiting for an override, which applies nothing, and the error makes
// the event retry.
func (p *RunStatusUpdater) discardSoftFailedAutoModeApply(ctx context.Context, run *tfe.Run, rmd runstream.RunMetadata) error {
	ctx, span := otel.Tracer("TFC").Start(ctx, "discardSoftFailedAutoModeApply")
	defer span.End()

	ws := rmd.GetWorkspace()
	logger := log.With().Str("project", rmd.GetMRProjectNameWithNamespace()).Int("mergeRequestID", rmd.GetMRInternalID()).
		Str("workspace", ws).Str("runID", rmd.GetRunID()).Str("status", string(run.Status)).Logger()

	if err := p.fallBackToManualMode(ctx, rmd, fmt.Sprintf("its apply of `%s` soft-failed a Sentinel policy, and Auto Mode never overrides a policy", ws)); err != nil {
		span.RecordError(err)
		return fmt.Errorf("not discarding the soft-failed Auto Mode apply until its fallback is recorded: %w", err)
	}

	discardErr := p.tfc.DiscardRun(ctx, rmd.GetRunID(),
		"TFBuddy Auto Mode never overrides a policy. The merge request falls back to Manual Mode.")
	description := "discarded: a policy soft-failed, and Auto Mode never overrides a policy"
	if discardErr != nil {
		span.RecordError(discardErr)
		logger.Error().Err(discardErr).Msg("could not discard a soft-failed Auto Mode apply")
		description = "not applied: a policy soft-failed; discard the run in TFC and do not override it"
	}
	if err := p.client.SetWorkspaceStatus(ctx, vcs.WorkspaceStatus{
		Project:         rmd.GetMRProjectNameWithNamespace(),
		CommitSHA:       rmd.GetCommitSHA(),
		MergeRequestIID: rmd.GetMRInternalID(),
		Workspace:       ws,
		Action:          runstream.ApplyAction,
		State:           vcs.CommitStateFailed,
		Description:     description,
		TargetURL:       *runUrlForTFRunMetadata(rmd),
	}); err != nil {
		logger.Error().Err(err).Msg("could not fail the apply status of a soft-failed Auto Mode apply")
	}

	msg := fmt.Sprintf(":no_entry: **Auto Mode** discarded the apply of `%s` (run `%s`): a Sentinel policy soft-failed, and TFBuddy never overrides a policy. "+
		"Nothing was applied to `%s`, and the MR will not auto-merge. The MR is now in Manual Mode: a person must review and approve it (approvals from automatic approvers and Auto Mode authors do not count), and someone must comment `tfc apply`.",
		ws, rmd.GetRunID(), ws)
	if discardErr != nil {
		msg = fmt.Sprintf(":no_entry: **Auto Mode** did not apply `%s` (run `%s`): a Sentinel policy soft-failed, and TFBuddy never overrides a policy. "+
			"TFBuddy could not discard the run (%v): discard it in Terraform Cloud, and do not override the policy. "+
			"The MR is now in Manual Mode: a person must review and approve it (approvals from automatic approvers and Auto Mode authors do not count), and someone must comment `tfc apply`.",
			ws, rmd.GetRunID(), discardErr)
	}
	if err := p.client.CreateMergeRequestComment(ctx, rmd.GetMRInternalID(), rmd.GetMRProjectNameWithNamespace(), msg); err != nil {
		logger.Error().Err(err).Msg("could not post the discarded Auto Mode apply to the MR")
	}
	return nil
}

// fallBackOnErroredAutoModeApply puts the MR of an Auto Mode apply that
// errored in Manual Mode, whatever the error was: TFBuddy cannot tell a failed
// class policy from any other error, and an MR stuck with its class marker
// would fail the same way on every retry. It returns the error when the
// fallback could not be recorded, so that the final event is handled again.
func (p *RunStatusUpdater) fallBackOnErroredAutoModeApply(ctx context.Context, rmd runstream.RunMetadata) error {
	if rmd.GetChangeClass() == "" || rmd.GetAction() != runstream.ApplyAction {
		return nil
	}
	return p.fallBackToManualMode(ctx, rmd, fmt.Sprintf("its apply of `%s` errored (run `%s`)", rmd.GetWorkspace(), rmd.GetRunID()))
}
