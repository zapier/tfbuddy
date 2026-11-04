package jsqueue

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"

	"github.com/zapier/tfbuddy/pkg/utils"
)

const (
	testStream  = "TEST"
	testSubject = "TEST.events"
	testQueue   = "test_worker"
)

type testMsg struct {
	ID int `json:"id"`
}

func (m *testMsg) DecodeEventData(b []byte) error {
	return json.Unmarshal(b, m)
}

// startJetStream runs an in-process JetStream server with one work-queue
// stream, shaped like TFBuddy's HOOKS stream.
func startJetStream(t *testing.T) nats.JetStreamContext {
	t.Helper()
	opts := natstest.DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = t.TempDir()
	srv := natstest.RunServer(&opts)
	t.Cleanup(srv.Shutdown)

	addr, ok := srv.Addr().(*net.TCPAddr)
	require.True(t, ok, "unexpected server address type %T", srv.Addr())
	nc, err := nats.Connect(fmt.Sprintf("nats://127.0.0.1:%d", addr.Port))
	require.NoError(t, err)
	t.Cleanup(nc.Close)

	js, err := nc.JetStream()
	require.NoError(t, err)
	_, err = js.AddStream(&nats.StreamConfig{
		Name:      testStream,
		Subjects:  []string{testStream + ".>"},
		Retention: nats.WorkQueuePolicy,
		Storage:   nats.MemoryStorage,
	})
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

func subscribe(t *testing.T, js nats.JetStreamContext, opts Options, fn Handler[testMsg]) {
	t.Helper()
	sub, err := QueueSubscribe[testMsg](js, testStream, testSubject, testQueue, opts, fn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Unsubscribe() })
}

func requireStreamEmpty(t *testing.T, js nats.JetStreamContext) {
	t.Helper()
	require.Eventually(t, func() bool {
		info, err := js.StreamInfo(testStream)
		return err == nil && info.State.Msgs == 0
	}, 2*time.Second, 20*time.Millisecond, "message was left in the stream")
}

// The bug this package fixes: a consumer that acks only after its handler
// returns gets a slow message redelivered once AckWait passes.
func TestWithoutHeartbeatASlowHandlerIsRedelivered(t *testing.T) {
	js := startJetStream(t)
	var calls atomic.Int32
	sub, err := js.QueueSubscribe(testSubject, testQueue, func(msg *nats.Msg) {
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
	js := startJetStream(t)
	var calls atomic.Int32
	done := make(chan struct{})
	subscribe(t, js, Options{AckWait: 500 * time.Millisecond}, func(*testMsg) error {
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

func TestFailedMessageIsRedeliveredAfterNakDelay(t *testing.T) {
	js := startJetStream(t)
	var calls atomic.Int32
	subscribe(t, js, Options{AckWait: time.Second, NakDelay: 100 * time.Millisecond}, func(*testMsg) error {
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
	js := startJetStream(t)
	var calls atomic.Int32
	subscribe(t, js, Options{AckWait: time.Second, MaxDeliver: 3, NakDelay: 50 * time.Millisecond}, func(*testMsg) error {
		calls.Add(1)
		return errors.New("always fails")
	})

	publish(t, js, 1)

	require.Eventually(t, func() bool { return calls.Load() == 3 }, 3*time.Second, 20*time.Millisecond)
	requireStreamEmpty(t, js)
	time.Sleep(300 * time.Millisecond)
	require.Equal(t, int32(3), calls.Load())
}

func TestPermanentErrorIsNotRetried(t *testing.T) {
	js := startJetStream(t)
	var calls atomic.Int32
	subscribe(t, js, Options{AckWait: time.Second, NakDelay: 50 * time.Millisecond}, func(*testMsg) error {
		calls.Add(1)
		return fmt.Errorf("bad request %w", utils.ErrPermanent)
	})

	publish(t, js, 1)

	requireStreamEmpty(t, js)
	time.Sleep(300 * time.Millisecond)
	require.Equal(t, int32(1), calls.Load())
}

func TestPanickingHandlerDropsTheMessageAndKeepsConsuming(t *testing.T) {
	js := startJetStream(t)
	var seen []int
	handled := make(chan struct{}, 2)
	subscribe(t, js, Options{AckWait: time.Second, NakDelay: 50 * time.Millisecond}, func(m *testMsg) error {
		seen = append(seen, m.ID)
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
	require.Equal(t, []int{1, 2}, seen)
}

func TestUndecodableMessageIsDropped(t *testing.T) {
	js := startJetStream(t)
	var calls atomic.Int32
	subscribe(t, js, Options{}, func(*testMsg) error {
		calls.Add(1)
		return nil
	})

	_, err := js.Publish(testSubject, []byte("not json"))
	require.NoError(t, err)

	requireStreamEmpty(t, js)
	require.Equal(t, int32(0), calls.Load())
}

func TestHeartbeatStopsAfterMaxInProgress(t *testing.T) {
	js := startJetStream(t)
	var calls atomic.Int32
	subscribe(t, js, Options{AckWait: 300 * time.Millisecond, MaxInProgress: 400 * time.Millisecond}, func(*testMsg) error {
		if calls.Add(1) == 1 {
			time.Sleep(1500 * time.Millisecond)
		}
		return nil
	})

	publish(t, js, 1)

	require.Eventually(t, func() bool { return calls.Load() >= 2 }, 5*time.Second, 20*time.Millisecond,
		"expected a handler running past MaxInProgress to lose the message")
}

// The HOOKS consumers already exist in production with JetStream's defaults.
// nats.go refuses to bind to them with a different AckWait, so QueueSubscribe
// has to update them first.
func TestExistingConsumerIsUpdated(t *testing.T) {
	js := startJetStream(t)
	_, err := js.AddConsumer(testStream, &nats.ConsumerConfig{
		Durable:        testQueue,
		DeliverSubject: nats.NewInbox(),
		DeliverGroup:   testQueue,
		AckPolicy:      nats.AckExplicitPolicy,
		FilterSubject:  testSubject,
	})
	require.NoError(t, err)

	_, err = js.QueueSubscribe(testSubject, testQueue, func(*nats.Msg) {}, nats.AckWait(2*time.Minute))
	require.ErrorContains(t, err, "ack wait")

	handled := make(chan int, 1)
	subscribe(t, js, Options{AckWait: 2 * time.Minute, MaxDeliver: 4}, func(m *testMsg) error {
		handled <- m.ID
		return nil
	})

	info, err := js.ConsumerInfo(testStream, testQueue)
	require.NoError(t, err)
	require.Equal(t, 2*time.Minute, info.Config.AckWait)
	require.Equal(t, 4, info.Config.MaxDeliver)

	publish(t, js, 7)
	select {
	case id := <-handled:
		require.Equal(t, 7, id)
	case <-time.After(3 * time.Second):
		t.Fatal("updated consumer did not deliver")
	}
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
