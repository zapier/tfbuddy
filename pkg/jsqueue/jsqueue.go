// Package jsqueue runs JetStream queue consumers whose handlers can take
// longer than the consumer's AckWait.
//
// A JetStream consumer redelivers a message once AckWait passes without an
// ack. TFBuddy's hook handlers plan every workspace of a merge request before
// they return, which takes far longer than JetStream's 30s default on a large
// merge request. Without a progress signal, the message went to another
// replica every 30s, and each replica planned the whole merge request again.
//
// QueueSubscribe keeps the message in progress while the handler runs, and
// settles it explicitly once the handler returns.
package jsqueue

import (
	"errors"
	"fmt"
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
)

// Options tune a queue consumer. Zero values take the defaults above.
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
)

func init() {
	prometheus.MustRegister(messagesSettled, messagesRedelivered)
}

// QueueSubscribe binds fn to the durable queue consumer named queue on
// subject in stream. Each message is handled by one subscriber.
//
// The consumer is created with Options' AckWait and MaxDeliver. An existing
// consumer is updated to them first, since nats.go refuses to bind to a
// consumer whose AckWait or MaxDeliver differ from the requested ones.
func QueueSubscribe[T any, I Event[T]](js nats.JetStreamContext, stream, subject, queue string, opts Options, fn Handler[T]) (*nats.Subscription, error) {
	o := opts.withDefaults()
	if err := ensureConsumer(js, stream, queue, o); err != nil {
		return nil, err
	}
	sub, err := js.QueueSubscribe(subject, queue,
		func(msg *nats.Msg) { handle[T, I](msg, queue, o, fn) },
		nats.BindStream(stream),
		nats.ManualAck(),
		nats.AckWait(o.AckWait),
		nats.MaxDeliver(o.MaxDeliver),
	)
	if err != nil {
		return nil, fmt.Errorf("could not subscribe %s to %s: %w", queue, subject, err)
	}
	return sub, nil
}

// ensureConsumer brings an existing durable consumer's AckWait and MaxDeliver
// in line with o. A missing consumer is left for QueueSubscribe to create.
func ensureConsumer(js nats.JetStreamContext, stream, durable string, o Options) error {
	info, err := js.ConsumerInfo(stream, durable)
	if errors.Is(err, nats.ErrConsumerNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not read consumer %s on stream %s: %w", durable, stream, err)
	}
	if info.Config.AckWait == o.AckWait && info.Config.MaxDeliver == o.MaxDeliver {
		return nil
	}
	cfg := info.Config
	cfg.AckWait = o.AckWait
	cfg.MaxDeliver = o.MaxDeliver
	if _, err := js.UpdateConsumer(stream, &cfg); err != nil {
		return fmt.Errorf("could not update consumer %s on stream %s: %w", durable, stream, err)
	}
	log.Info().Str("stream", stream).Str("consumer", durable).
		Dur("ackWait", o.AckWait).Int("maxDeliver", o.MaxDeliver).
		Dur("previousAckWait", info.Config.AckWait).Int("previousMaxDeliver", info.Config.MaxDeliver).
		Msg("updated JetStream consumer")
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
