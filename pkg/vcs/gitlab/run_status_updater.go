package gitlab

import (
	"context"

	"github.com/hashicorp/go-tfe"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.opentelemetry.io/otel"
)

type RunStatusUpdater struct {
	cfg          config.Config
	client       vcs.GitClient
	rs           runstream.StreamClient
	tfc          tfc_api.ApiClient
	eventQCloser func()
	// replanner asks for the Manual Mode re-plan of an Auto Mode MR that
	// falls back. The re-plan never runs in this run-event callback.
	replanner tfc_trigger.ManualModeReplanner
}

// NewRunStatusProcessor subscribes to GitLab run events. replanner, which may
// be nil, asks for the Manual Mode re-plan of an Auto Mode MR that falls back.
func NewRunStatusProcessor(cfg config.Config, client *GitlabClient, rs runstream.StreamClient, tfc tfc_api.ApiClient, replanner tfc_trigger.ManualModeReplanner) *RunStatusUpdater {
	rsp := &RunStatusUpdater{
		cfg:       cfg,
		client:    client,
		rs:        rs,
		tfc:       tfc,
		replanner: replanner,
	}

	// subscribe to TFRunEvents (TFC Notifications)
	var err error
	rsp.eventQCloser, err = rs.SubscribeTFRunEvents("gitlab", rsp.eventStreamCallback)
	if err != nil {
		log.Fatal().Err(err).Msg("could not create RunStream subscription")
	}

	return rsp
}

func (p *RunStatusUpdater) Close() {
	p.eventQCloser()
}

// runFinishedSince reports whether the run finished while re was being
// handled. The comment above can take seconds, and in that time another
// replica may handle the run's final event and set its final status. Writing
// re's in-progress status after that would undo it, so it is skipped. A
// failed check keeps the old behaviour and writes the status.
func (p *RunStatusUpdater) runFinishedSince(ctx context.Context, re runstream.RunEvent) bool {
	if tfc_api.IsFinalRunStatus(tfe.RunStatus(re.GetNewStatus())) {
		return false
	}
	run, err := p.tfc.GetRun(ctx, re.GetRunID())
	if err != nil {
		log.Warn().Err(err).Str("runID", re.GetRunID()).Msg("could not recheck the run before updating its status")
		return false
	}
	if !tfc_api.IsStaleRunEvent(run.Status, re.GetNewStatus()) {
		return false
	}
	log.Info().Str("runID", re.GetRunID()).
		Str("eventStatus", re.GetNewStatus()).
		Str("runStatus", string(run.Status)).
		Msg("not updating the commit status: the run finished while its event was handled")
	return true
}

// eventStreamCallback processes TFC run notifications via the NATS stream
func (p *RunStatusUpdater) eventStreamCallback(re runstream.RunEvent) bool {
	ctx, span := otel.Tracer("TFC").Start(re.GetContext(), "eventStreamCallback")
	defer span.End()

	log.Debug().Interface("TFRunEvent", re).Msg("Gitlab RunStatusUpdater.eventStreamCallback()")

	run, err := p.tfc.GetRun(ctx, re.GetRunID())
	if err != nil {
		log.Error().Err(err).Str("runID", re.GetRunID()).Msg("could not get run")
		return false
	}
	if tfc_api.IsStaleRunEvent(run.Status, re.GetNewStatus()) {
		log.Info().Str("runID", re.GetRunID()).
			Str("eventStatus", re.GetNewStatus()).
			Str("runStatus", string(run.Status)).
			Msg("skipping a stale run event: the run has already finished")
		return true
	}
	run.Status = tfe.RunStatus(re.GetNewStatus())

	p.postRunStatusComment(ctx, run, re.GetMetadata())
	if p.runFinishedSince(ctx, re) {
		return true
	}
	err = p.updateCommitStatusForRun(ctx, run, re.GetMetadata())
	classErr := p.updateClassStatus(ctx, run, re.GetMetadata())
	if err != nil {
		span.RecordError(err)
		log.Error().Err(err).Str("runID", re.GetRunID()).Msg("could not reconcile auto-merge state")
		return false
	}
	if classErr != nil {
		// Handled again later, so that a plan's outcome is not lost while
		// NATS or GitLab is briefly unavailable.
		span.RecordError(classErr)
		log.Error().Err(classErr).Str("runID", re.GetRunID()).Msg("could not reconcile the class status")
		return false
	}
	if tfc_api.IsFinalRunStatus(run.Status) {
		p.rs.RecordRunFinished("gitlab", re)
	}
	return true
}
