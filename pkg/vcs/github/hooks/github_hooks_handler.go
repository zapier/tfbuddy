package hooks

import (
	"context"
	"fmt"
	"os"

	"github.com/cbrgm/githubevents/githubevents"
	"github.com/google/go-github/v49/github"
	"github.com/labstack/echo/v4"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
	"github.com/sl1pm4t/gongs"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type TriggerCreationFunc func(
	vcs vcs.GitClient,
	tfc tfc_api.ApiClient,
	runstream runstream.StreamClient,
	cfg *tfc_trigger.TFCTriggerOptions,
) tfc_trigger.Trigger

type GithubHooksHandler struct {
	tfc             tfc_api.ApiClient
	vcs             vcs.GitClient
	runstream       runstream.StreamClient
	js              nats.JetStreamContext
	ghEvents        *githubevents.EventHandler
	triggerCreation TriggerCreationFunc

	// streams
	prStream      *gongs.GenericStream[PullRequestEventMsg, *PullRequestEventMsg]
	commentStream *gongs.GenericStream[GithubIssueCommentEventMsg, *GithubIssueCommentEventMsg]
}

func NewGithubHooksHandler(vcs vcs.GitClient, tfc tfc_api.ApiClient, rs runstream.StreamClient, js nats.JetStreamContext) *GithubHooksHandler {
	hookSecretEnv := os.Getenv("TFBUDDY_GITHUB_HOOK_SECRET_KEY")
	prStream := gongs.NewGenericStream[PullRequestEventMsg](js, getGithubJetstreamName(), getGithubJetstreamSubject(PullRequestEventType))
	commentStream := gongs.NewGenericStream[GithubIssueCommentEventMsg](js, getGithubJetstreamName(), getGithubJetstreamSubject(IssueCommentEvent))

	h := &GithubHooksHandler{
		tfc:             tfc,
		vcs:             vcs,
		runstream:       rs,
		js:              js,
		commentStream:   commentStream,
		prStream:        prStream,
		triggerCreation: tfc_trigger.NewTFCTrigger,
	}

	ghEvents := githubevents.New(hookSecretEnv)

	// add Github event callbacks
	ghEvents.OnIssueCommentCreated(h.handleIssueCommentCreatedEvent)
	ghEvents.OnError(onError)
	h.ghEvents = ghEvents

	// wire up worker callbacks
	_, err := commentStream.QueueSubscribe("github_comment_event_worker", h.processIssueCommentEvent)
	if err != nil {
		log.Error().Err(err).Msg("github worker: could not subscribe to hook stream")
	}

	return h
}

func (h *GithubHooksHandler) Handler(c echo.Context) error {
	err := h.ghEvents.HandleEventRequest(c.Request())
	if err != nil {
		fmt.Printf("error: %v\n", err)
	}
	return c.String(200, "NOK")
}

func onError(deliveryID string, eventName string, event interface{}, err error) error {
	_, span := otel.Tracer("GithubEvents").Start(context.Background(), "Github - ErrorHookHandler")
	defer span.End()

	log.Warn().Str("deliveryID", deliveryID).Str("eventName", eventName).Err(err).Msg("GitHub hook handler error")
	lbl := prometheus.Labels{}
	lbl["event-type"] = eventName
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
	_, err := h.commentStream.Publish(ctx, &GithubIssueCommentEventMsg{Payload: event})
	if err != nil {
		githubWebHookFailed.With(lbls).Inc()
	}
	githubWebHookSuccess.With(lbls).Inc()

	return nil
}
