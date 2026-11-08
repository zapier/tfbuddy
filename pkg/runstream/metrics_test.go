package runstream

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/zapier/tfbuddy/pkg/tfc_api"
)

func finalRunEvent(runID, status string) *TFRunEvent {
	return &TFRunEvent{
		RunID:     runID,
		NewStatus: status,
		Metadata:  &TFRunMetadata{RunID: runID, Action: PlanAction, VcsProvider: "gitlab"},
	}
}

func finishedCount(status string) float64 {
	return testutil.ToFloat64(runsFinished.WithLabelValues("gitlab", PlanAction, status))
}

func completionStream(t *testing.T) *Stream {
	t.Helper()
	_, url := startTestNATS(t)
	nc := testConnect(t, url)
	t.Cleanup(nc.Close)
	js := testGetJetstreamContext(t, nc)
	kv, err := configureRunCompletionKVStore(js)
	require.NoError(t, err)
	return &Stream{js: js, completionKV: kv}
}

// JetStream delivers a final event again when its ack is lost. The second
// delivery, on this replica or another, must not count the run again.
func TestRecordRunFinishedCountsARedeliveredRunOnce(t *testing.T) {
	replicaA := completionStream(t)
	// Another replica binds to the same bucket.
	kv, err := configureRunCompletionKVStore(replicaA.js)
	require.NoError(t, err)
	replicaB := &Stream{js: replicaA.js, completionKV: kv}

	before := finishedCount("errored")

	replicaA.RecordRunFinished("gitlab", finalRunEvent("run-redelivered", "errored"))
	replicaA.RecordRunFinished("gitlab", finalRunEvent("run-redelivered", "errored"))
	replicaB.RecordRunFinished("gitlab", finalRunEvent("run-redelivered", "errored"))
	require.Equal(t, before+1, finishedCount("errored"), "a redelivered final event must not count the run again")

	replicaB.RecordRunFinished("gitlab", finalRunEvent("run-other", "errored"))
	require.Equal(t, before+2, finishedCount("errored"), "another run counts")
}

// Without the completion bucket the run is still counted.
func TestRecordRunFinishedCountsWhenTheRecordCannotBeWritten(t *testing.T) {
	s := &Stream{}
	before := finishedCount("applied")

	s.RecordRunFinished("gitlab", finalRunEvent("run-no-kv", "applied"))

	require.Equal(t, before+1, finishedCount("applied"))
}

// The completion record must outlive every message RUN_EVENTS can still
// redeliver.
func TestRunCompletionRecordsOutliveRunEvents(t *testing.T) {
	require.Greater(t, runCompletionTTL, runEventsMaxAge)

	s := completionStream(t)
	status, err := s.completionKV.Status()
	require.NoError(t, err)
	require.Equal(t, runCompletionTTL, status.TTL())
}

// increase() ignores the value a series starts with, so every label set the
// completion SLI uses must exist at zero before the first run.
func TestCompletionSeriesExistFromStartup(t *testing.T) {
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	series := map[string]int{}
	for _, f := range families {
		series[f.GetName()] = len(f.GetMetric())
	}
	combos := len(metricProviders) * len(metricRunTypes)
	require.GreaterOrEqual(t, series["tfbuddy_tfc_runs_created"], combos)
	require.GreaterOrEqual(t, series["tfbuddy_tfc_runs_finished"], combos*len(tfc_api.FinalRunStatuses()))
}
