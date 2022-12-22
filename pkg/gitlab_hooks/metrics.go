package gitlab_hooks

import "github.com/prometheus/client_golang/prometheus"

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
	gitlabWebHookFailed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "tfbuddy_gitlab_webhook_failed",
			Help: "Count of all GitLab WebHook that could not publish to stream",
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
	r.MustRegister(gitlabWebHookIgnored)

	r.MustRegister(gitlabHookReadFromStream)
}
