package gitlab

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/go-tfe"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"go.opentelemetry.io/otel"
)

// terminalPlanStatuses are the run states after which a plan run's
// TFC/plan/<workspace> status no longer changes.
var terminalPlanStatuses = map[tfe.RunStatus]bool{
	tfe.RunPlannedAndFinished: true,
	tfe.RunPolicySoftFailed:   true,
	tfe.RunErrored:            true,
	tfe.RunCanceled:           true,
	tfe.RunDiscarded:          true,
}

// updateClassStatus keeps the TFC/class status of an Auto Mode merge request in
// step with its plans. It runs after every run event, and only a plan run with
// a class reaching a final state can change the result.
//
// The class is judged from the plans' own outcomes: a plan that errored, was
// canceled or discarded, or failed any policy fails the class, and the MR
// falls back to Manual Mode. Each outcome is recorded in a compare-and-swap
// aggregate rather than read back from GitLab's commit statuses, so when the
// last plans finish together exactly one of them sees them all and reports
// the result. This proof of concept does not read a Sentinel class-policy
// result, because that policy does not exist yet; once it does, its result is
// what should decide TFC/class.
//
// It returns an error when the outcome, the fallback or the report could not
// be recorded, so that the event is handled again: no later event is sure to
// come for the commit. A finished aggregate whose report did not land is
// reported again by the redelivery.
func (p *RunStatusUpdater) updateClassStatus(ctx context.Context, run *tfe.Run, rmd runstream.RunMetadata) error {
	class := rmd.GetChangeClass()
	if class == "" || rmd.GetAction() != runstream.PlanAction || !terminalPlanStatuses[run.Status] {
		return nil
	}
	ctx, span := otel.Tracer("TFC").Start(ctx, "updateClassStatus")
	defer span.End()

	var errs []error
	passed := run.Status == tfe.RunPlannedAndFinished
	ref := tfc_trigger.ClassPlanRef(rmd.GetMRProjectNameWithNamespace(), rmd.GetMRInternalID(), rmd.GetCommitSHA())
	state, finished, err := p.rs.RecordClassPlanResult(ref, rmd.GetWorkspace(), rmd.GetRunID(), rmd.GetClassTrigger(), passed, string(run.Status))
	switch {
	case errors.Is(err, runstream.ErrClassPlansNotFound):
		// A run with the class marker outside a class plan, such as one from
		// `tfc plan -w`: it does not decide TFC/class, but a failure still
		// puts the MR in Manual Mode below.
		log.Debug().Str("project", rmd.GetMRProjectNameWithNamespace()).Int("mergeRequestID", rmd.GetMRInternalID()).
			Str("runID", rmd.GetRunID()).Msg("plan run is not part of a class plan")
	case err != nil:
		span.RecordError(err)
		log.Error().Err(err).Str("project", rmd.GetMRProjectNameWithNamespace()).Int("mergeRequestID", rmd.GetMRInternalID()).
			Msg("could not record the plan's outcome for the class status")
		errs = append(errs, fmt.Errorf("could not record the plan's outcome for the class status: %w", err))
	}
	if !passed {
		// Any plan with the class marker that does not pass is a failed class
		// check: the MR runs in Manual Mode from now on, and its head is
		// planned again without the class.
		if err := p.fallBackToManualMode(ctx, rmd, fmt.Sprintf("the plan for %s did not pass (%s)", rmd.GetWorkspace(), run.Status)); err != nil {
			errs = append(errs, err)
		}
	}
	// A finished aggregate that is not reported yet is one whose report
	// failed: this event, most likely its redelivery, reports it again.
	if finished || (err == nil && state != nil && state.Finished && !state.Reported) {
		if err := tfc_trigger.ReportClassPlans(ctx, p.client, p.rs, state); err != nil {
			span.RecordError(err)
			log.Error().Err(err).Str("project", rmd.GetMRProjectNameWithNamespace()).Int("mergeRequestID", rmd.GetMRInternalID()).
				Msg("could not report the class plans")
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// fallBackToManualMode puts rmd's MR in Manual Mode until it merges; see
// tfc_trigger.FallBackToManualMode. It logs and returns the error when the
// fallback could not be recorded.
func (p *RunStatusUpdater) fallBackToManualMode(ctx context.Context, rmd runstream.RunMetadata, reason string) error {
	err := tfc_trigger.FallBackToManualMode(ctx, tfc_trigger.AutoModeDeps{
		AppCfg:    p.cfg,
		GL:        p.client,
		TFC:       p.tfc,
		RS:        p.rs,
		Replanner: p.replanner,
	}, tfc_trigger.ManualModeFallback{
		Project:         rmd.GetMRProjectNameWithNamespace(),
		MergeRequestIID: rmd.GetMRInternalID(),
		CommitSHA:       rmd.GetCommitSHA(),
		Class:           rmd.GetChangeClass(),
		Reason:          reason,
		// Every run-event fallback is a failed run of an Auto Mode MR, whose
		// plans carry the class marker: plan the head again without it.
		Replan: true,
	})
	if err != nil {
		log.Error().Err(err).Str("project", rmd.GetMRProjectNameWithNamespace()).Int("mergeRequestID", rmd.GetMRInternalID()).
			Msg("could not fall back to Manual Mode")
	}
	return err
}
