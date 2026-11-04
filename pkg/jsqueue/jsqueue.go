// Package jsqueue runs JetStream work queues whose handlers can take longer
// than the consumer's AckWait.
//
// TFBuddy's hook handlers plan every workspace of a merge request before they
// return, which can take minutes. Two things used to turn that into repeated
// plans:
//
//   - A message whose handler outlived AckWait was redelivered to another
//     replica, which planned the whole merge request again.
//   - The old consumers were push consumers. JetStream pushed every waiting
//     message to the replicas at once, and a message's AckWait clock ran while
//     it sat in a busy replica's buffer, so a burst of merge requests timed out
//     and was redelivered before anyone had started on it.
//
// Consume fixes both. Each replica pulls one message, handles it, and only
// then pulls the next, so waiting messages stay in the stream with no clock
// running. While a handler runs, its message is kept in progress, so a live
// handler never loses it.
package jsqueue

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/zapier/tfbuddy/pkg/utils"
)

const (
	// DefaultAckWait is how long JetStream waits for an ack or a progress
	// signal before it redelivers. It only bounds how long a crashed worker
	// holds a message: a live handler extends it every heartbeat.
	DefaultAckWait = time.Minute
	// DefaultMaxDeliver caps the deliveries of one message, the first included.
	DefaultMaxDeliver = 5
	// DefaultNakDelay is how long JetStream waits before it redelivers a
	// message whose handler returned an error.
	DefaultNakDelay = 30 * time.Second
	// DefaultMaxInProgress is how long a handler may keep a message before the
	// heartbeat stops, so a hung handler cannot hold a message forever.
	DefaultMaxInProgress = 30 * time.Minute
	// DefaultFetchWait is how long one pull request waits for a message.
	DefaultFetchWait = 10 * time.Second

	maxFetchErrorDelay = 30 * time.Second
)

// Queue names a durable pull consumer on a stream.
type Queue struct {
	Stream  string
	Subject string
	// Durable names the pull consumer.
	Durable string
	// LegacyPushDurable names the push consumer this queue replaces. A push
	// consumer with this name is deleted before the pull consumer is created,
	// because a work-queue stream allows one consumer per subject. Messages it
	// had not acked stay in the stream and go to the pull consumer.
	LegacyPushDurable string
}

// Options tune a queue. Zero values take the defaults above.
type Options struct {
	AckWait       time.Duration
	MaxDeliver    int
	NakDelay      time.Duration
	MaxInProgress time.Duration
	FetchWait     time.Duration
	// Heartbeat is how often a running handler extends the ack deadline.
	// It defaults to a third of AckWait.
	Heartbeat time.Duration
}

func (o Options) withDefaults() Options {
	if o.AckWait <= 0 {
		o.AckWait = DefaultAckWait
	}
	if o.MaxDeliver <= 0 {
		o.MaxDeliver = DefaultMaxDeliver
	}
	if o.NakDelay <= 0 {
		o.NakDelay = DefaultNakDelay
	}
	if o.MaxInProgress <= 0 {
		o.MaxInProgress = DefaultMaxInProgress
	}
	if o.FetchWait <= 0 {
		o.FetchWait = DefaultFetchWait
	}
	if o.Heartbeat <= 0 || o.Heartbeat >= o.AckWait {
		o.Heartbeat = o.AckWait / 3
	}
	return o
}

// Event is a message type that can be decoded from a JetStream payload. The
// gongs message types used for publishing satisfy it.
type Event[T any] interface {
	*T
	DecodeEventData(b []byte) error
}

// Handler processes one decoded message. A nil return acks the message. An
// error wrapping utils.ErrPermanent terminates it. Any other error asks
// JetStream to redeliver it after Options.NakDelay, up to Options.MaxDeliver
// deliveries in all.
type Handler[T any] func(*T) error

const (
	outcomeAck  = "ack"
	outcomeNak  = "nak"
	outcomeTerm = "term"
)

var (
	messagesSettled = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tfbuddy_jetstream_messages_settled",
		Help: "JetStream queue messages settled by TFBuddy, by queue and outcome (ack, nak, term)",
	}, []string{"queue", "outcome"})
	messagesRedelivered = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tfbuddy_jetstream_messages_redelivered",
		Help: "JetStream queue messages received on a second or later delivery, by queue",
	}, []string{"queue"})
)

func init() {
	prometheus.MustRegister(messagesSettled, messagesRedelivered)
}

// Consumer is a running Consume loop.
type Consumer struct {
	sub      *nats.Subscription
	done     chan struct{}
	stopped  chan struct{}
	stopOnce sync.Once
}

// Stop ends the loop and waits for the message being handled, if any.
func (c *Consumer) Stop() {
	c.stopOnce.Do(func() {
		close(c.done)
		_ = c.sub.Unsubscribe()
	})
	<-c.stopped
}

// Consume handles q's messages with fn, one at a time: it pulls the next
// message only once fn has returned, so several replicas calling Consume
// share the queue and each takes the next message when it is free.
func Consume[T any, I Event[T]](js nats.JetStreamContext, q Queue, opts Options, fn Handler[T]) (*Consumer, error) {
	o := opts.withDefaults()
	if err := ensurePullConsumer(js, q, o); err != nil {
		return nil, err
	}
	sub, err := js.PullSubscribe(q.Subject, q.Durable, nats.Bind(q.Stream, q.Durable))
	if err != nil {
		return nil, fmt.Errorf("could not bind to consumer %s on stream %s: %w", q.Durable, q.Stream, err)
	}
	c := &Consumer{sub: sub, done: make(chan struct{}), stopped: make(chan struct{})}
	go c.run(func(msg *nats.Msg) { handle[T, I](msg, q.Durable, o, fn) }, q, o)
	return c, nil
}

func (c *Consumer) run(handleMsg func(*nats.Msg), q Queue, o Options) {
	defer close(c.stopped)
	logger := log.With().Str("stream", q.Stream).Str("queue", q.Durable).Logger()
	errDelay := time.Second
	for {
		select {
		case <-c.done:
			return
		default:
		}
		msgs, err := c.sub.Fetch(1, nats.MaxWait(o.FetchWait))
		switch {
		case err == nil:
			errDelay = time.Second
			for _, msg := range msgs {
				handleMsg(msg)
			}
		case errors.Is(err, nats.ErrTimeout):
			// No message within FetchWait: ask again.
		case errors.Is(err, nats.ErrBadSubscription), errors.Is(err, nats.ErrConnectionClosed):
			select {
			case <-c.done:
			default:
				logger.Error().Err(err).Msg("JetStream pull subscription closed; no longer consuming")
			}
			return
		default:
			logger.Warn().Err(err).Dur("retryIn", errDelay).Msg("could not fetch JetStream message")
			select {
			case <-c.done:
				return
			case <-time.After(errDelay):
			}
			errDelay = min(2*errDelay, maxFetchErrorDelay)
		}
	}
}

// ensurePullConsumer replaces q's legacy push consumer, if any, with a pull
// consumer configured from o. Replicas starting together may race here; every
// step tolerates another replica having done it first.
func ensurePullConsumer(js nats.JetStreamContext, q Queue, o Options) error {
	if q.LegacyPushDurable != "" {
		if err := deleteLegacyPushConsumer(js, q.Stream, q.LegacyPushDurable); err != nil {
			return err
		}
	}

	want := nats.ConsumerConfig{
		Durable:       q.Durable,
		FilterSubject: q.Subject,
		DeliverPolicy: nats.DeliverAllPolicy,
		AckPolicy:     nats.AckExplicitPolicy,
		AckWait:       o.AckWait,
		MaxDeliver:    o.MaxDeliver,
	}
	info, err := js.ConsumerInfo(q.Stream, q.Durable)
	if errors.Is(err, nats.ErrConsumerNotFound) {
		if _, err := js.AddConsumer(q.Stream, &want); err == nil {
			log.Info().Str("stream", q.Stream).Str("consumer", q.Durable).Msg("created JetStream pull consumer")
			return nil
		}
		// Another replica may have created it first.
		info, err = js.ConsumerInfo(q.Stream, q.Durable)
	}
	if err != nil {
		return fmt.Errorf("could not set up consumer %s on stream %s: %w", q.Durable, q.Stream, err)
	}
	if info.Config.DeliverSubject != "" {
		return fmt.Errorf("consumer %s on stream %s is a push consumer; delete it so TFBuddy can recreate it as a pull consumer", q.Durable, q.Stream)
	}
	cfg := info.Config
	if cfg.AckWait == o.AckWait && cfg.MaxDeliver == o.MaxDeliver && cfg.FilterSubject == q.Subject {
		return nil
	}
	cfg.AckWait = o.AckWait
	cfg.MaxDeliver = o.MaxDeliver
	cfg.FilterSubject = q.Subject
	if _, err := js.UpdateConsumer(q.Stream, &cfg); err != nil {
		return fmt.Errorf("could not update consumer %s on stream %s: %w", q.Durable, q.Stream, err)
	}
	log.Info().Str("stream", q.Stream).Str("consumer", q.Durable).
		Dur("ackWait", o.AckWait).Int("maxDeliver", o.MaxDeliver).
		Msg("updated JetStream pull consumer")
	return nil
}

func deleteLegacyPushConsumer(js nats.JetStreamContext, stream, durable string) error {
	info, err := js.ConsumerInfo(stream, durable)
	if errors.Is(err, nats.ErrConsumerNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not read legacy consumer %s on stream %s: %w", durable, stream, err)
	}
	if info.Config.DeliverSubject == "" {
		return nil
	}
	if err := js.DeleteConsumer(stream, durable); err != nil && !errors.Is(err, nats.ErrConsumerNotFound) {
		return fmt.Errorf("could not delete legacy push consumer %s on stream %s: %w", durable, stream, err)
	}
	log.Info().Str("stream", stream).Str("consumer", durable).
		Msg("deleted legacy JetStream push consumer; its unacked messages go to the pull consumer")
	return nil
}

func handle[T any, I Event[T]](msg *nats.Msg, queue string, o Options, fn Handler[T]) {
	logger := log.With().Str("queue", queue).Str("subject", msg.Subject).Logger()
	delivered := uint64(1)
	if md, err := msg.Metadata(); err == nil {
		delivered = md.NumDelivered
		logger = logger.With().Uint64("streamSeq", md.Sequence.Stream).Uint64("delivery", delivered).Logger()
	}
	if delivered > 1 {
		messagesRedelivered.WithLabelValues(queue).Inc()
		logger.Warn().Msg("received a redelivered JetStream message")
	}

	evt := I(new(T))
	if err := evt.DecodeEventData(msg.Data); err != nil {
		logger.Error().Err(err).Msg("dropping JetStream message that could not be decoded")
		settle(logger, queue, outcomeTerm, msg.Term())
		return
	}

	stop := keepInProgress(msg, o, logger)
	err := run(fn, (*T)(evt))
	stop()

	switch {
	case err == nil:
		settle(logger, queue, outcomeAck, msg.Ack())
	case errors.Is(err, utils.ErrPermanent):
		logger.Error().Err(err).Msg("dropping JetStream message after a permanent error")
		settle(logger, queue, outcomeTerm, msg.Term())
	case delivered >= uint64(o.MaxDeliver):
		logger.Error().Err(err).Int("maxDeliver", o.MaxDeliver).Msg("dropping JetStream message after its last delivery failed")
		settle(logger, queue, outcomeTerm, msg.Term())
	default:
		logger.Warn().Err(err).Dur("retryIn", o.NakDelay).Msg("JetStream message handler failed; it will be redelivered")
		settle(logger, queue, outcomeNak, msg.NakWithDelay(o.NakDelay))
	}
}

// run calls fn, turning a panic into a permanent error so the message is
// dropped instead of crashing the process.
func run[T any](fn Handler[T], evt *T) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("handler panicked: %v: %w", r, utils.ErrPermanent)
		}
	}()
	return fn(evt)
}

// keepInProgress extends msg's ack deadline every heartbeat until the returned
// stop function is called, or until MaxInProgress passes. stop returns once
// the heartbeat has stopped, so no progress signal can follow the ack.
func keepInProgress(msg *nats.Msg, o Options, logger zerolog.Logger) (stop func()) {
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(o.Heartbeat)
		defer ticker.Stop()
		deadline := time.NewTimer(o.MaxInProgress)
		defer deadline.Stop()
		for {
			select {
			case <-done:
				return
			case <-deadline.C:
				logger.Error().Dur("maxInProgress", o.MaxInProgress).
					Msg("JetStream message handler is still running; no longer extending its ack deadline")
				return
			case <-ticker.C:
				if err := msg.InProgress(); err != nil {
					logger.Warn().Err(err).Msg("could not extend JetStream ack deadline")
				}
			}
		}
	}()
	return func() {
		close(done)
		<-stopped
	}
}

func settle(logger zerolog.Logger, queue, outcome string, err error) {
	messagesSettled.WithLabelValues(queue, outcome).Inc()
	if err != nil {
		logger.Error().Err(err).Str("outcome", outcome).Msg("could not settle JetStream message")
	}
}
