package runstream

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
)

func TestPollingTaskDue(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name      string
		task      TFRunPollingTask
		due       bool
		wantStuck bool
	}{
		{"waiting, next poll due", TFRunPollingTask{NextPoll: now.Add(-time.Second)}, true, false},
		{"waiting, next poll later", TFRunPollingTask{NextPoll: now.Add(time.Second)}, false, false},
		{"dispatched a moment ago", TFRunPollingTask{Processing: true, LastUpdate: now.Add(-10 * time.Second)}, false, false},
		{"dispatched and never handled", TFRunPollingTask{Processing: true, LastUpdate: now.Add(-pollingTaskStuckAfter - time.Second)}, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			due, stuck := pollingTaskDue(&tt.task, now)
			require.Equal(t, tt.due, due)
			require.Equal(t, tt.wantStuck, stuck)
		})
	}
}

// newTestPollingStream runs the polling dispatcher against a test NATS
// server. It returns a channel of the run IDs the dispatcher publishes,
// watched with a plain subscription so the test does not depend on how the
// polling queue is consumed.
func newTestPollingStream(t *testing.T) (*Stream, <-chan string) {
	t.Helper()
	_, url := startTestNATS(t)
	nc := testConnect(t, url)
	t.Cleanup(nc.Close)
	js := testGetJetstreamContext(t, nc)
	configureTFRunPollingTaskStream(js)
	kv, err := configureRunPollingKVStore(js)
	require.NoError(t, err)

	published := make(chan string, 10)
	_, err = nc.Subscribe(RunPollingStreamNameV0+".*", func(msg *nats.Msg) {
		published <- strings.TrimPrefix(msg.Subject, RunPollingStreamNameV0+".")
	})
	require.NoError(t, err)

	s := &Stream{js: js, pollingKV: kv}
	s.startPollingTaskDispatcher()
	return s, published
}

// putPollingTask stores a task in the polling KV as it was left by an
// earlier dispatch.
func putPollingTask(t *testing.T, s *Stream, task *TFRunPollingTask) {
	t.Helper()
	b, err := encodeTFRunPollingTask(context.Background(), task)
	require.NoError(t, err)
	_, err = s.pollingKV.Create(task.GetRunID(), b)
	require.NoError(t, err)
}

// A task whose dispatch was lost, for example because the NATS server
// restarted, is dispatched again instead of staying claimed until its KV
// entry expires.
func TestPollingDispatcherSendsAStuckTaskAgain(t *testing.T) {
	s, polled := newTestPollingStream(t)
	putPollingTask(t, s, &TFRunPollingTask{
		RunMetadata: &TFRunMetadata{RunID: "run-1"},
		LastStatus:  "planning",
		NextPoll:    time.Now().Add(-pollingTaskStuckAfter),
		Processing:  true,
		LastUpdate:  time.Now().Add(-pollingTaskStuckAfter - time.Minute),
	})

	select {
	case runID := <-polled:
		require.Equal(t, "run-1", runID)
	case <-time.After(10 * time.Second):
		t.Fatal("the stuck polling task was not dispatched again")
	}
}

// A task dispatched a moment ago is left alone while it is being handled.
func TestPollingDispatcherLeavesAFreshlyDispatchedTaskAlone(t *testing.T) {
	s, polled := newTestPollingStream(t)
	putPollingTask(t, s, &TFRunPollingTask{
		RunMetadata: &TFRunMetadata{RunID: "run-1"},
		LastStatus:  "planning",
		NextPoll:    time.Now().Add(-time.Second),
		Processing:  true,
		LastUpdate:  time.Now(),
	})

	select {
	case <-polled:
		t.Fatal("a task that is being handled was dispatched a second time")
	case <-time.After(3 * time.Second):
	}
}

// When RUN_POLLING cannot store a dispatched task, the dispatcher releases
// its claim and sends the task again once the stream is back.
func TestPollingDispatcherRetriesAFailedPublish(t *testing.T) {
	s, _ := newTestPollingStream(t)
	require.NoError(t, s.js.DeleteStream(RunPollingStreamNameV0))

	task := s.NewTFRunPollingTask(&TFRunMetadata{RunID: "run-1"}, TaskPollingDelayMinimum).(*TFRunPollingTask)
	require.NoError(t, task.Schedule(context.Background()))
	created := task.Revision

	// The publish fails while the stream is gone, and the claim is released:
	// the dispatcher has claimed the task and released it again.
	require.Eventually(t, func() bool {
		entry, err := s.pollingKV.Get("run-1")
		if err != nil {
			return false
		}
		stored, err := s.decodeTFRunPollingTaskKVEntry(entry)
		return err == nil && entry.Revision() >= created+2 && !stored.Processing
	}, 10*time.Second, 50*time.Millisecond)

	// Once the stream is back, a later pass stores the task in it. The plain
	// subscription also saw the failed attempts, so check the stream itself.
	configureTFRunPollingTaskStream(s.js)
	require.Eventually(t, func() bool {
		info, err := s.js.StreamInfo(RunPollingStreamNameV0)
		return err == nil && info.State.Msgs > 0
	}, 10*time.Second, 50*time.Millisecond, "the task was not stored after the stream came back")
}
