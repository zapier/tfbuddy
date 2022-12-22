package gitlab_hooks

import (
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sl1pm4t/gongs"
	"github.com/zapier/tfbuddy/pkg/hooks_stream"

	"github.com/labstack/echo/v4"
	nats "github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
	gogitlab "github.com/xanzy/go-gitlab"

	"github.com/zapier/tfbuddy/pkg/gitlab"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/vcs"
)

const GitlabTokenHeader = "X-Gitlab-Token"
const GitlabHookIgnoreReasonUnhandledEventType = "unhandled-event-type"

type TriggerCreationFunc func(gl vcs.GitClient,
	tfc tfc_api.ApiClient,
	runstream runstream.StreamClient,
	cfg tfc_trigger.TriggerConfig) tfc_trigger.Trigger

type GitlabHooksHandler struct {
	tfc             tfc_api.ApiClient
	gl              vcs.GitClient
	runstream       runstream.StreamClient
	triggerCreation TriggerCreationFunc

	// hook streams and workers
	hookSecretKey string
	notesStream   *gongs.GenericStream[NoteEventMsg, *NoteEventMsg]
	mrStream      *gongs.GenericStream[MergeRequestEventMsg, *MergeRequestEventMsg]
	hooksWorker   *GitlabEventWorker
}

func NewGitlabHooksHandler(gl vcs.GitClient, tfc tfc_api.ApiClient, rs runstream.StreamClient, js nats.JetStreamContext) *GitlabHooksHandler {
	hookSecretEnv := os.Getenv("TFBUDDY_GITLAB_HOOK_SECRET_KEY")
	notesStream := gongs.NewGenericStream[NoteEventMsg](js, noteEventsStreamSubject(), hooks_stream.HooksStreamName)
	mrStream := gongs.NewGenericStream[MergeRequestEventMsg](js, mrEventsStreamSubject(), hooks_stream.HooksStreamName)

	h := &GitlabHooksHandler{
		tfc:             tfc,
		gl:              gl,
		runstream:       rs,
		triggerCreation: tfc_trigger.NewTFCTrigger,
		mrStream:        mrStream,
		notesStream:     notesStream,
		hookSecretKey:   hookSecretEnv,
	}

	h.hooksWorker = NewGitlabEventWorker(h, js)

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

	var err error
	var proj string
	labels["eventType"] = string(eventType)
	switch eventType {
	case gogitlab.EventTypeMergeRequest:
		log.Info().Msg("processing GitLab Merge Request event")

		event, err := getGitlabEventBody[gogitlab.MergeEvent](c)
		if checkError(err, "could not decode merge request event") {
			break
		}
		msg := &MergeRequestEventMsg{
			GitlabHookEvent: GitlabHookEvent{},
			payload:         event,
		}

		proj = event.Project.PathWithNamespace
		_, err = h.mrStream.Publish(msg)
		checkError(err, "could not publish merge request event to stream")

	case gogitlab.EventTypeNote:
		log.Info().Msg("processing GitLab Note/Comment event")

		event, err := getNoteEventBody(c)
		if checkError(err, "could not decode Note/Comment event") {
			break
		}

		proj = event.payload.GetProject().GetPathWithNamespace()
		_, err = h.notesStream.Publish(event)
		checkError(err, "could not publish note event to stream")

	default:
		log.Info().Msgf("Ignoring Gitlab Event type: %s", eventType)
		labels["reason"] = GitlabHookIgnoreReasonUnhandledEventType
		labels["project"] = ""
		gitlabWebHookIgnored.With(labels).Inc()
		return c.String(http.StatusOK, "OK")
	}
	labels["project"] = proj

	if err != nil {
		labels["reason"] = "error"
		gitlabWebHookFailed.With(labels).Inc()
	} else {
		labels["reason"] = "processed"
		gitlabWebHookSuccess.With(labels).Inc()
	}

	return c.String(http.StatusOK, "OK")
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
		payload:         mrCommentEvt,
	}
	return ne, nil
}

func checkError(err error, detail string) bool {
	if err != nil {
		log.Error().Err(err).Msg(detail)
		return true
	}
	return false
}
