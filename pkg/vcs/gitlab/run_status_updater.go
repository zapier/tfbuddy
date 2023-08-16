package gitlab

import (
	"github.com/hashicorp/go-tfe"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.opentelemetry.io/otel"
)

type RunStatusUpdater struct {
	client       vcs.GitClient
	rs           runstream.StreamClient
	tfc          tfc_api.ApiClient
	eventQCloser func()
}

func NewRunStatusProcessor(client *GitlabClient, rs runstream.StreamClient, tfc tfc_api.ApiClient) *RunStatusUpdater {
	rsp := &RunStatusUpdater{
		client: client,
		rs:     rs,
		tfc:    tfc,
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
	run.Status = tfe.RunStatus(re.GetNewStatus())

	p.postRunStatusComment(ctx, run, re.GetMetadata())
	p.updateCommitStatusForRun(ctx, run, re.GetMetadata())
	return true
}
