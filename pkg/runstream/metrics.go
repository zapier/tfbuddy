package runstream

import "github.com/prometheus/client_golang/prometheus"

// runsFinished is the completion side of tfbuddy_tfc_runs_started: a run
// TFBuddy started is counted here once a run event worker has handled its
// final status and updated the merge request. Run events are deduplicated by
// run ID and status, and a worker counts only an event it acks, so each run
// is counted once. A run that is started but never counted here never got
// its final status reported.
var runsFinished = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "tfbuddy_tfc_runs_finished",
	Help: "Count of TFC runs whose final status TFBuddy reported back to the merge request, by VCS provider, run type and final status",
}, []string{"provider", "runType", "status"})

func init() {
	prometheus.MustRegister(runsFinished)
}

// RecordRunFinished counts a run whose final status the provider's run event
// worker has handled. runType is the run metadata's action, the same value
// tfbuddy_tfc_runs_started uses.
func RecordRunFinished(provider, runType, status string) {
	runsFinished.WithLabelValues(provider, runType, status).Inc()
}
