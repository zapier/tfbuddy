package runstream

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
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

// JetStream delivers a final event again when its ack is lost. The second
// delivery, on this replica or another, must not count the run again.
func TestRecordRunFinishedCountsARedeliveredRunOnce(t *testing.T) {
	_, url := startTestNATS(t)
	nc := testConnect(t, url)
	defer nc.Close()
	js := testGetJetstreamContext(t, nc)

	kv, err := configureWorkspaceDispatchKVStore(js)
	require.NoError(t, err)
	replicaA := &Stream{js: js, dispatchKV: kv}
	kv2, err := configureWorkspaceDispatchKVStore(js)
	require.NoError(t, err)
	replicaB := &Stream{js: js, dispatchKV: kv2}

	before := finishedCount("errored")

	replicaA.RecordRunFinished("gitlab", finalRunEvent("run-redelivered", "errored"))
	replicaA.RecordRunFinished("gitlab", finalRunEvent("run-redelivered", "errored"))
	replicaB.RecordRunFinished("gitlab", finalRunEvent("run-redelivered", "errored"))
	require.Equal(t, before+1, finishedCount("errored"), "a redelivered final event must not count the run again")

	replicaB.RecordRunFinished("gitlab", finalRunEvent("run-other", "errored"))
	require.Equal(t, before+2, finishedCount("errored"), "another run counts")
}

// Without the dedup bucket the run is still counted: its status was reported.
func TestRecordRunFinishedCountsWhenTheClaimCannotBeRecorded(t *testing.T) {
	s := &Stream{}
	before := finishedCount("applied")

	s.RecordRunFinished("gitlab", finalRunEvent("run-no-kv", "applied"))

	require.Equal(t, before+1, finishedCount("applied"))
}
