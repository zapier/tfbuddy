package tfc_hooks

import (
	"github.com/hashicorp/go-tfe"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"go.opentelemetry.io/otel"
)

// pollingStreamCallback processes TFC run polling tasks for speculative plans. We do not receive webhook notifications
// for speculative plans, so they need to be polled instead.
func (p *NotificationHandler) pollingStreamCallback(task runstream.RunPollingTask) bool {
	ctx, span := otel.Tracer("TFC").Start(task.GetContext(), "PollingStreamCallback")
	defer span.End()

	log.Debug().Interface("task", task).Msg("TFC Run Polling Callback()")

	run, err := p.api.GetRun(ctx, task.GetRunID())
	if err != nil {
		log.Error().Err(err).Str("runID", task.GetRunID()).Msg("could not get run")
		return false
	}

	log.Debug().
		Interface("run.Status", run.Status).
		Str("lastStatus", task.GetLastStatus()).
		Msg("processing run polling task.")

	if string(run.Status) != task.GetLastStatus() {
		// Publish new RunEvent
		err = p.stream.PublishTFRunEvent(ctx, &runstream.TFRunEvent{
			Organization: run.Workspace.Organization.Name,
			Workspace:    run.Workspace.Name,
			RunID:        run.ID,
			NewStatus:    string(run.Status),
		})
		if err != nil {
			span.RecordError(err)
			log.Error().Err(err).Str("runID", task.GetRunID()).Msg("could not publish run event")
			return false
		}

	}

	if isRunning(run) {
		// queue another polling task
		task.SetLastStatus(string(run.Status))
		if err := task.Reschedule(ctx); err != nil {
			log.Error().Err(err).Msg("could not reschedule TFC run polling task")
		}

	} else {
		if err := task.Completed(); err != nil {
			log.Error().Err(err).Msg("could not flag TFC run polling task as complete")
		}

	}
	return true
}

func isRunning(run *tfe.Run) bool {
	// Get current run
	if run == nil {
		return false
	}

	switch run.Status {
	case "apply_queued":
		fallthrough
	case "applying":
		fallthrough
	case "cost_estimating":
		fallthrough
	case "plan_queued":
		fallthrough
	case "policy_checking":
		fallthrough
	case "planning":
		fallthrough
	case "pending":
		return true
	}

	return false
}
