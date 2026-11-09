package runstream

import (
	"context"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/rs/zerolog/log"
)

// natsSetupRetry bounds how long NewStream waits for NATS while it sets up
// its streams and KV buckets. A NATS server restart, during a rollout or a
// node replacement, takes about a minute, and a TFBuddy pod that starts in
// the meantime should wait for it instead of starting without a bucket.
//
// All of NewStream's setup steps share one window, so together they still
// finish well inside the pod's liveness allowance of about 5 minutes. A step
// that starts after the window has ended gets one attempt, which the NATS
// request timeout bounds.
var natsSetupRetry = struct {
	For, InitialInterval, MaxInterval time.Duration
}{For: 2 * time.Minute, InitialInterval: time.Second, MaxInterval: 10 * time.Second}

// newSetupDeadline starts the setup window NewStream's steps share.
func newSetupDeadline() time.Time {
	return time.Now().Add(natsSetupRetry.For)
}

// setUpWithRetry runs setup until it succeeds or deadline passes. It always
// tries at least once, and on giving up it returns setup's last error.
func setUpWithRetry[T any](deadline time.Time, what string, setup func() (T, error)) (T, error) {
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = natsSetupRetry.InitialInterval
	b.MaxInterval = natsSetupRetry.MaxInterval
	// The shared deadline ends the retries, not the backoff's own clock.
	b.MaxElapsedTime = 0
	var v T
	var lastErr error
	err := backoff.RetryNotify(func() error {
		var err error
		v, err = setup()
		lastErr = err
		return err
	}, backoff.WithContext(b, ctx), func(err error, next time.Duration) {
		log.Warn().Err(err).Str("resource", what).Dur("retryIn", next).Msg("could not set up a NATS resource; retrying")
	})
	if err != nil && lastErr != nil {
		// When the window ends, backoff returns the context's error; the
		// setup error says what was wrong.
		return v, lastErr
	}
	return v, err
}

// mustSetUp is setUpWithRetry for a resource TFBuddy can't run without. It
// exits with a clear error instead of returning a nil resource that would
// crash the process later.
func mustSetUp[T any](deadline time.Time, what string, setup func() (T, error)) T {
	v, err := setUpWithRetry(deadline, what, setup)
	if err != nil {
		log.Fatal().Err(err).Str("resource", what).Msg("could not set up a NATS resource; is NATS reachable?")
	}
	return v
}

// noValue adapts a setup step that returns only an error.
func noValue(setup func() error) func() (struct{}, error) {
	return func() (struct{}, error) { return struct{}{}, setup() }
}
