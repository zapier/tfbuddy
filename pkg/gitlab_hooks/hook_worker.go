package gitlab_hooks

import (
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/hooks_stream"
	"github.com/zapier/tfbuddy/pkg/jsqueue"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/utils"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.opentelemetry.io/otel"
)

type GitlabEventWorker struct {
	cfg             config.Config
	tfc             tfc_api.ApiClient
	gl              vcs.GitClient
	runstream       runstream.StreamClient
	triggerCreation TriggerCreationFunc
	workspaceStream tfc_trigger.WorkspacePublisher
}

func NewGitlabEventWorker(h *GitlabHooksHandler, js nats.JetStreamContext) *GitlabEventWorker {
	w := &GitlabEventWorker{
		cfg:             h.cfg,
		tfc:             h.tfc,
		gl:              h.gl,
		runstream:       h.runstream,
		triggerCreation: tfc_trigger.NewTFCTrigger,
		workspaceStream: h.workspaceStream,
	}

	// One event can plan every workspace of a merge request, which outlasts
	// any fixed AckWait. jsqueue keeps the event in progress meanwhile, so
	// JetStream does not hand it to another replica to plan again.
	_, err := jsqueue.QueueSubscribe[MergeRequestEventMsg](js, hooks_stream.HooksStreamName, mrEventsStreamSubject(),
		"gitlab_mr_event_worker", jsqueue.Options{}, w.processMREventStreamMsg)
	if err != nil {
		log.Error().Err(err).Msg("could not subscribe to hook stream")
	}

	_, err = jsqueue.QueueSubscribe[NoteEventMsg](js, hooks_stream.HooksStreamName, noteEventsStreamSubject(),
		"gitlab_note_event_worker", jsqueue.Options{}, w.processNoteEventStreamMsg)
	if err != nil {
		log.Error().Err(err).Msg("could not subscribe to hook stream")
	}

	return w
}

func (w *GitlabEventWorker) processNoteEventStreamMsg(msg *NoteEventMsg) error {
	ctx, span := otel.Tracer("hooks").Start(msg.Context, "ProcessNoteEventStreamMsg")
	defer span.End()

	var noteErr error
	defer func() {
		if r := recover(); r != nil {
			log.Error().Msgf("Unrecoverable error in note event processing %v", r)
			noteErr = nil
		}
	}()
	_, noteErr = w.processNoteEvent(ctx, msg)

	return utils.EmitPermanentError(noteErr, func(err error) {
		log.Error().Str("project", msg.Payload.Project.PathWithNamespace).Int("mergeRequestID", msg.Payload.GetMR().GetInternalID()).Str("discussionID", msg.Payload.GetDiscussionID()).Msgf("got permanent error processing Note event: %s", err.Error())
	})
}

func (w *GitlabEventWorker) processMREventStreamMsg(msg *MergeRequestEventMsg) error {
	var mrErr error
	defer func() {
		if r := recover(); r != nil {
			log.Error().Msgf("Unrecoverable error in MR event processing %v", r)
			mrErr = nil
		}
	}()
	_, mrErr = w.processMergeRequestEvent(msg)

	return utils.EmitPermanentError(mrErr, func(err error) {
		log.Error().Str("project", msg.Payload.Project.PathWithNamespace).Int("mergeRequestID", msg.Payload.ObjectAttributes.IID).Msgf("got permanent error processing MR event: %s", err.Error())
	})
}
