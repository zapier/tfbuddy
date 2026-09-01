package runstream

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
)

// testDedupWindow is the JetStream Duplicates window used by configureTFRunEventsStream
// in tests. Pick a value that comfortably exceeds the duration of a single test.
const testDedupWindow = 30 * time.Minute

// startTestNATS spins up an in-process NATS server bound to an OS-allocated
// port (avoiding the flake risk of hard-coded ports when CI runs tests in
// parallel or when local dev already has something on 8369). The server is
// shut down via t.Cleanup so tests stay lean.
func startTestNATS(t *testing.T) (*server.Server, string) {
	t.Helper()
	opts := natstest.DefaultTestOptions
	opts.Port = -1 // ask the OS for a free port
	opts.JetStream = true
	opts.StoreDir = t.TempDir()
	srv := natstest.RunServer(&opts)
	t.Cleanup(srv.Shutdown)

	addr, ok := srv.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("NATS test server bound to unexpected addr type %T", srv.Addr())
	}
	return srv, fmt.Sprintf("nats://127.0.0.1:%d", addr.Port)
}

func Test_configureRunPollingKVStore(t *testing.T) {
	_, url := startTestNATS(t)
	nc := testConnect(t, url)
	defer nc.Close()

	js := testGetJetstreamContext(t, nc)

	type args struct {
		js nats.JetStreamContext
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "basic",
			args: args{
				js: js,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := configureRunPollingKVStore(tt.args.js)
			if err != nil {
				t.Errorf("configureRunPollingKVStore() failure")
			}
			if got == nil {
				t.Errorf("configureRunPollingKVStore() failure")
			}
		})
	}
}

func Test_configureTFRunEventsStream(t *testing.T) {
	_, url := startTestNATS(t)
	nc := testConnect(t, url)
	defer nc.Close()

	js := testGetJetstreamContext(t, nc)

	type args struct {
		js          nats.JetStreamContext
		dedupWindow time.Duration
	}
	tests := []struct {
		name string
		args args
	}{
		{
			"create",
			args{
				js:          js,
				dedupWindow: testDedupWindow,
			},
		},
		{
			"update",
			args{
				js:          js,
				dedupWindow: testDedupWindow,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configureTFRunEventsStream(tt.args.js, tt.args.dedupWindow)
		})
	}
}

func testConnect(t *testing.T, url string) *nats.Conn {
	nc, err := nats.Connect(url)
	if err != nil {
		t.Errorf("could not connect to NATS test server: %v", err)
	}
	return nc
}

func testGetJetstreamContext(t *testing.T, nc *nats.Conn) nats.JetStreamContext {
	js, err := nc.JetStream()
	if err != nil {
		t.Errorf("could not create NATS Jetstream Context for tests: %v", err)
	}
	return js
}
