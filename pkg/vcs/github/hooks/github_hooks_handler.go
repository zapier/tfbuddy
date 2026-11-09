package hooks

import (
	"context"

	"github.com/cbrgm/githubevents/githubevents"
	"github.com/google/go-github/v69/github"
	"github.com/labstack/echo/v4"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
	"github.com/sl1pm4t/gongs"
	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/hooks_stream"
	"github.com/zapier/tfbuddy/pkg/jsqueue"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type TriggerCreationFunc func(
	cfg config.Config,
	vcs vcs.GitClient,
	tfc tfc_api.ApiClient,
	runstream runstream.StreamClient,
	triggerCfg *tfc_trigger.TFCTriggerOptions,
) tfc_trigger.Trigger

type GithubHooksHandler struct {
	cfg             config.Config
	tfc             tfc_api.ApiClient
	vcs             vcs.GitClient
	runstream       runstream.StreamClient
	js              nats.JetStreamContext
	ghEvents        *githubevents.EventHandler
	triggerCreation TriggerCreationFunc

	publishRetry hooks_stream.RetryPolicy

	// streams
	prStream      *gongs.GenericStream[PullRequestEventMsg, *PullRequestEventMsg]
	commentStream *gongs.GenericStream[GithubIssueCommentEventMsg, *GithubIssueCommentEventMsg]
}

func NewGithubHooksHandler(cfg config.Config, vcs vcs.GitClient, tfc tfc_api.ApiClient, rs runstream.StreamClient, js nats.JetStreamContext, jsc jetstream.JetStream) *GithubHooksHandler {
	prStream := gongs.NewGenericStream[PullRequestEventMsg](js, getGithubJetstreamName(), getGithubJetstreamSubject(PullRequestEventType))
	commentStream := gongs.NewGenericStream[GithubIssueCommentEventMsg](js, getGithubJetstreamName(), getGithubJetstreamSubject(IssueCommentEvent))

	h := &GithubHooksHandler{
		cfg:             cfg,
		tfc:             tfc,
		vcs:             vcs,
		runstream:       rs,
		js:              js,
		commentStream:   commentStream,
		prStream:        prStream,
		triggerCreation: tfc_trigger.NewTFCTrigger,
		publishRetry:    hooks_stream.DefaultRetryPolicy,
	}

	ghEvents := githubevents.New(cfg.GithubHookSecretKey)

	// add Github event callbacks
	ghEvents.OnIssueCommentCreated(h.handleIssueCommentCreatedEvent)
	ghEvents.OnError(onError)
	h.ghEvents = ghEvents

	// wire up worker callbacks
	// gongs.NewGenericStream takes the subject before the stream, so comment
	// events are published on getGithubJetstreamName().
	_, err := jsqueue.Consume[GithubIssueCommentEventMsg](jsc, jsqueue.Queue{
		Stream:            hooks_stream.HooksStreamName,
		Subject:           getGithubJetstreamName(),
		Durable:           "github_comment_event_pull_worker",
		LegacyPushDurable: "github_comment_event_worker",
	}, jsqueue.Options{}, h.processIssueCommentEvent)
	if err != nil {
		log.Error().Err(err).Msg("github worker: could not subscribe to hook stream")
	}

	return h
}

func (h *GithubHooksHandler) Handler(c echo.Context) error {
	err := h.ghEvents.HandleEventRequest(c.Request())
	if err != nil {
		log.Printf("error: %v\n", err)
	}
	return c.String(200, "NOK")
}

func onError(deliveryID string, eventName string, event interface{}, err error) error {
	_, span := otel.Tracer("GithubEvents").Start(context.Background(), "Github - ErrorHookHandler")
	defer span.End()

	log.Warn().Str("deliveryID", deliveryID).Str("eventName", eventName).Err(err).Msg("GitHub hook handler error")
	lbl := prometheus.Labels{}
	lbl["eventType"] = eventName
	lbl["repository"] = ""
	span.RecordError(err, trace.WithAttributes(
		attribute.String("event-type", eventName),
		attribute.String("deliveryID", deliveryID),
	))
	log.Error().Err(err).Msg("unexpected error while processing Github event")
	githubWebHookFailed.With(lbl).Inc()
	return nil
}

func (h *GithubHooksHandler) handleIssueCommentCreatedEvent(deliveryID string, eventName string, event *github.IssueCommentEvent) error {
	ctx, span := otel.Tracer("GithubHandler").Start(context.Background(), "Github - HooksHandler")
	defer span.End()

	lbls := prometheus.Labels{
		"eventType":  eventName,
		"repository": *event.Repo.FullName,
	}
	msg := &GithubIssueCommentEventMsg{
		Payload:    event,
		DeliveryID: deliveryID,
	}
	// GitHub doesn't redeliver a failed delivery on its own, so a publish that
	// fails, for example while a NATS server restarts, is retried in the
	// background after the webhook has been answered.
	ok := hooks_stream.PublishWithRetry(func() error {
		_, err := h.commentStream.Publish(ctx, msg)
		return err
	}, h.publishRetry, func(err error, attempts int) {
		if err != nil {
			// Logged before it is counted, so once the counter shows the
			// failure its log line has been written.
			log.Error().Err(err).Str("deliveryID", deliveryID).Int("attempts", attempts).
				Msg("could not publish GitHub issue comment event; GitHub won't redeliver it, so redeliver it from the webhook's recent deliveries")
			githubWebHookFailed.With(lbls).Inc()
			return
		}
		githubWebHookSuccess.With(lbls).Inc()
	})
	if !ok {
		log.Warn().Str("deliveryID", deliveryID).Msg("could not publish GitHub issue comment event; retrying in the background")
	}

	return nil
}
