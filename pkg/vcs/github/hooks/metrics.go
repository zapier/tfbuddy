package hooks

import "github.com/prometheus/client_golang/prometheus"

var (
	githubWebHookReceived = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "tfbuddy_github_webhook_received",
		Help: "Count of all GitHub webhooks received",
	})
	commonLabels = []string{
		"eventType",
		"repository",
	}
	githubWebHookSuccess = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "tfbuddy_github_webhook_success",
			Help: "Count of all GitHub WebHook that were published to stream",
		},
		commonLabels,
	)
	githubWebHookFailed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "tfbuddy_github_webhook_failed",
			Help: "Count of all GitHub WebHook that could not publish to stream",
		},
		commonLabels,
	)
	githubWebHookIgnored = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "tfbuddy_github_webhook_ignored",
			Help: "Count of all GitHub WebHook that were ignored",
		},
		append(commonLabels, "reason"),
	)
)

func init() {
	r := prometheus.DefaultRegisterer
	r.MustRegister(githubWebHookReceived)
	r.MustRegister(githubWebHookSuccess)
	r.MustRegister(githubWebHookFailed)
	r.MustRegister(githubWebHookIgnored)
}
