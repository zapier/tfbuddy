package runstream

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestStream_PublishTFRunEvent_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	s, cleanup := setupTestStream(t)
	defer cleanup()

	testID := time.Now().UnixNano()
	runID := fmt.Sprintf("test-run-%d", testID)
	metadata := &TFRunMetadata{
		RunID:        runID,
		Organization: "test-org",
		Workspace:    "test-workspace",
		VcsProvider:  "gitlab",
	}

	err := s.AddRunMeta(metadata)
	if err != nil {
		t.Fatalf("Failed to add run metadata: %v", err)
	}

	event := &TFRunEvent{
		RunID:        runID,
		Organization: "test-org",
		Workspace:    "test-workspace",
		NewStatus:    "planning",
	}
	err = s.PublishTFRunEvent(context.Background(), event)
	if err != nil {
		t.Errorf("PublishTFRunEvent() failed: %v", err)
	}
}

func TestStream_SubscribeTFRunEvents_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	s, cleanup := setupTestStream(t)
	defer cleanup()

	testID := time.Now().UnixNano()
	queueName := fmt.Sprintf("gitlab-test-%d", testID)
	runID := fmt.Sprintf("sub-test-run-%d", testID)
	metadata := &TFRunMetadata{
		RunID:        runID,
		Organization: "test-org",
		Workspace:    "test-workspace",
		VcsProvider:  queueName,
	}

	err := s.AddRunMeta(metadata)
	if err != nil {
		t.Fatalf("Failed to add run metadata: %v", err)
	}

	received := make(chan RunEvent, 1)
	closer, err := s.SubscribeTFRunEvents(queueName, func(run RunEvent) bool {
		received <- run
		return true
	})
	if err != nil {
		t.Fatalf("SubscribeTFRunEvents() failed: %v", err)
	}
	defer closer()

	event := &TFRunEvent{
		RunID:        runID,
		Organization: "test-org",
		Workspace:    "test-workspace",
		NewStatus:    "applied",
	}

	err = s.PublishTFRunEvent(context.Background(), event)
	if err != nil {
		t.Fatalf("PublishTFRunEvent() failed: %v", err)
	}
	select {
	case receivedEvent := <-received:
		if receivedEvent.GetRunID() != runID {
			t.Errorf("Expected RunID '%s', got '%s'", runID, receivedEvent.GetRunID())
		}
		if receivedEvent.GetNewStatus() != "applied" {
			t.Errorf("Expected NewStatus 'applied', got '%s'", receivedEvent.GetNewStatus())
		}
		if receivedEvent.GetMetadata() == nil {
			t.Error("Expected metadata to be enriched, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Error("Timeout waiting for subscribed event")
	}
}

func TestStream_waitForTFRunMetadata_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	s, cleanup := setupTestStream(t)
	defer cleanup()

	tests := []struct {
		name          string
		setupMetadata bool
		expectedError bool
	}{
		{
			name:          "metadata exists",
			setupMetadata: true,
			expectedError: false,
		},
		{
			name:          "metadata missing",
			setupMetadata: false,
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testID := time.Now().UnixNano()
			runID := fmt.Sprintf("wait-test-%s-%d", strings.ReplaceAll(tt.name, " ", "-"), testID)

			if tt.setupMetadata {
				metadata := &TFRunMetadata{
					RunID:        runID,
					Organization: "test-org",
					Workspace:    "test-workspace",
					VcsProvider:  "gitlab",
				}
				err := s.AddRunMeta(metadata)
				if err != nil {
					t.Fatalf("Failed to setup metadata: %v", err)
				}
			}

			event := &TFRunEvent{RunID: runID}

			result, err := s.waitForTFRunMetadata(event)

			if tt.expectedError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				if result != nil {
					t.Error("Expected nil result when error occurs")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if result == nil {
					t.Error("Expected metadata result but got nil")
				}
				if result.GetRunID() != runID {
					t.Errorf("Expected RunID %s, got %s", runID, result.GetRunID())
				}
			}
		})
	}
}

type testContextKey string

func TestTFRunEvent_GetContext(t *testing.T) {
	ctx := context.WithValue(context.Background(), testContextKey("test"), "value")
	tests := []struct {
		name    string
		context context.Context
		want    context.Context
	}{
		{
			name:    "returns nil context",
			context: nil,
			want:    nil,
		},
		{
			name:    "returns background context",
			context: context.Background(),
			want:    context.Background(),
		},
		{
			name:    "returns context with value",
			context: ctx,
			want:    ctx,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			e := &TFRunEvent{context: tt.context}
			got := e.GetContext()
			if got != tt.want {
				t.Errorf("GetContext() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTFRunEvent_GetMetadata(t *testing.T) {
	metadata := &TFRunMetadata{
		RunID:        "run-123",
		Organization: "test-org",
		Workspace:    "test-workspace",
	}
	tests := []struct {
		name     string
		metadata RunMetadata
		want     RunMetadata
	}{
		{"nil metadata", nil, nil},
		{"valid metadata", metadata, metadata},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &TFRunEvent{Metadata: tt.metadata}
			got := e.GetMetadata()
			if got != tt.want {
				t.Errorf("GetMetadata() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTFRunEvent_GetNewStatus(t *testing.T) {
	tests := []struct {
		name      string
		newStatus string
		want      string
	}{
		{"empty status", "", ""},
		{"planning status", "planning", "planning"},
		{"applied status", "applied", "applied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &TFRunEvent{NewStatus: tt.newStatus}
			if got := e.GetNewStatus(); got != tt.want {
				t.Errorf("GetNewStatus() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTFRunEvent_GetRunID(t *testing.T) {
	tests := []struct {
		name  string
		runID string
		want  string
	}{
		{"empty run ID", "", ""},
		{"valid run ID", "run-123", "run-123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &TFRunEvent{RunID: tt.runID}
			if got := e.GetRunID(); got != tt.want {
				t.Errorf("GetRunID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTFRunEvent_SetCarrier(t *testing.T) {
	tests := []struct {
		name    string
		carrier map[string]string
	}{
		{
			name:    "sets nil carrier",
			carrier: nil,
		},
		{
			name:    "sets empty carrier",
			carrier: map[string]string{},
		},
		{
			name: "sets carrier with values",
			carrier: map[string]string{
				"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
				"tracestate":  "congo=t61rcWkgMzE",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			e := &TFRunEvent{}
			e.SetCarrier(tt.carrier)

			if tt.carrier == nil {
				if e.Carrier != nil {
					t.Errorf("SetCarrier() expected nil carrier, got %v", e.Carrier)
				}
			} else {
				if len(e.Carrier) != len(tt.carrier) {
					t.Errorf("SetCarrier() carrier length = %v, want %v", len(e.Carrier), len(tt.carrier))
				}
				for k, v := range tt.carrier {
					if e.Carrier[k] != v {
						t.Errorf("SetCarrier() carrier[%s] = %v, want %v", k, e.Carrier[k], v)
					}
				}
			}
		})
	}
}

func TestTFRunEvent_SetContext(t *testing.T) {
	ctx := context.WithValue(context.Background(), testContextKey("test"), "value")
	tests := []struct {
		name string
		ctx  context.Context
	}{
		{
			name: "sets nil context",
			ctx:  nil,
		},
		{
			name: "sets background context",
			ctx:  context.Background(),
		},
		{
			name: "sets context with value",
			ctx:  ctx,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			e := &TFRunEvent{}
			e.SetContext(tt.ctx)
			if e.context != tt.ctx {
				t.Errorf("SetContext() set context = %v, want %v", e.context, tt.ctx)
			}
		})
	}
}

func TestTFRunEvent_SetMetadata(t *testing.T) {
	metadata := &TFRunMetadata{
		RunID:        "run-123",
		Organization: "test-org",
		Workspace:    "test-workspace",
	}
	tests := []struct {
		name string
		meta RunMetadata
	}{
		{
			name: "sets nil metadata",
			meta: nil,
		},
		{
			name: "sets metadata",
			meta: metadata,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			e := &TFRunEvent{}
			e.SetMetadata(tt.meta)
			if e.Metadata != tt.meta {
				t.Errorf("SetMetadata() set metadata = %v, want %v", e.Metadata, tt.meta)
			}
		})
	}
}

func Test_decodeTFRunEvent(t *testing.T) {
	validEvent := &TFRunEvent{
		RunID:        "run-123",
		Organization: "test-org",
		Workspace:    "test-workspace",
		NewStatus:    "planning",
		Metadata:     &TFRunMetadata{RunID: "run-123"},
		Carrier: map[string]string{
			"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		},
	}
	validJSON, err := json.Marshal(validEvent)
	if err != nil {
		t.Fatalf("Failed to marshal valid event: %v", err)
	}

	tests := []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{
			name:    "valid JSON",
			data:    validJSON,
			wantErr: false,
		},
		{
			name:    "invalid JSON",
			data:    []byte(`{"invalid": json`),
			wantErr: true,
		},
		{
			name:    "empty data",
			data:    []byte{},
			wantErr: true,
		},
		{
			name:    "nil data",
			data:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeTFRunEvent(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("decodeTFRunEvent() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if got == nil {
					t.Error("decodeTFRunEvent() returned nil event")
					return
				}

				tfEvent, ok := got.(*TFRunEvent)
				if !ok {
					t.Error("decodeTFRunEvent() did not return TFRunEvent")
					return
				}

				if tfEvent.GetRunID() != "run-123" {
					t.Errorf("decodeTFRunEvent() RunID = %v, want run-123", tfEvent.GetRunID())
				}

				if tfEvent.GetContext() == nil {
					t.Error("decodeTFRunEvent() context should be set from carrier")
				}
			}
		})
	}
}

func Test_encodeTFRunEvent(t *testing.T) {
	tests := []struct {
		name    string
		ctx     context.Context
		event   RunEvent
		wantErr bool
	}{
		{
			name: "valid event with background context",
			ctx:  context.Background(),
			event: &TFRunEvent{
				RunID:        "run-123",
				Organization: "test-org",
				Workspace:    "test-workspace",
				NewStatus:    "planning",
				Metadata:     &TFRunMetadata{RunID: "run-123"},
			},
			wantErr: false,
		},
		{
			name: "valid event with valued context",
			ctx:  context.WithValue(context.Background(), testContextKey("test"), "value"),
			event: &TFRunEvent{
				RunID:        "run-456",
				Organization: "another-org",
				Workspace:    "another-workspace",
				NewStatus:    "applied",
			},
			wantErr: false,
		},
		{
			name:    "empty event",
			ctx:     context.Background(),
			event:   &TFRunEvent{},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := encodeTFRunEvent(tt.ctx, tt.event)
			if (err != nil) != tt.wantErr {
				t.Errorf("encodeTFRunEvent() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if len(got) == 0 {
					t.Error("encodeTFRunEvent() returned empty data")
					return
				}

				if decoded := make(map[string]interface{}); json.Unmarshal(got, &decoded) != nil {
					t.Error("encodeTFRunEvent() produced invalid JSON")
					return
				}

				if !json.Valid(got) {
					t.Error("encodeTFRunEvent() produced invalid JSON")
				}
			}
		})
	}
}

func TestTFRunEvent_AllFields(t *testing.T) {
	metadata := &TFRunMetadata{
		RunID:        "run-456",
		Organization: "test-org-2",
		Workspace:    "test-workspace-2",
	}
	carrier := map[string]string{
		"traceparent": "00-trace-span-01",
		"tracestate":  "state=value",
	}
	ctx := context.WithValue(context.Background(), testContextKey("key"), "val")

	event := &TFRunEvent{
		RunID:        "run-456",
		Organization: "test-org-2",
		Workspace:    "test-workspace-2",
		NewStatus:    "planned",
		Metadata:     metadata,
		Carrier:      carrier,
		context:      ctx,
	}

	if event.GetRunID() != "run-456" {
		t.Errorf("GetRunID() = %v, want run-456", event.GetRunID())
	}
	if event.GetNewStatus() != "planned" {
		t.Errorf("GetNewStatus() = %v, want planned", event.GetNewStatus())
	}
	if event.GetMetadata() != metadata {
		t.Errorf("GetMetadata() = %v, want %v", event.GetMetadata(), metadata)
	}
	if event.GetContext() != ctx {
		t.Errorf("GetContext() = %v, want %v", event.GetContext(), ctx)
	}

	newMetadata := &TFRunMetadata{RunID: "new-run"}
	event.SetMetadata(newMetadata)
	if event.GetMetadata() != newMetadata {
		t.Errorf("After SetMetadata(), GetMetadata() = %v, want %v", event.GetMetadata(), newMetadata)
	}

	newCarrier := map[string]string{"new": "carrier"}
	event.SetCarrier(newCarrier)
	if len(event.Carrier) != 1 || event.Carrier["new"] != "carrier" {
		t.Errorf("After SetCarrier(), Carrier = %v, want %v", event.Carrier, newCarrier)
	}

	newCtx := context.Background()
	event.SetContext(newCtx)
	if event.GetContext() != newCtx {
		t.Errorf("After SetContext(), GetContext() = %v, want %v", event.GetContext(), newCtx)
	}
}

func TestStream_PubSub_ErrorScenarios_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	s, cleanup := setupTestStream(t)
	defer cleanup()

	t.Run("publish_without_metadata", func(t *testing.T) {
		runID := fmt.Sprintf("no-metadata-run-%d", time.Now().UnixNano())

		event := &TFRunEvent{
			RunID:        runID,
			Organization: "test-org",
			NewStatus:    "planning",
		}

		err := s.PublishTFRunEvent(context.Background(), event)
		if err == nil {
			t.Error("Expected error when publishing without metadata, got none")
		}
	})

	t.Run("subscribe_callback_returns_false", func(t *testing.T) {
		testID := time.Now().UnixNano()
		runID := fmt.Sprintf("nack-test-run-%d", testID)
		queueName := fmt.Sprintf("gitlab-nack-%d", testID)

		metadata := &TFRunMetadata{
			RunID:        runID,
			Organization: "test-org",
			Workspace:    "test-workspace",
			VcsProvider:  queueName,
		}
		err := s.AddRunMeta(metadata)
		if err != nil {
			t.Fatalf("Failed to add metadata: %v", err)
		}

		nackReceived := make(chan bool, 1)
		closer, err := s.SubscribeTFRunEvents(queueName, func(run RunEvent) bool {
			nackReceived <- true
			return false
		})
		if err != nil {
			t.Fatalf("SubscribeTFRunEvents() failed: %v", err)
		}
		defer closer()

		event := &TFRunEvent{
			RunID:        runID,
			Organization: "test-org",
			NewStatus:    "planning",
		}

		err = s.PublishTFRunEvent(context.Background(), event)
		if err != nil {
			t.Fatalf("PublishTFRunEvent() failed: %v", err)
		}

		select {
		case <-nackReceived:
		case <-time.After(2 * time.Second):
			t.Error("Timeout waiting for NACK callback")
		}
	})
}
