package runstream

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"
)

func testJetStream(t *testing.T, nc *nats.Conn) jetstream.JetStream {
	t.Helper()
	jsc, err := jetstream.New(nc)
	require.NoError(t, err)
	return jsc
}

// newTestReplica connects one TFBuddy replica's Stream to the NATS server at url.
func newTestReplica(t *testing.T, url string) *Stream {
	t.Helper()
	nc := testConnect(t, url)
	t.Cleanup(nc.Close)
	return NewStream(testGetJetstreamContext(t, nc), testJetStream(t, nc), testDedupWindow).(*Stream)
}

func publishTestRunEvent(t *testing.T, s *Stream, runID string) {
	t.Helper()
	require.NoError(t, s.AddRunMeta(&TFRunMetadata{RunID: runID, Workspace: "ws", Action: PlanAction, VcsProvider: "gitlab"}))
	require.NoError(t, s.PublishTFRunEvent(context.Background(), &TFRunEvent{RunID: runID, NewStatus: "planning"}))
}

// addLegacyPushConsumer creates the push consumer older releases used.
func addLegacyPushConsumer(t *testing.T, s *Stream, stream, durable, subject string) {
	t.Helper()
	_, err := s.js.AddConsumer(stream, &nats.ConsumerConfig{
		Durable:        durable,
		DeliverSubject: nats.NewInbox(),
		DeliverGroup:   durable,
		FilterSubject:  subject,
		AckPolicy:      nats.AckExplicitPolicy,
	})
	require.NoError(t, err)
}

func consumerNames(t *testing.T, s *Stream, stream string) []string {
	t.Helper()
	var names []string
	for name := range s.js.ConsumerNames(stream) {
		names = append(names, name)
	}
	return names
}

func TestSubscribeTFRunEventsReplacesTheLegacyPushConsumer(t *testing.T) {
	_, url := startTestNATS(t)
	s := newTestReplica(t, url)
	addLegacyPushConsumer(t, s, RunEventsStreamName, "gitlab", RunEventsStreamName+".gitlab")
	// Published while only the legacy consumer exists, as during a rollout.
	publishTestRunEvent(t, s, "run-1")

	got := make(chan RunEvent, 1)
	closer, err := s.SubscribeTFRunEvents("gitlab", func(re RunEvent) bool {
		got <- re
		return true
	})
	require.NoError(t, err)
	t.Cleanup(closer)

	select {
	case re := <-got:
		require.Equal(t, "run-1", re.GetRunID())
		require.Equal(t, "ws", re.GetMetadata().GetWorkspace(), "the event is enriched with its run metadata")
	case <-time.After(10 * time.Second):
		t.Fatal("the event published to the legacy consumer was not handled")
	}
	require.ElementsMatch(t, []string{"gitlab_run_event_pull_worker"}, consumerNames(t, s, RunEventsStreamName))
}

// A burst of run events across several replicas is handled exactly once,
// even though each handler takes a while.
func TestSubscribeTFRunEventsHandlesEachEventOnceAcrossReplicas(t *testing.T) {
	_, url := startTestNATS(t)
	replicas := []*Stream{newTestReplica(t, url), newTestReplica(t, url), newTestReplica(t, url)}

	var mu sync.Mutex
	handled := map[string]int{}
	for _, s := range replicas {
		closer, err := s.SubscribeTFRunEvents("gitlab", func(re RunEvent) bool {
			time.Sleep(50 * time.Millisecond)
			mu.Lock()
			handled[re.GetRunID()]++
			mu.Unlock()
			return true
		})
		require.NoError(t, err)
		t.Cleanup(closer)
	}

	const events = 30
	for i := 0; i < events; i++ {
		publishTestRunEvent(t, replicas[0], fmt.Sprintf("run-%d", i))
	}

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(handled) == events
	}, 20*time.Second, 50*time.Millisecond)
	time.Sleep(time.Second) // leave time for any duplicate delivery to show up
	mu.Lock()
	defer mu.Unlock()
	for runID, n := range handled {
		require.Equal(t, 1, n, "%s handled %d times", runID, n)
	}
}

func TestSubscribeTFRunPollingTasksReplacesTheLegacyPushConsumer(t *testing.T) {
	_, url := startTestNATS(t)
	s := newTestReplica(t, url)
	addLegacyPushConsumer(t, s, RunPollingStreamNameV0, "polling", RunPollingStreamNameV0+".*")

	polled := make(chan string, 1)
	closer, err := s.SubscribeTFRunPollingTasks(func(task RunPollingTask) bool {
		polled <- task.GetRunID()
		return true
	})
	require.NoError(t, err)
	t.Cleanup(closer)
	require.ElementsMatch(t, []string{"run_polling_pull_worker"}, consumerNames(t, s, RunPollingStreamNameV0))

	task := s.NewTFRunPollingTask(&TFRunMetadata{RunID: "run-1"}, TaskPollingDelayMinimum)
	require.NoError(t, task.Schedule(context.Background()))
	select {
	case runID := <-polled:
		require.Equal(t, "run-1", runID)
	case <-time.After(10 * time.Second):
		t.Fatal("the polling task was not dispatched to the pull consumer")
	}
}

// A poll that fails, for example while the TFC API is unavailable, goes back
// to the dispatcher for its usual delay instead of being redelivered at once.
func TestSubscribeTFRunPollingTasksReschedulesAFailedPoll(t *testing.T) {
	_, url := startTestNATS(t)
	s := newTestReplica(t, url)

	polls := make(chan time.Time, 10)
	closer, err := s.SubscribeTFRunPollingTasks(func(task RunPollingTask) bool {
		polls <- time.Now()
		return false
	})
	require.NoError(t, err)
	t.Cleanup(closer)

	task := s.NewTFRunPollingTask(&TFRunMetadata{RunID: "run-1"}, TaskPollingDelayMinimum)
	require.NoError(t, task.Schedule(context.Background()))

	var failedAt time.Time
	select {
	case failedAt = <-polls:
	case <-time.After(10 * time.Second):
		t.Fatal("the polling task was not dispatched")
	}

	// The task is back in the dispatcher's hands, due after the usual delay.
	require.Eventually(t, func() bool {
		entry, err := s.pollingKV.Get("run-1")
		if err != nil {
			return false
		}
		stored, err := s.decodeTFRunPollingTaskKVEntry(entry)
		return err == nil && !stored.Processing && stored.NextPoll.After(failedAt.Add(TaskPollingDelayDefault/2))
	}, 5*time.Second, 50*time.Millisecond)

	// The message itself was acked, not redelivered.
	cons, err := s.jsc.Consumer(context.Background(), RunPollingStreamNameV0, pollingQueue.Durable)
	require.NoError(t, err)
	info, err := cons.Info(context.Background())
	require.NoError(t, err)
	require.Zero(t, info.NumAckPending)
	require.Zero(t, info.NumRedelivered)
	select {
	case <-polls:
		t.Fatal("the failed poll was retried before the polling delay")
	case <-time.After(3 * time.Second):
	}
}
