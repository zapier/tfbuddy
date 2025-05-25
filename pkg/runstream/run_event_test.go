package runstream

import (
	"context"
	"encoding/json"
	"errors"
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

	tests := []struct {
		name      string
		setupMeta bool
		event     *TFRunEvent
		wantErr   bool
	}{
		{
			name:      "successful publish with metadata",
			setupMeta: true,
			event: &TFRunEvent{
				RunID:        "test-run-success",
				Organization: "test-org",
				Workspace:    "test-workspace",
				NewStatus:    "planning",
			},
			wantErr: false,
		},
		{
			name:      "publish without metadata fails",
			setupMeta: false,
			event: &TFRunEvent{
				RunID:        "test-run-no-meta",
				Organization: "test-org",
				Workspace:    "test-workspace",
				NewStatus:    "planning",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testID := time.Now().UnixNano()
			tt.event.RunID = fmt.Sprintf("%s-%d", tt.event.RunID, testID)

			if tt.setupMeta {
				metadata := &TFRunMetadata{
					RunID:        tt.event.RunID,
					Organization: tt.event.Organization,
					Workspace:    tt.event.Workspace,
					VcsProvider:  "gitlab",
				}
				err := s.AddRunMeta(metadata)
				if err != nil {
					t.Fatalf("Failed to add run metadata: %v", err)
				}
			}

			err := s.PublishTFRunEvent(context.Background(), tt.event)
			if (err != nil) != tt.wantErr {
				t.Errorf("PublishTFRunEvent() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
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

func TestTFRunEvent_JSONMarshaling(t *testing.T) {
	event := &TFRunEvent{
		RunID:        "run-marshal",
		Organization: "org-marshal",
		Workspace:    "ws-marshal",
		NewStatus:    "planned",
		Carrier:      map[string]string{"trace": "123"},
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal TFRunEvent: %v", err)
	}

	var decoded TFRunEvent
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Failed to unmarshal TFRunEvent: %v", err)
	}

	if decoded.RunID != event.RunID {
		t.Errorf("Decoded RunID = %v, want %v", decoded.RunID, event.RunID)
	}
	if decoded.Organization != event.Organization {
		t.Errorf("Decoded Organization = %v, want %v", decoded.Organization, event.Organization)
	}
	if decoded.Workspace != event.Workspace {
		t.Errorf("Decoded Workspace = %v, want %v", decoded.Workspace, event.Workspace)
	}
	if decoded.NewStatus != event.NewStatus {
		t.Errorf("Decoded NewStatus = %v, want %v", decoded.NewStatus, event.NewStatus)
	}
}

func Test_encodeTFRunEvent_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		event   RunEvent
		wantErr bool
	}{
		{
			name: "event with nil metadata",
			event: &TFRunEvent{
				RunID:     "test-nil-metadata",
				NewStatus: "planning",
				Metadata:  nil,
			},
			wantErr: false,
		},
		{
			name: "event with empty strings",
			event: &TFRunEvent{
				RunID:        "",
				Organization: "",
				Workspace:    "",
				NewStatus:    "",
			},
			wantErr: false,
		},
		{
			name: "event with special characters",
			event: &TFRunEvent{
				RunID:        "run-with-special-!@#$%^&*()",
				Organization: "org/with/slashes",
				Workspace:    "workspace\"with\"quotes",
				NewStatus:    "status\nwith\nnewlines",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := encodeTFRunEvent(context.Background(), tt.event)
			if (err != nil) != tt.wantErr {
				t.Errorf("encodeTFRunEvent() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && len(data) > 0 {
				// Verify we can decode it back
				decoded, err := decodeTFRunEvent(data)
				if err != nil {
					t.Errorf("Failed to decode encoded event: %v", err)
				}
				if decoded == nil {
					t.Error("Decoded event is nil")
				}
			}
		})
	}
}

func Test_decodeTFRunEvent_MalformedJSON(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{
			name:    "JSON with wrong type for field",
			data:    []byte(`{"RunID": 123, "NewStatus": "planning"}`),
			wantErr: true,
		},
		{
			name:    "JSON with extra closing brace",
			data:    []byte(`{"RunID": "test", "NewStatus": "planning"}}`),
			wantErr: true,
		},
		{
			name:    "JSON array instead of object",
			data:    []byte(`["RunID", "test"]`),
			wantErr: true,
		},
		{
			name:    "Valid JSON but empty object",
			data:    []byte(`{}`),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeTFRunEvent(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("decodeTFRunEvent() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

type failingJSONMarshaler struct{}

func (f failingJSONMarshaler) MarshalJSON() ([]byte, error) {
	return nil, errors.New("mock marshal error")
}

func TestTFRunEvent_LargeData(t *testing.T) {
	// Create a large carrier map
	largeCarrier := make(map[string]string)
	for i := 0; i < 1000; i++ {
		largeCarrier[fmt.Sprintf("key-%d", i)] = fmt.Sprintf("value-%d", i)
	}

	event := &TFRunEvent{
		RunID:        "large-data-test",
		Organization: "test-org",
		Workspace:    "test-workspace",
		NewStatus:    "planning",
		Carrier:      largeCarrier,
	}

	// Test JSON marshaling directly to preserve carrier
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal large event: %v", err)
	}

	// Test unmarshaling
	var decoded TFRunEvent
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Failed to unmarshal large event: %v", err)
	}

	if len(decoded.Carrier) != len(largeCarrier) {
		t.Errorf("Carrier size mismatch: got %d, want %d", len(decoded.Carrier), len(largeCarrier))
	}

	// Also test that encodeTFRunEvent works (it will overwrite carrier)
	_, err = encodeTFRunEvent(context.Background(), event)
	if err != nil {
		t.Fatalf("Failed to encode large event: %v", err)
	}
}

func BenchmarkEncodeTFRunEvent(b *testing.B) {
	event := &TFRunEvent{
		RunID:        "bench-run",
		Organization: "bench-org",
		Workspace:    "bench-workspace",
		NewStatus:    "planning",
		Carrier:      map[string]string{"trace": "123"},
	}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := encodeTFRunEvent(ctx, event)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeTFRunEvent(b *testing.B) {
	event := &TFRunEvent{
		RunID:        "bench-run",
		Organization: "bench-org",
		Workspace:    "bench-workspace",
		NewStatus:    "planning",
	}
	data, _ := json.Marshal(event)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := decodeTFRunEvent(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}
