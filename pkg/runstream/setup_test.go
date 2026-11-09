package runstream

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func shortNATSSetupRetry(t *testing.T, retryFor time.Duration) {
	t.Helper()
	saved := natsSetupRetry
	natsSetupRetry.For = retryFor
	natsSetupRetry.InitialInterval = 10 * time.Millisecond
	natsSetupRetry.MaxInterval = 50 * time.Millisecond
	t.Cleanup(func() { natsSetupRetry = saved })
}

// A bucket that can't be set up while a NATS server restarts is set up once
// the server is back, instead of being left nil.
func TestSetUpWithRetryWaitsForNATS(t *testing.T) {
	shortNATSSetupRetry(t, 5*time.Second)
	calls := 0
	got, err := setUpWithRetry(newSetupDeadline(), "POLLING_TASKS bucket", func() (string, error) {
		calls++
		if calls < 3 {
			return "", errors.New("nats: stream is offline")
		}
		return "bucket", nil
	})

	require.NoError(t, err)
	require.Equal(t, "bucket", got)
	require.Equal(t, 3, calls)
}

// Giving up returns the setup error, not the deadline's.
func TestSetUpWithRetryGivesUp(t *testing.T) {
	shortNATSSetupRetry(t, 200*time.Millisecond)
	start := time.Now()
	_, err := setUpWithRetry(newSetupDeadline(), "POLLING_TASKS bucket", func() (string, error) {
		return "", errors.New("nats: no responders available for request")
	})

	require.EqualError(t, err, "nats: no responders available for request")
	require.Less(t, time.Since(start), 3*time.Second)
}

// Setup steps share one window: a step that starts after an earlier step
// used it up gets a single attempt instead of a window of its own, so
// startup stays inside the pod's liveness allowance.
func TestSetUpStepsShareOneWindow(t *testing.T) {
	const window = 300 * time.Millisecond
	shortNATSSetupRetry(t, window)
	failing := func() (string, error) { return "", errors.New("nats: stream is offline") }
	start := time.Now()
	deadline := newSetupDeadline()

	_, err := setUpWithRetry(deadline, "RUN_EVENTS stream", failing)
	require.Error(t, err)

	calls := 0
	_, err = setUpWithRetry(deadline, "POLLING_TASKS bucket", func() (string, error) {
		calls++
		return failing()
	})
	require.Error(t, err)
	require.Equal(t, 1, calls, "a step after the window gets one attempt")
	require.Less(t, time.Since(start), window+time.Second)
}
