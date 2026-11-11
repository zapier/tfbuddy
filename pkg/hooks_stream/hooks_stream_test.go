package hooks_stream

import (
	"fmt"
	"net"
	"testing"
	"time"

	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
)

// natsPublishTimeout is the JetStream publish timeout TFBuddy uses, the
// nats.go default.
const natsPublishTimeout = 5 * time.Second

// A retry of an event whose first publish was stored but not acknowledged
// must still be dropped as a duplicate, so the window covers the first
// attempt, the retries and the last attempt.
func TestDuplicatesWindowOutlastsPublishRetries(t *testing.T) {
	require.Greater(t, hooksDuplicatesWindow, natsPublishTimeout+DefaultRetryPolicy.For+natsPublishTimeout)
}

// The window is set when the stream is created and when an existing stream,
// created with the 2-minute default, is updated on startup.
func TestConfigureHooksStreamSetsTheDuplicatesWindow(t *testing.T) {
	opts := natstest.DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = t.TempDir()
	srv := natstest.RunServer(&opts)
	t.Cleanup(srv.Shutdown)
	nc, err := nats.Connect(fmt.Sprintf("nats://127.0.0.1:%d", srv.Addr().(*net.TCPAddr).Port))
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	js, err := nc.JetStream()
	require.NoError(t, err)

	_, err = js.AddStream(&nats.StreamConfig{
		Name:      HooksStreamName,
		Subjects:  []string{HooksStreamName + ".>"},
		Retention: nats.WorkQueuePolicy,
		MaxAge:    time.Hour,
	})
	require.NoError(t, err)
	info, err := js.StreamInfo(HooksStreamName)
	require.NoError(t, err)
	require.Equal(t, 2*time.Minute, info.Config.Duplicates, "an existing stream with the default window")

	configureHooksStream(js)

	info, err = js.StreamInfo(HooksStreamName)
	require.NoError(t, err)
	require.Equal(t, hooksDuplicatesWindow, info.Config.Duplicates)
}
