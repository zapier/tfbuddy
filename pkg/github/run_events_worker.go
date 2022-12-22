package github

import (
	"fmt"

	"github.com/hashicorp/go-tfe"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/comment_formatter"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/vcs"
)

const runEventsConsumerDurableName = "github"

type RunEventsWorker struct {
	client       vcs.GitClient
	rs           runstream.StreamClient
	tfc          tfc_api.ApiClient
	eventQCloser func()
}

func NewRunEventsWorker(client *Client, rs runstream.StreamClient, tfc tfc_api.ApiClient) *RunEventsWorker {
	rsp := &RunEventsWorker{
		client: client,
		rs:     rs,
		tfc:    tfc,
	}

	// subscribe to TFRunEvents (TFC Notifications)
	var err error
	_, err = rs.SubscribeTFRunEvents(runEventsConsumerDurableName, rsp.eventStreamCallback)
	if err != nil {
		log.Fatal().Err(err).Msg("could not create RunStream subscription")
	}

	return rsp
}

func (w *RunEventsWorker) Close() {
	w.eventQCloser()
}

// eventStreamCallback processes TFC run notifications via the NATS stream
func (w *RunEventsWorker) eventStreamCallback(re runstream.RunEvent) bool {
	log.Debug().Interface("TFRunEvent", re).Msg("Gitlab RunEventsWorker.eventStreamCallback()")

	run, err := w.tfc.GetRun(re.GetRunID())
	if err != nil {
		log.Error().Err(err).Str("runID", re.GetRunID()).Msg("could not get run")
		return false
	}
	run.Status = tfe.RunStatus(re.GetNewStatus())

	w.postRunStatusComment(run, re.GetMetadata())
	//w.updateCommitStatusForRun(run, re.GetMetadata())
	return true
}

func (w *RunEventsWorker) postRunStatusComment(run *tfe.Run, rmd runstream.RunMetadata) {

	commentBody, _, _ := comment_formatter.FormatRunStatusCommentBody(w.tfc, run, rmd)

	if commentBody != "" {
		w.client.CreateMergeRequestComment(
			rmd.GetMRInternalID(),
			rmd.GetMRProjectNameWithNamespace(),
			fmt.Sprintf(
				"Status: `%s`<br>%s",
				run.Status,
				commentBody),
		)

	}

}
