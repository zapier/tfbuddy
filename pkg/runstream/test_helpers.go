package runstream

import (
	"flag"
	"fmt"
	"testing"

	"github.com/nats-io/nats-server/v2/server"
	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
)

var integration = flag.Bool("integration", false, "run integration tests")

func setupTestNATSStream(t *testing.T, port int) (*Stream, *server.Server, nats.JetStreamContext, func()) {
	opts := natstest.DefaultTestOptions
	opts.Port = port
	opts.JetStream = true
	s := natstest.RunServer(&opts)

	url := fmt.Sprintf("nats://127.0.0.1:%d", port)
	nc, err := nats.Connect(url)
	if err != nil {
		s.Shutdown()
		t.Fatalf("Could not connect to test NATS server: %v", err)
	}

	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		s.Shutdown()
		t.Fatalf("Could not create JetStream context: %v", err)
	}

	stream := NewStream(js).(*Stream)

	cleanup := func() {
		nc.Close()
		s.Shutdown()
	}

	return stream, s, js, cleanup
}

func setupTestStream(t *testing.T) (*Stream, func()) {
	stream, _, _, cleanup := setupTestNATSStream(t, 8370)
	return stream, cleanup
}

func setupTestJS(t *testing.T) (nats.JetStreamContext, func()) {
	_, _, js, cleanup := setupTestNATSStream(t, 8371)
	return js, cleanup
}
