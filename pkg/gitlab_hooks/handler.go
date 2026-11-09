package gitlab_hooks

import (
	"context"
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sl1pm4t/gongs"
	"github.com/zapier/tfbuddy/pkg/hooks_stream"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/labstack/echo/v4"
	nats "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/rs/zerolog/log"
	gogitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"github.com/zapier/tfbuddy/pkg/vcs/gitlab"
)

const GitlabTokenHeader = "X-Gitlab-Token"
const GitlabHookIgnoreReasonUnhandledEventType = "unhandled-event-type"

// gitlabDeliveryHeaders identify a webhook delivery. Some integrations
// surface only Idempotency-Key, so we accept either.
var gitlabDeliveryHeaders = []string{"X-Gitlab-Event-UUID", "Idempotency-Key"}

func gitlabDeliveryID(r *http.Request) string {
	for _, h := range gitlabDeliveryHeaders {
		if v := r.Header.Get(h); v != "" {
			return v
		}
	}
	return ""
}

type TriggerCreationFunc func(appCfg config.Config,
	gl vcs.GitClient,
	tfc tfc_api.ApiClient,
	runstream runstream.StreamClient,
	triggerCfg *tfc_trigger.TFCTriggerOptions) tfc_trigger.Trigger

type GitlabHooksHandler struct {
	cfg             config.Config
	tfc             tfc_api.ApiClient
	gl              vcs.GitClient
	runstream       runstream.StreamClient
	triggerCreation TriggerCreationFunc

	// hook streams and workers
	hookSecretKey string
	publishRetry  hooks_stream.RetryPolicy
	notesStream   *gongs.GenericStream[NoteEventMsg, *NoteEventMsg]
	mrStream      *gongs.GenericStream[MergeRequestEventMsg, *MergeRequestEventMsg]
	hooksWorker   *GitlabEventWorker
}

func NewGitlabHooksHandler(cfg config.Config, gl vcs.GitClient, tfc tfc_api.ApiClient, rs runstream.StreamClient, js nats.JetStreamContext, jsc jetstream.JetStream) *GitlabHooksHandler {
	notesStream := gongs.NewGenericStream[NoteEventMsg](js, noteEventsStreamSubject(), hooks_stream.HooksStreamName)
	mrStream := gongs.NewGenericStream[MergeRequestEventMsg](js, mrEventsStreamSubject(), hooks_stream.HooksStreamName)

	h := &GitlabHooksHandler{
		cfg:             cfg,
		tfc:             tfc,
		gl:              gl,
		runstream:       rs,
		triggerCreation: tfc_trigger.NewTFCTrigger,
		mrStream:        mrStream,
		notesStream:     notesStream,
		hookSecretKey:   cfg.GitlabHookSecretKey,
		publishRetry:    hooks_stream.DefaultRetryPolicy,
	}

	h.hooksWorker = NewGitlabEventWorker(h, jsc)

	return h
}

func (h *GitlabHooksHandler) GroupHandler() func(c echo.Context) error {
	return h.handler
}

func (h *GitlabHooksHandler) ProjectHandler() func(c echo.Context) error {
	return h.handler
}

func (h *GitlabHooksHandler) handler(c echo.Context) error {
	gitlabWebHookReceived.Inc()
	labels := prometheus.Labels{}
	// Validate X-Gitlab-Token header matches expected value
	if h.hookSecretKey != "" {
		if h.hookSecretKey != c.Request().Header.Get(GitlabTokenHeader) {
			gitlabWebHookFailed.WithLabelValues("error", "invalid-token", "").Inc()
			return c.String(http.StatusUnauthorized, "Unauthorized")
		}
	}

	eventType := gogitlab.EventType(c.Request().Header.Get("X-Gitlab-Event"))
	if eventType == "" {
		gitlabWebHookFailed.WithLabelValues("error", "invalid-event", "").Inc()
		return c.String(http.StatusBadRequest, "Invalid X-Gitlab-Event")
	}

	var proj string
	labels["eventType"] = string(eventType)
	switch eventType {
	case gogitlab.EventTypeMergeRequest:
		gitlabWebHookEnqueueAttempts.WithLabelValues(string(eventType)).Inc()
		ctx, span := otel.Tracer("GitlabHandler").Start(c.Request().Context(), "Gitlab - MergeRequestHook")
		defer span.End()

		event, err := getGitlabEventBody[gogitlab.MergeEvent](c)
		if checkError(ctx, err, "could not decode merge request event") {
			gitlabWebHookFailed.WithLabelValues(labels["eventType"], hookFailedDecode, "").Inc()
			break
		}
		log.Info().Str("project", event.Project.PathWithNamespace).Str("action", event.ObjectAttributes.Action).Int("mergeRequestID", event.ObjectAttributes.IID).Msg("processing GitLab Merge Request event")

		span.SetAttributes(
			attribute.String("project", event.Project.PathWithNamespace),
			attribute.String("action", event.ObjectAttributes.Action),
			attribute.Int("mergeRequestID", event.ObjectAttributes.IID),
		)

		msg := &MergeRequestEventMsg{
			GitlabHookEvent: GitlabHookEvent{},
			Payload:         event,
			DeliveryID:      gitlabDeliveryID(c.Request()),
		}

		proj = event.Project.PathWithNamespace
		h.publish(ctx, labels["eventType"], proj,
			fmt.Sprintf("merge request event for project: %s, merge request ID: %d", proj, event.ObjectAttributes.IID),
			func(ctx context.Context) error {
				_, err := h.mrStream.Publish(ctx, msg)
				return err
			})

	case gogitlab.EventTypeNote:
		gitlabWebHookEnqueueAttempts.WithLabelValues(string(eventType)).Inc()
		ctx, span := otel.Tracer("GitlabHandler").Start(c.Request().Context(), "Gitlab - EventTypeNote")
		defer span.End()

		event, err := getNoteEventBody(c)
		if checkError(ctx, err, "could not decode Note/Comment event") {
			gitlabWebHookFailed.WithLabelValues(labels["eventType"], hookFailedDecode, "").Inc()
			break
		}
		event.DeliveryID = gitlabDeliveryID(c.Request())
		log.Info().Str("project", event.Payload.GetProject().GetPathWithNamespace()).Int("mergeRequestID", event.Payload.GetMR().GetInternalID()).Str("discussionID", event.Payload.GetDiscussionID()).Msg("processing GitLab Note/Comment event")

		proj = event.Payload.GetProject().GetPathWithNamespace()
		h.publish(ctx, labels["eventType"], proj,
			fmt.Sprintf("note event for project: %s, merge request ID: %d, discussion ID: %s", proj, event.Payload.GetMR().GetInternalID(), event.Payload.GetDiscussionID()),
			func(ctx context.Context) error {
				_, err := h.notesStream.Publish(ctx, event)
				return err
			})

	default:
		log.Info().Msgf("Ignoring Gitlab Event type: %s", eventType)
		labels["reason"] = GitlabHookIgnoreReasonUnhandledEventType
		labels["project"] = ""
		gitlabWebHookIgnored.With(labels).Inc()
	}

	// Always answer 200: GitLab doesn't resend a failed delivery, and it
	// disables a webhook after four failed deliveries in a row.
	return c.String(http.StatusOK, "OK")
}

// publish sends a webhook event to the HOOKS stream and counts the outcome.
// A publish that fails, for example while a NATS server restarts, is retried
// in the background, because GitLab won't resend the delivery.
func (h *GitlabHooksHandler) publish(ctx context.Context, eventType, project, event string, publish func(context.Context) error) {
	// The request's context ends with the response, and the retries outlive it.
	ctx = context.WithoutCancel(ctx)
	// Each outcome is logged before it is counted, so once the counter shows
	// an outcome its log line has been written.
	ok := hooks_stream.PublishWithRetry(func() error { return publish(ctx) }, h.publishRetry, func(err error, attempts int) {
		switch {
		case err != nil:
			log.Error().Err(err).Int("attempts", attempts).
				Msgf("could not publish %s; GitLab won't resend it, so resend it from the webhook's recent events", event)
			// Recorded without the project so the series exist from startup
			// (see init in metrics.go). The project is in the error log.
			gitlabWebHookFailed.WithLabelValues(eventType, hookFailedPublish, "").Inc()
		case attempts > 1:
			log.Info().Int("attempts", attempts).Msgf("published %s after retrying", event)
			gitlabWebHookSuccess.With(webhookLabels(eventType, project, "processed-after-retry")).Inc()
		default:
			gitlabWebHookSuccess.With(webhookLabels(eventType, project, "processed")).Inc()
		}
	})
	if !ok {
		log.Warn().Msgf("could not publish %s; retrying in the background", event)
	}
}

func webhookLabels(eventType, project, reason string) prometheus.Labels {
	return prometheus.Labels{"eventType": eventType, "project": project, "reason": reason}
}

func getGitlabEventBody[T any](c echo.Context) (*T, error) {
	event := new(T)

	if err := (&echo.DefaultBinder{}).BindBody(c, &event); err != nil {
		log.Error().Err(err).Msg("failed to unmarshall event payload")
		return nil, err
	}

	return event, nil
}

func getNoteEventBody(c echo.Context) (*NoteEventMsg, error) {
	event, err := getGitlabEventBody[gogitlab.MergeCommentEvent](c)
	if err != nil {
		return nil, err
	}

	mrCommentEvt := &gitlab.GitlabMergeCommentEvent{MergeCommentEvent: event}

	ne := &NoteEventMsg{
		GitlabHookEvent: GitlabHookEvent{},
		Payload:         mrCommentEvt,
	}
	return ne, nil
}

func checkError(ctx context.Context, err error, detail string) bool {
	if err != nil {
		log.Error().Err(err).Msg(detail)
		trace.SpanFromContext(ctx).RecordError(err, trace.WithAttributes(attribute.String("detail", detail)))
		return true
	}
	return false
}
