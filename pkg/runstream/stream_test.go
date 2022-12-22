package runstream

import (
	"fmt"
	"testing"

	"github.com/nats-io/nats-server/v2/server"
	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
)

const TEST_PORT = 8369

func RunServerOnPort(port int) *server.Server {
	opts := natstest.DefaultTestOptions
	opts.Port = port
	opts.JetStream = true
	return RunServerWithOptions(&opts)
}

func RunServerWithOptions(opts *server.Options) *server.Server {
	return natstest.RunServer(opts)
}

func Test_configureRunPollingKVStore(t *testing.T) {
	s := RunServerOnPort(TEST_PORT)
	defer s.Shutdown()

	url := fmt.Sprintf("nats://127.0.0.1:%d", TEST_PORT)
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
	s := RunServerOnPort(TEST_PORT)
	defer s.Shutdown()

	url := fmt.Sprintf("nats://127.0.0.1:%d", TEST_PORT)
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
			"create",
			args{
				js: js,
			},
		},
		{
			"update",
			args{
				js: js,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configureTFRunEventsStream(tt.args.js)
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
