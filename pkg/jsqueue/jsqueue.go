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
// Consume pulls one message at a time through nats.go's jetstream package and
// pulls the next only once the handler has returned, so waiting messages stay
// in the stream with no clock running. What the library does not do is keep a
// long-running message in progress, or turn the handler's result into an ack,
// a delayed retry or a drop; that is the code here.
package jsqueue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
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

	setupTimeout = 30 * time.Second
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
	// messageWait measures queue freshness: how long a message sat in the
	// stream before a replica picked it up the first time. Redeliveries are
	// left out, since their wait includes the earlier attempts; they are
	// counted by messagesRedelivered.
	messageWait = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "tfbuddy_jetstream_message_wait_seconds",
		Help:    "Seconds a JetStream queue message waited in the stream before its first delivery, by queue",
		Buckets: []float64{0.1, 0.5, 1, 5, 10, 30, 60, 120, 300, 600, 1800, 3600},
	}, []string{"queue"})
)

func init() {
	prometheus.MustRegister(messagesSettled, messagesRedelivered, messageWait)
}

// observeWait records how long a first delivery waited in the stream. A
// stored time ahead of this replica's clock counts as no wait.
func observeWait(queue string, stored, now time.Time) {
	wait := now.Sub(stored)
	if wait < 0 {
		wait = 0
	}
	messageWait.WithLabelValues(queue).Observe(wait.Seconds())
}

// Consume handles q's messages with fn, one at a time: the next message is
// pulled only once fn has returned, so several replicas calling Consume share
// the queue and each takes the next message when it is free. Stop the
// returned ConsumeContext to end it.
func Consume[T any, I Event[T]](js jetstream.JetStream, q Queue, opts Options, fn Handler[T]) (jetstream.ConsumeContext, error) {
	o := opts.withDefaults()
	ctx, cancel := context.WithTimeout(context.Background(), setupTimeout)
	defer cancel()

	if q.LegacyPushDurable != "" {
		if err := deleteLegacyPushConsumer(ctx, js, q.Stream, q.LegacyPushDurable); err != nil {
			return nil, err
		}
	}
	cons, err := js.CreateOrUpdateConsumer(ctx, q.Stream, jetstream.ConsumerConfig{
		Durable:       q.Durable,
		FilterSubject: q.Subject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       o.AckWait,
		MaxDeliver:    o.MaxDeliver,
	})
	if err != nil {
		return nil, fmt.Errorf("could not set up consumer %s on stream %s: %w", q.Durable, q.Stream, err)
	}

	logger := log.With().Str("stream", q.Stream).Str("queue", q.Durable).Logger()
	// With PullMaxMessages(1), Consume asks for the next message only after
	// the handler returns. It also pauses its own idle-heartbeat check while
	// the handler runs, so a handler that takes minutes does not make it pull
	// a second message early.
	cc, err := cons.Consume(
		func(msg jetstream.Msg) { handle[T, I](msg, q.Durable, o, fn) },
		jetstream.PullMaxMessages(1),
		jetstream.ConsumeErrHandler(func(_ jetstream.ConsumeContext, err error) {
			logger.Warn().Err(err).Msg("JetStream consumer error")
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("could not consume %s on stream %s: %w", q.Durable, q.Stream, err)
	}
	return cc, nil
}

func deleteLegacyPushConsumer(ctx context.Context, js jetstream.JetStream, stream, durable string) error {
	_, err := js.Consumer(ctx, stream, durable)
	switch {
	case err == nil, errors.Is(err, jetstream.ErrConsumerNotFound):
		// Gone already, or a pull consumer by that name: nothing to replace.
		return nil
	case !errors.Is(err, jetstream.ErrNotPullConsumer):
		return fmt.Errorf("could not read legacy consumer %s on stream %s: %w", durable, stream, err)
	}
	if err := js.DeleteConsumer(ctx, stream, durable); err != nil && !errors.Is(err, jetstream.ErrConsumerNotFound) {
		return fmt.Errorf("could not delete legacy push consumer %s on stream %s: %w", durable, stream, err)
	}
	log.Info().Str("stream", stream).Str("consumer", durable).
		Msg("deleted legacy JetStream push consumer; its unacked messages go to the pull consumer")
	return nil
}

func handle[T any, I Event[T]](msg jetstream.Msg, queue string, o Options, fn Handler[T]) {
	logger := log.With().Str("queue", queue).Str("subject", msg.Subject()).Logger()
	delivered := uint64(1)
	if md, err := msg.Metadata(); err == nil {
		delivered = md.NumDelivered
		logger = logger.With().Uint64("streamSeq", md.Sequence.Stream).Uint64("delivery", delivered).Logger()
		if delivered == 1 {
			observeWait(queue, md.Timestamp, time.Now())
		}
	}
	if delivered > 1 {
		messagesRedelivered.WithLabelValues(queue).Inc()
		logger.Warn().Msg("received a redelivered JetStream message")
	}

	evt := I(new(T))
	if err := evt.DecodeEventData(msg.Data()); err != nil {
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
func keepInProgress(msg jetstream.Msg, o Options, logger zerolog.Logger) (stop func()) {
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
