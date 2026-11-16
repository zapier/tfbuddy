package github

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/hashicorp/go-tfe"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/comment_formatter"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.opentelemetry.io/otel"
)

const runEventsConsumerDurableName = "github"

// runResultPoster posts a run's final comment and deletes it again if a
// newer run replaced the run meanwhile. *Client implements it.
type runResultPoster interface {
	PostRunResult(ctx context.Context, prID int, fullName string, rootCommentID int, body string) error
}

type RunEventsWorker struct {
	cfg          config.Config
	client       vcs.GitClient
	rs           runstream.StreamClient
	tfc          tfc_api.ApiClient
	eventQCloser func()
}

func NewRunEventsWorker(cfg config.Config, client *Client, rs runstream.StreamClient, tfc tfc_api.ApiClient) *RunEventsWorker {
	rsp := &RunEventsWorker{
		cfg:    cfg,
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
	ctx, span := otel.Tracer("TFC").Start(re.GetContext(), "eventStreamCallback")
	defer span.End()

	log.Debug().Interface("TFRunEvent", re).Msg("Gitlab RunEventsWorker.eventStreamCallback()")

	run, err := w.tfc.GetRun(ctx, re.GetRunID())
	if err != nil {
		span.RecordError(err)
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

	w.postRunStatusComment(ctx, run, re.GetMetadata())
	//w.updateCommitStatusForRun(run, re.GetMetadata())
	if tfc_api.IsFinalRunStatus(run.Status) {
		w.rs.RecordRunFinished("github", re)
	}
	return true
}

func (w *RunEventsWorker) postRunStatusComment(ctx context.Context, run *tfe.Run, rmd runstream.RunMetadata) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "postRunStatusComment")
	defer span.End()

	commentBody, topLevelNoteBody, _ := comment_formatter.FormatRunStatusCommentBody(w.cfg, w.tfc, run, rmd)

	final := run.Status == tfe.RunErrored || run.Status == tfe.RunCanceled || run.Status == tfe.RunDiscarded || run.Status == tfe.RunPlannedAndFinished
	superseded := false
	if topLevelNoteBody != "" {
		if final {
			oldUrls, err := w.client.GetOldRunUrls(ctx, rmd.GetMRInternalID(), rmd.GetMRProjectNameWithNamespace(), startingCommentID(rmd), run.ID, run.Workspace.Name, rmd.GetAction())
			switch {
			case errors.Is(err, vcs.ErrRunSuperseded):
				superseded = true
				log.Warn().Str("project", rmd.GetMRProjectNameWithNamespace()).Int("prID", rmd.GetMRInternalID()).Str("runID", run.ID).Str("runStatus", string(run.Status)).
					Msg("a newer run of the same workspace has replaced this run's comments; skipping its final comment")
			case err != nil:
				log.Error().Str("project", rmd.GetMRProjectNameWithNamespace()).Int("prID", rmd.GetMRInternalID()).Err(err).Msg("could not retrieve old run urls")
			}
			if oldUrls != "" {
				topLevelNoteBody = fmt.Sprintf("%s\n\n%s", oldUrls, topLevelNoteBody)
			}
		}
	}

	if topLevelNoteBody != "" && !superseded {
		body := topLevelNoteBody
		if commentBody != "" {
			body += fmt.Sprintf("\n%s", commentBody)
		}
		if poster, ok := w.client.(runResultPoster); ok && final {
			err := poster.PostRunResult(ctx, rmd.GetMRInternalID(), rmd.GetMRProjectNameWithNamespace(), startingCommentID(rmd), body)
			switch {
			case errors.Is(err, vcs.ErrRunSuperseded):
				log.Warn().Str("project", rmd.GetMRProjectNameWithNamespace()).Int("prID", rmd.GetMRInternalID()).Str("runID", run.ID).Str("runStatus", string(run.Status)).
					Msg("a newer run of the same workspace replaced this run while its result was being posted; deleted the stale result")
			case err != nil:
				log.Error().Str("project", rmd.GetMRProjectNameWithNamespace()).Int("prID", rmd.GetMRInternalID()).Str("runID", run.ID).Err(err).Msg("could not post the run's result")
			}
		} else {
			w.client.CreateMergeRequestComment(
				ctx,
				rmd.GetMRInternalID(),
				rmd.GetMRProjectNameWithNamespace(),
				body,
			)
		}
	}
	if run.Status == tfe.RunApplied {
		if len(run.TargetAddrs) > 0 {
			return
		}
		// The applying phase of a run has completed.
		w.mergePRIfPossible(ctx, rmd)
	}
	if run.Status == tfe.RunPlannedAndFinished {
		if len(run.TargetAddrs) > 0 {
			return
		}
		if rmd.GetAction() == runstream.ApplyAction {
			w.mergePRIfPossible(ctx, rmd)
		}
	}
}

// startingCommentID returns the ID of the comment the trigger posted when it
// started the run. On GitHub the run's discussion ID is that comment's ID;
// the root note ID is only set for GitLab threads.
func startingCommentID(rmd runstream.RunMetadata) int {
	if id := rmd.GetRootNoteID(); id > 0 {
		return int(id)
	}
	id, err := strconv.Atoi(rmd.GetDiscussionID())
	if err != nil {
		return 0
	}
	return id
}

func (w *RunEventsWorker) mergePRIfPossible(ctx context.Context, rmd runstream.RunMetadata) {
	if !rmd.GetAutoMerge() {
		return
	}
	w.client.MergeMR(ctx, rmd.GetMRInternalID(), rmd.GetMRProjectNameWithNamespace())
}
