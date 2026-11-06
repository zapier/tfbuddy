package hooks_stream

import (
	"time"

	"github.com/cenkalti/backoff/v4"
)

// RetryPolicy bounds how a webhook event that could not be published is
// retried in the background.
type RetryPolicy struct {
	// For is how long to keep retrying after the first attempt failed.
	For time.Duration
	// InitialInterval and MaxInterval bound the backoff between attempts.
	InitialInterval time.Duration
	MaxInterval     time.Duration
}

// DefaultRetryPolicy outlasts a NATS server restart, which takes about a
// minute during a rollout or a node replacement.
var DefaultRetryPolicy = RetryPolicy{
	For:             2 * time.Minute,
	InitialInterval: time.Second,
	MaxInterval:     15 * time.Second,
}

// withDefaults fills unset fields from DefaultRetryPolicy. A zero For would
// otherwise mean retrying forever.
func (p RetryPolicy) withDefaults() RetryPolicy {
	if p.For <= 0 {
		p.For = DefaultRetryPolicy.For
	}
	if p.InitialInterval <= 0 {
		p.InitialInterval = DefaultRetryPolicy.InitialInterval
	}
	if p.MaxInterval <= 0 {
		p.MaxInterval = DefaultRetryPolicy.MaxInterval
	}
	return p
}

// PublishWithRetry calls publish once. If that fails, it returns false right
// away and keeps calling publish in the background, backing off, until it
// succeeds or the policy's time runs out.
//
// Webhook handlers use this because neither GitLab nor GitHub resends a
// webhook delivery on its own, and GitLab disables a webhook after four
// failed deliveries in a row. So the handler must answer quickly and with a
// 2xx even when NATS is briefly unavailable, and the event has to be kept
// alive here instead.
//
// done is called exactly once, with the final error (nil when the event was
// published) and the number of attempts, from the background goroutine when
// the first attempt failed.
func PublishWithRetry(publish func() error, policy RetryPolicy, done func(err error, attempts int)) bool {
	policy = policy.withDefaults()
	if err := publish(); err == nil {
		done(nil, 1)
		return true
	}
	go func() {
		b := backoff.NewExponentialBackOff()
		b.InitialInterval = policy.InitialInterval
		b.MaxInterval = policy.MaxInterval
		b.MaxElapsedTime = policy.For
		attempts := 1
		err := backoff.Retry(func() error {
			attempts++
			return publish()
		}, b)
		done(err, attempts)
	}()
	return false
}
