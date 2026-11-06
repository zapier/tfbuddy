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
	got, err := setUpWithRetry("POLLING_TASKS bucket", func() (string, error) {
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

func TestSetUpWithRetryGivesUp(t *testing.T) {
	shortNATSSetupRetry(t, 200*time.Millisecond)
	start := time.Now()
	_, err := setUpWithRetry("POLLING_TASKS bucket", func() (string, error) {
		return "", errors.New("nats: no responders available for request")
	})

	require.Error(t, err)
	require.Less(t, time.Since(start), 3*time.Second)
}
