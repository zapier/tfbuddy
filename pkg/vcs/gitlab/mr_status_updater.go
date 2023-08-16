package gitlab

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/hashicorp/go-tfe"
	"github.com/rs/zerolog/log"
	gogitlab "github.com/xanzy/go-gitlab"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"go.opentelemetry.io/otel"
)

// Sentinel error
var errNoPipelineStatus = errors.New("nil pipeline status")

func (p *RunStatusUpdater) updateCommitStatusForRun(ctx context.Context, run *tfe.Run, rmd runstream.RunMetadata) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "updateCommitStatusForRun")
	defer span.End()

	switch run.Status {
	// https://www.terraform.io/cloud-docs/api-docs/run#run-states
	case tfe.RunPending:
		// The initial status of a run once it has been created.
		if rmd.GetAction() == "plan" {
			p.updateStatus(ctx, gogitlab.Pending, "plan", rmd)
			p.updateStatus(ctx, gogitlab.Failed, "apply", rmd)
		} else {
			p.updateStatus(ctx, gogitlab.Pending, "apply", rmd)
		}

	case tfe.RunApplyQueued:
		// Once the changes in the plan have been confirmed, the run run will transition to apply_queued.
		// This status indicates that the run should start as soon as the backend services have available capacity.
		p.updateStatus(ctx, gogitlab.Pending, "apply", rmd)

	case tfe.RunApplying:
		// The applying phase of a run is in progress.
		p.updateStatus(ctx, gogitlab.Running, "apply", rmd)

	case tfe.RunApplied:
		if len(run.TargetAddrs) > 0 {
			p.updateStatus(ctx, gogitlab.Pending, "apply", rmd)
			return
		}
		// The applying phase of a run has completed.
		p.updateStatus(ctx, gogitlab.Success, "apply", rmd)

	case tfe.RunCanceled:
		// The run has been discarded. This is a final state.
		p.updateStatus(ctx, gogitlab.Failed, rmd.GetAction(), rmd)

	case tfe.RunDiscarded:
		// The run has been discarded. This is a final state.
		p.updateStatus(ctx, gogitlab.Failed, "plan", rmd)
		p.updateStatus(ctx, gogitlab.Failed, "apply", rmd)

	case tfe.RunErrored:
		// The run has errored. This is a final state.
		p.updateStatus(ctx, gogitlab.Failed, rmd.GetAction(), rmd)

	case tfe.RunPlanning:
		// The planning phase of a run is in progress.
		p.updateStatus(ctx, gogitlab.Running, rmd.GetAction(), rmd)

	case tfe.RunPlanned:
		// this status is for Apply runs (as opposed to `RunPlannedAndFinished` below, so don't update the status.
		return

	case tfe.RunPlannedAndFinished:
		// The completion of a run containing a plan only, or a run the produces a plan with no changes to apply.
		// This is a final state.
		p.updateStatus(ctx, gogitlab.Success, rmd.GetAction(), rmd)
		if run.HasChanges {
			// TODO: is pending enough to block merging before apply?
			p.updateStatus(ctx, gogitlab.Pending, "apply", rmd)
		}

	case tfe.RunPolicySoftFailed:
		// A sentinel policy has soft failed for a plan-only run. This is a final state.
		// During the apply, the policy failure will need to be overriden.
		p.updateStatus(ctx, gogitlab.Success, rmd.GetAction(), rmd)

	case tfe.RunPolicyChecked:
		// The sentinel policy checking phase of a run has completed.

		// no op

	default:
		log.Debug().Str("status", string(run.Status)).Msg("ignoring run status")
		return
	}

}

func (p *RunStatusUpdater) updateStatus(ctx context.Context, state gogitlab.BuildStateValue, action string, rmd runstream.RunMetadata) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "updateStatus")
	defer span.End()

	status := &gogitlab.SetCommitStatusOptions{
		Name:        statusName(rmd.GetWorkspace(), action),
		Context:     statusName(rmd.GetWorkspace(), action),
		TargetURL:   runUrlForTFRunMetadata(rmd),
		Description: descriptionForState(state),
		State:       state,
	}

	// Look up the latest pipeline ID for this MR, since Gitlab is eventually consistent
	// Once we have a pipeline ID returned, we know we have a valid pipeline to set commit status for
	var pipelineID *int
	getPipelineIDFn := func() error {
		log.Debug().Msg("getting pipeline status")
		pipelineID := p.getLatestPipelineID(ctx, rmd)
		if pipelineID == nil {
			return errNoPipelineStatus
		}
		return nil
	}

	err := backoff.Retry(getPipelineIDFn, configureBackOff())
	if err != nil {
		log.Warn().Msg("could not retrieve pipeline id after multiple attempts")
	}
	if pipelineID != nil {
		log.Trace().Int("pipeline_id", *pipelineID).Msg("pipeline status")
		status.PipelineID = pipelineID
	}

	log.Debug().Interface("new_status", status).Msg("updating Gitlab commit status")
	cs, err := p.client.SetCommitStatus(
		ctx,
		rmd.GetMRProjectNameWithNamespace(),
		rmd.GetCommitSHA(),
		&GitlabCommitStatusOptions{status},
	)
	if err != nil {
		log.Error().Err(err).Interface("status", status).Msg("could not update status")
		return
	}
	log.Debug().Interface("commit_status", cs.Info()).Msg("updated Commit Status")
}

func statusName(ws, action string) *string {
	return gogitlab.String(fmt.Sprintf("TFC/%v/%s", action, ws))
}

func descriptionForState(state gogitlab.BuildStateValue) *string {
	switch state {
	case gogitlab.Pending:
		return gogitlab.String("pending...")
	case gogitlab.Running:
		return gogitlab.String("in progress...")
	case gogitlab.Failed:
		return gogitlab.String("failed.")
	case gogitlab.Success:
		return gogitlab.String("succeeded.")
	}
	return gogitlab.String("unknown")
}

func runUrlForTFRunMetadata(rmd runstream.RunMetadata) *string {
	return gogitlab.String(fmt.Sprintf(
		"https://app.terraform.io/app/%s/workspaces/%s/runs/%s",
		rmd.GetOrganization(),
		rmd.GetWorkspace(),
		rmd.GetRunID(),
	))
}

func (p *RunStatusUpdater) getLatestPipelineID(ctx context.Context, rmd runstream.RunMetadata) *int {
	pipelines, err := p.client.GetPipelinesForCommit(ctx, rmd.GetMRProjectNameWithNamespace(), rmd.GetCommitSHA())
	if err != nil {
		log.Error().Err(err).Msg("could not retrieve pipelines for commit")
		return nil
	}
	log.Trace().Interface("pipelines", pipelines).Msg("retrieved pipelines for commit")
	if len(pipelines) > 0 {
		for _, p := range pipelines {
			if p.GetSource() == "merge_request_event" {
				return gogitlab.Int(p.GetID())
			}
		}
	}
	return nil
}

// configureBackOff returns a backoff configuration to use to retry requests
func configureBackOff() *backoff.ExponentialBackOff {

	// Lets setup backoff logic to retry this request for 30 seconds
	expBackOff := backoff.NewExponentialBackOff()
	expBackOff.MaxInterval = 10 * time.Second
	expBackOff.MaxElapsedTime = 30 * time.Second

	return expBackOff
}
