package runstream

import (
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/rs/zerolog/log"
)

// natsSetupRetry bounds how long NewStream waits for NATS while it sets up
// its streams and KV buckets. A NATS server restart, during a rollout or a
// node replacement, takes about a minute, and a TFBuddy pod that starts in
// the meantime should wait for it instead of starting without a bucket.
var natsSetupRetry = struct {
	For, InitialInterval, MaxInterval time.Duration
}{For: 2 * time.Minute, InitialInterval: time.Second, MaxInterval: 10 * time.Second}

// setUpWithRetry runs setup until it succeeds or natsSetupRetry.For passes.
func setUpWithRetry[T any](what string, setup func() (T, error)) (T, error) {
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = natsSetupRetry.InitialInterval
	b.MaxInterval = natsSetupRetry.MaxInterval
	b.MaxElapsedTime = natsSetupRetry.For
	var v T
	err := backoff.RetryNotify(func() error {
		var err error
		v, err = setup()
		return err
	}, b, func(err error, next time.Duration) {
		log.Warn().Err(err).Str("resource", what).Dur("retryIn", next).Msg("could not set up a NATS resource; retrying")
	})
	return v, err
}

// mustSetUp is setUpWithRetry for a resource TFBuddy can't run without. It
// exits with a clear error instead of returning a nil resource that would
// crash the process later.
func mustSetUp[T any](what string, setup func() (T, error)) T {
	v, err := setUpWithRetry(what, setup)
	if err != nil {
		log.Fatal().Err(err).Str("resource", what).Msg("could not set up a NATS resource; is NATS reachable?")
	}
	return v
}

// noValue adapts a setup step that returns only an error.
func noValue(setup func() error) func() (struct{}, error) {
	return func() (struct{}, error) { return struct{}{}, setup() }
}
