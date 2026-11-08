package jsqueue

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	"github.com/zapier/tfbuddy/pkg/utils"
)

const (
	testStream  = "TEST"
	testSubject = "TEST.events"
	testDurable = "test_pull_worker"
	testLegacy  = "test_worker"
)

var testQueue = Queue{Stream: testStream, Subject: testSubject, Durable: testDurable, LegacyPushDurable: testLegacy}

type testMsg struct {
	ID int `json:"id"`
}

func (m *testMsg) DecodeEventData(b []byte) error {
	return json.Unmarshal(b, m)
}

// startJetStream runs an in-process JetStream server with one work-queue
// stream, shaped like TFBuddy's HOOKS stream. The legacy client it returns is
// used to set the stream up and publish, as TFBuddy does.
func startJetStream(t *testing.T) (*server.Server, nats.JetStreamContext) {
	t.Helper()
	opts := natstest.DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = t.TempDir()
	srv := natstest.RunServer(&opts)
	t.Cleanup(srv.Shutdown)

	js, err := connect(t, srv).JetStream()
	require.NoError(t, err)
	_, err = js.AddStream(&nats.StreamConfig{
		Name:      testStream,
		Subjects:  []string{testStream + ".>"},
		Retention: nats.WorkQueuePolicy,
		Storage:   nats.MemoryStorage,
	})
	require.NoError(t, err)
	return srv, js
}

func connect(t *testing.T, srv *server.Server) *nats.Conn {
	t.Helper()
	addr, ok := srv.Addr().(*net.TCPAddr)
	require.True(t, ok, "unexpected server address type %T", srv.Addr())
	nc, err := nats.Connect(fmt.Sprintf("nats://127.0.0.1:%d", addr.Port))
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	return nc
}

// replica opens a client connection of its own, standing in for one TFBuddy
// replica.
func replica(t *testing.T, srv *server.Server) jetstream.JetStream {
	t.Helper()
	js, err := jetstream.New(connect(t, srv))
	require.NoError(t, err)
	return js
}

func publish(t *testing.T, js nats.JetStreamContext, id int) {
	t.Helper()
	b, err := json.Marshal(testMsg{ID: id})
	require.NoError(t, err)
	_, err = js.Publish(testSubject, b)
	require.NoError(t, err)
}

func consume(t *testing.T, js jetstream.JetStream, opts Options, fn Handler[testMsg]) {
	t.Helper()
	cc, err := Consume[testMsg](js, testQueue, opts, fn)
	require.NoError(t, err)
	t.Cleanup(cc.Stop)
}

func requireStreamEmpty(t *testing.T, js nats.JetStreamContext) {
	t.Helper()
	require.Eventually(t, func() bool {
		info, err := js.StreamInfo(testStream)
		return err == nil && info.State.Msgs == 0
	}, 5*time.Second, 20*time.Millisecond, "message was left in the stream")
}

// The bug this package fixes: a consumer that acks only after its handler
// returns gets a slow message redelivered once AckWait passes.
func TestWithoutHeartbeatASlowHandlerIsRedelivered(t *testing.T) {
	_, js := startJetStream(t)
	var calls atomic.Int32
	sub, err := js.QueueSubscribe(testSubject, testLegacy, func(msg *nats.Msg) {
		if calls.Add(1) == 1 {
			time.Sleep(1500 * time.Millisecond)
		}
	}, nats.AckWait(500*time.Millisecond))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	publish(t, js, 1)

	require.Eventually(t, func() bool { return calls.Load() >= 2 }, 5*time.Second, 20*time.Millisecond,
		"expected JetStream to redeliver the message while the first handler was still running")
}

func TestSlowHandlerIsDeliveredOnce(t *testing.T) {
	srv, js := startJetStream(t)
	var calls atomic.Int32
	done := make(chan struct{})
	consume(t, replica(t, srv), Options{AckWait: 500 * time.Millisecond}, func(*testMsg) error {
		calls.Add(1)
		time.Sleep(1500 * time.Millisecond) // three AckWaits
		close(done)
		return nil
	})

	publish(t, js, 1)
	<-done
	time.Sleep(time.Second) // past another AckWait, so a missed heartbeat would show

	require.Equal(t, int32(1), calls.Load())
	requireStreamEmpty(t, js)
}

// A burst of merge requests: many events arrive at once and three replicas
// share them. Waiting events must not time out and be handled twice.
func TestBurstAcrossReplicasHandlesEachEventOnce(t *testing.T) {
	srv, js := startJetStream(t)
	const events, replicas = 30, 3

	var mu sync.Mutex
	seen := map[int]int{}
	byReplica := make([]int, replicas)
	for r := 0; r < replicas; r++ {
		consume(t, replica(t, srv), Options{AckWait: 500 * time.Millisecond}, func(m *testMsg) error {
			time.Sleep(200 * time.Millisecond)
			mu.Lock()
			seen[m.ID]++
			byReplica[r]++
			mu.Unlock()
			return nil
		})
	}

	// The backlog takes about 2s to drain, four times AckWait.
	for i := 1; i <= events; i++ {
		publish(t, js, i)
	}

	requireStreamEmpty(t, js)
	time.Sleep(time.Second) // a late redelivery would show here
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, seen, events)
	for id, n := range seen {
		require.Equal(t, 1, n, "event %d was handled %d times", id, n)
	}
	for r, n := range byReplica {
		require.Positive(t, n, "replica %d handled nothing", r)
	}
}

func TestFailedMessageIsRedeliveredAfterNakDelay(t *testing.T) {
	srv, js := startJetStream(t)
	var calls atomic.Int32
	consume(t, replica(t, srv), Options{AckWait: time.Second, NakDelay: 100 * time.Millisecond}, func(*testMsg) error {
		if calls.Add(1) == 1 {
			return errors.New("transient")
		}
		return nil
	})

	publish(t, js, 1)

	require.Eventually(t, func() bool { return calls.Load() == 2 }, 3*time.Second, 20*time.Millisecond)
	requireStreamEmpty(t, js)
	require.Equal(t, int32(2), calls.Load())
}

func TestFailingMessageStopsAfterMaxDeliver(t *testing.T) {
	srv, js := startJetStream(t)
	var calls atomic.Int32
	consume(t, replica(t, srv), Options{AckWait: time.Second, MaxDeliver: 3, NakDelay: 50 * time.Millisecond}, func(*testMsg) error {
		calls.Add(1)
		return errors.New("always fails")
	})

	publish(t, js, 1)

	require.Eventually(t, func() bool { return calls.Load() == 3 }, 5*time.Second, 20*time.Millisecond)
	requireStreamEmpty(t, js)
	time.Sleep(300 * time.Millisecond)
	require.Equal(t, int32(3), calls.Load())
}

func TestPermanentErrorIsNotRetried(t *testing.T) {
	srv, js := startJetStream(t)
	var calls atomic.Int32
	consume(t, replica(t, srv), Options{AckWait: time.Second, NakDelay: 50 * time.Millisecond}, func(*testMsg) error {
		calls.Add(1)
		return fmt.Errorf("bad request %w", utils.ErrPermanent)
	})

	publish(t, js, 1)

	requireStreamEmpty(t, js)
	time.Sleep(300 * time.Millisecond)
	require.Equal(t, int32(1), calls.Load())
}

func TestPanickingHandlerDropsTheMessageAndKeepsConsuming(t *testing.T) {
	srv, js := startJetStream(t)
	var mu sync.Mutex
	var seen []int
	handled := make(chan struct{}, 2)
	consume(t, replica(t, srv), Options{AckWait: time.Second, NakDelay: 50 * time.Millisecond}, func(m *testMsg) error {
		mu.Lock()
		seen = append(seen, m.ID)
		mu.Unlock()
		handled <- struct{}{}
		if m.ID == 1 {
			panic("boom")
		}
		return nil
	})

	publish(t, js, 1)
	publish(t, js, 2)
	<-handled
	<-handled

	requireStreamEmpty(t, js)
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []int{1, 2}, seen)
}

func TestUndecodableMessageIsDropped(t *testing.T) {
	srv, js := startJetStream(t)
	var calls atomic.Int32
	consume(t, replica(t, srv), Options{}, func(*testMsg) error {
		calls.Add(1)
		return nil
	})

	_, err := js.Publish(testSubject, []byte("not json"))
	require.NoError(t, err)

	requireStreamEmpty(t, js)
	require.Equal(t, int32(0), calls.Load())
}

func TestHeartbeatStopsAfterMaxInProgress(t *testing.T) {
	srv, js := startJetStream(t)
	var calls atomic.Int32
	opts := Options{AckWait: 300 * time.Millisecond, MaxInProgress: 400 * time.Millisecond}
	handler := func(*testMsg) error {
		if calls.Add(1) == 1 {
			time.Sleep(1500 * time.Millisecond)
		}
		return nil
	}
	consume(t, replica(t, srv), opts, handler)
	consume(t, replica(t, srv), opts, handler)

	publish(t, js, 1)

	require.Eventually(t, func() bool { return calls.Load() >= 2 }, 5*time.Second, 20*time.Millisecond,
		"expected a handler running past MaxInProgress to lose the message to the other replica")
}

// Production runs push consumers created by TFBuddy v0.8.3. Starting a
// release with Consume deletes them, keeps their unacked messages, and stops
// a v0.8.3 replica from creating them again.
func TestLegacyPushConsumerIsReplaced(t *testing.T) {
	srv, js := startJetStream(t)
	_, err := js.AddConsumer(testStream, &nats.ConsumerConfig{
		Durable:        testLegacy,
		DeliverSubject: nats.NewInbox(),
		DeliverGroup:   testLegacy,
		AckPolicy:      nats.AckExplicitPolicy,
		FilterSubject:  testSubject,
	})
	require.NoError(t, err)
	publish(t, js, 7) // waiting for the legacy consumer, never acked

	handled := make(chan int, 1)
	consume(t, replica(t, srv), Options{AckWait: 2 * time.Minute, MaxDeliver: 4}, func(m *testMsg) error {
		handled <- m.ID
		return nil
	})

	select {
	case id := <-handled:
		require.Equal(t, 7, id)
	case <-time.After(3 * time.Second):
		t.Fatal("the legacy consumer's message was not handled")
	}

	_, err = js.ConsumerInfo(testStream, testLegacy)
	require.ErrorIs(t, err, nats.ErrConsumerNotFound)
	info, err := js.ConsumerInfo(testStream, testDurable)
	require.NoError(t, err)
	require.Empty(t, info.Config.DeliverSubject, "expected a pull consumer")
	require.Equal(t, 2*time.Minute, info.Config.AckWait)
	require.Equal(t, 4, info.Config.MaxDeliver)

	// A v0.8.3 replica restarting mid-rollout cannot take the subject back.
	_, err = js.QueueSubscribe(testSubject, testLegacy, func(*nats.Msg) {})
	require.Error(t, err)
}

func TestExistingPullConsumerIsUpdated(t *testing.T) {
	srv, js := startJetStream(t)
	_, err := js.AddConsumer(testStream, &nats.ConsumerConfig{
		Durable:       testDurable,
		AckPolicy:     nats.AckExplicitPolicy,
		FilterSubject: testSubject,
	})
	require.NoError(t, err)

	consume(t, replica(t, srv), Options{AckWait: 2 * time.Minute, MaxDeliver: 4}, func(*testMsg) error { return nil })

	info, err := js.ConsumerInfo(testStream, testDurable)
	require.NoError(t, err)
	require.Equal(t, 2*time.Minute, info.Config.AckWait)
	require.Equal(t, 4, info.Config.MaxDeliver)
}

// Replicas start together after a deploy and race to set up the consumer.
func TestReplicasStartingTogetherShareOneConsumer(t *testing.T) {
	srv, js := startJetStream(t)
	_, err := js.AddConsumer(testStream, &nats.ConsumerConfig{
		Durable:        testLegacy,
		DeliverSubject: nats.NewInbox(),
		DeliverGroup:   testLegacy,
		AckPolicy:      nats.AckExplicitPolicy,
		FilterSubject:  testSubject,
	})
	require.NoError(t, err)

	const replicas = 3
	jss := make([]jetstream.JetStream, replicas)
	for r := range jss {
		jss[r] = replica(t, srv)
	}
	var calls atomic.Int32
	errs := make(chan error, replicas)
	var wg sync.WaitGroup
	for r := 0; r < replicas; r++ {
		wg.Add(1)
		go func(js jetstream.JetStream) {
			defer wg.Done()
			cc, err := Consume[testMsg](js, testQueue, Options{}, func(*testMsg) error {
				calls.Add(1)
				return nil
			})
			if err == nil {
				t.Cleanup(cc.Stop)
			}
			errs <- err
		}(jss[r])
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	publish(t, js, 1)
	requireStreamEmpty(t, js)
	require.Equal(t, int32(1), calls.Load())
}

func TestOptionsDefaults(t *testing.T) {
	o := Options{}.withDefaults()
	require.Equal(t, DefaultAckWait, o.AckWait)
	require.Equal(t, DefaultMaxDeliver, o.MaxDeliver)
	require.Equal(t, DefaultNakDelay, o.NakDelay)
	require.Equal(t, DefaultMaxInProgress, o.MaxInProgress)
	require.Equal(t, DefaultAckWait/3, o.Heartbeat)

	// A heartbeat no shorter than AckWait would let the message lapse.
	o = Options{AckWait: time.Second, Heartbeat: 2 * time.Second}.withDefaults()
	require.Equal(t, time.Second/3, o.Heartbeat)
}

func waitSamples(t *testing.T, queue string) (count uint64, sum float64) {
	t.Helper()
	m := &dto.Metric{}
	require.NoError(t, messageWait.WithLabelValues(queue).(prometheus.Metric).Write(m))
	return m.GetHistogram().GetSampleCount(), m.GetHistogram().GetSampleSum()
}

// The wait histogram is the queue-freshness SLI: time from publish to the
// first pick-up. A redelivery must not add a second sample.
func TestWaitIsObservedOnFirstDeliveryOnly(t *testing.T) {
	srv, js := startJetStream(t)
	countBefore, sumBefore := waitSamples(t, testDurable)

	publish(t, js, 1)
	time.Sleep(time.Second) // no consumer yet, so the message waits in the stream

	var calls atomic.Int32
	consume(t, replica(t, srv), Options{AckWait: time.Second, NakDelay: 100 * time.Millisecond}, func(*testMsg) error {
		if calls.Add(1) == 1 {
			return errors.New("transient")
		}
		return nil
	})
	require.Eventually(t, func() bool { return calls.Load() == 2 }, 3*time.Second, 20*time.Millisecond)
	requireStreamEmpty(t, js)

	count, sum := waitSamples(t, testDurable)
	require.Equal(t, countBefore+1, count, "only the first delivery is observed")
	require.GreaterOrEqual(t, sum-sumBefore, 1.0, "the wait covers the time before the consumer started")
}

func TestObserveWaitTreatsAFutureTimestampAsNoWait(t *testing.T) {
	const queue = "clock_skew_test"
	countBefore, sumBefore := waitSamples(t, queue)
	now := time.Now()

	observeWait(queue, now.Add(time.Minute), now)

	count, sum := waitSamples(t, queue)
	require.Equal(t, countBefore+1, count)
	require.Equal(t, sumBefore, sum)
}

// rate() ignores the value a series starts with, so a queue's series must
// exist at zero as soon as it starts consuming.
func TestConsumeCreatesTheQueueSeries(t *testing.T) {
	srv, _ := startJetStream(t)
	const queue = "series_test_pull_worker"
	q := testQueue
	q.Durable = queue
	q.LegacyPushDurable = ""
	cc, err := Consume[testMsg](replica(t, srv), q, Options{}, func(*testMsg) error { return nil })
	require.NoError(t, err)
	t.Cleanup(cc.Stop)

	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	found := map[string]int{}
	for _, f := range families {
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "queue" && l.GetValue() == queue {
					found[f.GetName()]++
				}
			}
		}
	}
	require.Equal(t, 1, found["tfbuddy_jetstream_message_wait_seconds"])
	require.Equal(t, 1, found["tfbuddy_jetstream_messages_redelivered"])
	require.Equal(t, 3, found["tfbuddy_jetstream_messages_settled"])
}
