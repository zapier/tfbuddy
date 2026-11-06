package hooks_stream

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var testRetryPolicy = RetryPolicy{For: 2 * time.Second, InitialInterval: 10 * time.Millisecond, MaxInterval: 50 * time.Millisecond}

type result struct {
	err      error
	attempts int
}

func TestPublishWithRetryPublishesRightAway(t *testing.T) {
	results := make(chan result, 1)
	ok := PublishWithRetry(func() error { return nil }, testRetryPolicy, func(err error, attempts int) {
		results <- result{err, attempts}
	})

	require.True(t, ok)
	require.Equal(t, result{nil, 1}, <-results)
}

// A NATS server that comes back while the event is retried gets the event.
func TestPublishWithRetryKeepsTheEventUntilNATSIsBack(t *testing.T) {
	var calls atomic.Int32
	results := make(chan result, 1)
	ok := PublishWithRetry(func() error {
		if calls.Add(1) < 4 {
			return errors.New("nats: no response from stream")
		}
		return nil
	}, testRetryPolicy, func(err error, attempts int) {
		results <- result{err, attempts}
	})

	require.False(t, ok, "the first attempt failed, so the caller must not wait for the retries")
	select {
	case r := <-results:
		require.NoError(t, r.err)
		require.Equal(t, 4, r.attempts)
	case <-time.After(5 * time.Second):
		t.Fatal("the event was not published once NATS was back")
	}
}

func TestPublishWithRetryGivesUpAfterThePolicy(t *testing.T) {
	policy := RetryPolicy{For: 200 * time.Millisecond, InitialInterval: 10 * time.Millisecond, MaxInterval: 20 * time.Millisecond}
	results := make(chan result, 1)
	start := time.Now()
	PublishWithRetry(func() error { return errors.New("nats: timeout") }, policy, func(err error, attempts int) {
		results <- result{err, attempts}
	})

	select {
	case r := <-results:
		require.Error(t, r.err)
		require.Greater(t, r.attempts, 2)
		require.Less(t, time.Since(start), 2*time.Second)
	case <-time.After(5 * time.Second):
		t.Fatal("the retries did not stop")
	}
}
