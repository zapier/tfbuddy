package gitlab_hooks

import (
	"github.com/prometheus/client_golang/prometheus"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

// Reasons tfbuddy_gitlab_webhook_failed records for an event TFBuddy accepted
// but could not queue.
const (
	hookFailedDecode  = "decode-error"
	hookFailedPublish = "publish-error"
)

var (
	gitlabWebHookReceived = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "tfbuddy_gitlab_webhook_received",
		Help: "Count of all GitLab webhooks received",
	})
	commonLabels = []string{
		"eventType",
		"reason",
		"project",
	}
	gitlabWebHookSuccess = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "tfbuddy_gitlab_webhook_success",
			Help: "Count of all GitLab WebHook that were published to stream",
		},
		commonLabels,
	)
	// gitlabWebHookFailed's reason is invalid-token, invalid-event,
	// decode-error or publish-error. The handler answers GitLab with 200 OK
	// either way, so this counter is the only sign that an event was lost.
	// decode-error and publish-error leave the project empty, so their series
	// can be created at zero on startup: rate() ignores the value a series
	// starts with, and a series that first appeared at 1 would hide the
	// first lost event.
	gitlabWebHookFailed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "tfbuddy_gitlab_webhook_failed",
			Help: "Count of all GitLab WebHooks that were rejected or could not be published to the stream, by reason",
		},
		commonLabels,
	)
	gitlabWebHookIgnored = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "tfbuddy_gitlab_webhook_ignored",
			Help: "Count of all GitLab WebHook that were ignored",
		},
		commonLabels,
	)

	gitlabHookReadFromStream = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "tfbuddy_gitlab_hook_stream_msg_read",
		Help: "Count of all GitLab hook messages read off the stream",
	})
)

func init() {
	r := prometheus.DefaultRegisterer
	r.MustRegister(gitlabWebHookReceived)
	r.MustRegister(gitlabWebHookSuccess)
	r.MustRegister(gitlabWebHookFailed)
	for _, eventType := range []gogitlab.EventType{gogitlab.EventTypeMergeRequest, gogitlab.EventTypeNote} {
		for _, reason := range []string{hookFailedDecode, hookFailedPublish} {
			gitlabWebHookFailed.WithLabelValues(string(eventType), reason, "")
		}
	}
	r.MustRegister(gitlabWebHookIgnored)

	r.MustRegister(gitlabHookReadFromStream)
}
