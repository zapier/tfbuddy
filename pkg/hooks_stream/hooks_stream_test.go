package hooks_stream

import (
	"fmt"
	"net"
	"testing"

	"github.com/nats-io/nats-server/v2/server"
	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
)

func runTestServer(t *testing.T) (*server.Server, int) {
	opts := natstest.DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	s := natstest.RunServer(&opts)
	return s, s.Addr().(*net.TCPAddr).Port
}

func testConnect(t *testing.T, port int) *nats.Conn {
	url := fmt.Sprintf("nats://127.0.0.1:%d", port)
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("failed to connect to test server: %v", err)
	}
	return nc
}

func TestNewHooksStream(t *testing.T) {
	s, port := runTestServer(t)
	defer s.Shutdown()

	nc := testConnect(t, port)
	defer nc.Close()

	hs := NewHooksStream(nc)
	if hs == nil {
		t.Fatal("NewHooksStream returned nil")
	}
	if hs.nc != nc {
		t.Error("HooksStream.nc not set correctly")
	}
	if hs.js == nil {
		t.Error("HooksStream.js not initialized")
	}

	info, err := hs.js.StreamInfo(HooksStreamName)
	if err != nil {
		t.Errorf("stream not created: %v", err)
	}
	if info.Config.Name != HooksStreamName {
		t.Errorf("expected stream name %s, got %s", HooksStreamName, info.Config.Name)
	}
}

func TestHooksStream_HealthCheck_NoConsumers(t *testing.T) {
	s, port := runTestServer(t)
	defer s.Shutdown()

	nc := testConnect(t, port)
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("failed to get JetStream context: %v", err)
	}

	configureHooksStream(js)

	// Ensure no consumers exist by deleting any that might exist
	consumers := js.Consumers(HooksStreamName)
	for consumer := range consumers {
		if consumer != nil {
			js.DeleteConsumer(HooksStreamName, consumer.Name)
		}
	}

	hs := &HooksStream{nc: nc, js: js}

	err = hs.HealthCheck()
	if err == nil {
		t.Error("expected error when no consumers, got nil")
	} else if err.Error() != "HOOKS stream has no consumers" {
		t.Errorf("expected specific error message, got: %v", err)
	}
}

func TestHooksStream_HealthCheck_WithConsumers(t *testing.T) {
	s, port := runTestServer(t)
	defer s.Shutdown()

	nc := testConnect(t, port)
	defer nc.Close()

	hs := NewHooksStream(nc)

	_, err := hs.js.AddConsumer(HooksStreamName, &nats.ConsumerConfig{
		Durable:   "test-consumer",
		AckPolicy: nats.AckExplicitPolicy,
	})
	if err != nil {
		t.Fatalf("failed to create consumer: %v", err)
	}

	err = hs.HealthCheck()
	if err != nil {
		t.Errorf("expected no error with consumers, got: %v", err)
	}
}

func TestConfigureHooksStream_UpdateExisting(t *testing.T) {
	s, port := runTestServer(t)
	defer s.Shutdown()

	nc := testConnect(t, port)
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("failed to get JetStream context: %v", err)
	}

	configureHooksStream(js)

	configureHooksStream(js)

	info, err := js.StreamInfo(HooksStreamName)
	if err != nil {
		t.Errorf("stream not found after update: %v", err)
	}
	if info.Config.Name != HooksStreamName {
		t.Errorf("expected stream name %s, got %s", HooksStreamName, info.Config.Name)
	}
	if info.Config.Retention != nats.WorkQueuePolicy {
		t.Errorf("expected WorkQueuePolicy retention, got %v", info.Config.Retention)
	}
}
