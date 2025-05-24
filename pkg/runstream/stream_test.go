package runstream

import (
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func TestNewStream(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	js, cleanup := setupTestJS(t)
	defer cleanup()

	stream := NewStream(js)
	if stream == nil {
		t.Error("NewStream() returned nil")
	}

	s, ok := stream.(*Stream)
	if !ok {
		t.Error("NewStream() did not return *Stream")
		return
	}

	if s.js == nil {
		t.Error("NewStream() did not set js")
	}
	if s.metadataKV == nil {
		t.Error("NewStream() did not set metadataKV")
	}
	if s.pollingKV == nil {
		t.Error("NewStream() did not set pollingKV")
	}
}

func TestStream_HealthCheck_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	js, cleanup := setupTestJS(t)
	defer cleanup()

	stream := NewStream(js).(*Stream)

	err := stream.HealthCheck()
	if err != nil {
		t.Errorf("HealthCheck() failed: %v", err)
	}
}

func TestStream_HealthCheck_NoConsumers_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	js, cleanup := setupTestJS(t)
	defer cleanup()

	streamConfig := &nats.StreamConfig{
		Name:     RunPollingKvName,
		Subjects: []string{"test.health.*"},
		MaxMsgs:  100,
		MaxAge:   time.Hour,
	}

	_, err := js.AddStream(streamConfig)
	if err != nil {
		t.Fatalf("Failed to create test stream: %v", err)
	}
	defer func() { _ = js.DeleteStream(RunPollingKvName) }()

	s := &Stream{js: js}

	err = s.HealthCheck()
	if err == nil {
		t.Error("HealthCheck() should fail when stream has no consumers")
	}
}

func Test_addOrUpdateStream_Create_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	js, cleanup := setupTestJS(t)
	defer cleanup()

	streamConfig := &nats.StreamConfig{
		Name:     "TEST_CREATE_STREAM",
		Subjects: []string{"test.create.*"},
		MaxMsgs:  100,
		MaxAge:   time.Hour,
	}

	err := addOrUpdateStream(js, streamConfig)
	if err != nil {
		t.Errorf("addOrUpdateStream() create failed: %v", err)
	}

	defer func() { _ = js.DeleteStream("TEST_CREATE_STREAM") }()

	info, err := js.StreamInfo("TEST_CREATE_STREAM")
	if err != nil {
		t.Errorf("Stream was not created: %v", err)
	}

	if info.Config.Name != "TEST_CREATE_STREAM" {
		t.Errorf("Expected stream name TEST_CREATE_STREAM, got %s", info.Config.Name)
	}
}

func Test_addOrUpdateStream_Update_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	js, cleanup := setupTestJS(t)
	defer cleanup()

	streamConfig := &nats.StreamConfig{
		Name:     "TEST_UPDATE_STREAM",
		Subjects: []string{"test.update.*"},
		MaxMsgs:  100,
		MaxAge:   time.Hour,
	}

	err := addOrUpdateStream(js, streamConfig)
	if err != nil {
		t.Fatalf("Failed to create initial stream: %v", err)
	}
	defer func() { _ = js.DeleteStream("TEST_UPDATE_STREAM") }()

	updatedConfig := &nats.StreamConfig{
		Name:     "TEST_UPDATE_STREAM",
		Subjects: []string{"test.update.*"},
		MaxMsgs:  200,
		MaxAge:   time.Hour * 2,
	}

	err = addOrUpdateStream(js, updatedConfig)
	if err != nil {
		t.Errorf("addOrUpdateStream() update failed: %v", err)
	}

	info, err := js.StreamInfo("TEST_UPDATE_STREAM")
	if err != nil {
		t.Errorf("Stream info failed: %v", err)
	}

	if info.Config.MaxMsgs != 200 {
		t.Errorf("Expected MaxMsgs 200, got %d", info.Config.MaxMsgs)
	}
}

func Test_needsMigration(t *testing.T) {
	tests := []struct {
		name   string
		oldCfg *nats.StreamConfig
		newCfg *nats.StreamConfig
		want   bool
	}{
		{
			name: "same retention no migration",
			oldCfg: &nats.StreamConfig{
				Retention: nats.LimitsPolicy,
			},
			newCfg: &nats.StreamConfig{
				Retention: nats.LimitsPolicy,
			},
			want: false,
		},
		{
			name: "different retention needs migration",
			oldCfg: &nats.StreamConfig{
				Retention: nats.LimitsPolicy,
			},
			newCfg: &nats.StreamConfig{
				Retention: nats.WorkQueuePolicy,
			},
			want: true,
		},
		{
			name: "interest to limits needs migration",
			oldCfg: &nats.StreamConfig{
				Retention: nats.InterestPolicy,
			},
			newCfg: &nats.StreamConfig{
				Retention: nats.LimitsPolicy,
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := needsMigration(tt.oldCfg, tt.newCfg); got != tt.want {
				t.Errorf("needsMigration() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_waitForStreamSources_NoSources_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	js, cleanup := setupTestJS(t)
	defer cleanup()

	streamConfig := &nats.StreamConfig{
		Name:     "TEST_NO_SOURCES",
		Subjects: []string{"test.nosources.*"},
		MaxMsgs:  100,
		MaxAge:   time.Hour,
	}

	_, err := js.AddStream(streamConfig)
	if err != nil {
		t.Fatalf("Failed to create test stream: %v", err)
	}
	defer func() { _ = js.DeleteStream("TEST_NO_SOURCES") }()

	err = waitForStreamSources(js, "TEST_NO_SOURCES")
	if err != nil {
		t.Errorf("waitForStreamSources() failed for stream with no sources: %v", err)
	}
}

func Test_waitForStreamSources_NonexistentStream_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	js, cleanup := setupTestJS(t)
	defer cleanup()

	err := waitForStreamSources(js, "NONEXISTENT_STREAM")
	if err == nil {
		t.Error("waitForStreamSources() should fail for nonexistent stream")
	}
}

func Test_migrateStream_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	js, cleanup := setupTestJS(t)
	defer cleanup()

	originalConfig := &nats.StreamConfig{
		Name:      "TEST_MIGRATE_ORIGINAL",
		Subjects:  []string{"test.migrate.*"},
		MaxMsgs:   100,
		MaxAge:    time.Hour,
		Retention: nats.LimitsPolicy,
	}

	_, err := js.AddStream(originalConfig)
	if err != nil {
		t.Fatalf("Failed to create original stream: %v", err)
	}

	targetConfig := &nats.StreamConfig{
		Name:      "TEST_MIGRATE_ORIGINAL",
		Subjects:  []string{"test.migrate.*"},
		MaxMsgs:   100,
		MaxAge:    time.Hour,
		Retention: nats.WorkQueuePolicy,
	}

	err = migrateStream(js, "TEST_MIGRATE_ORIGINAL", targetConfig)
	if err != nil {
		t.Errorf("migrateStream() failed: %v", err)
	}

	defer func() { _ = js.DeleteStream("TEST_MIGRATE_ORIGINAL") }()

	info, err := js.StreamInfo("TEST_MIGRATE_ORIGINAL")
	if err != nil {
		t.Errorf("Stream info failed after migration: %v", err)
	}

	if info.Config.Retention != nats.WorkQueuePolicy {
		t.Errorf("Expected retention WorkQueuePolicy after migration, got %v", info.Config.Retention)
	}
}

func Test_migrateStream_NonexistentStream_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	js, cleanup := setupTestJS(t)
	defer cleanup()

	targetConfig := &nats.StreamConfig{
		Name:      "NONEXISTENT_STREAM",
		Subjects:  []string{"test.nonexistent.*"},
		MaxMsgs:   100,
		MaxAge:    time.Hour,
		Retention: nats.WorkQueuePolicy,
	}

	err := migrateStream(js, "NONEXISTENT_STREAM", targetConfig)
	if err == nil {
		t.Error("migrateStream() should fail for nonexistent stream")
	}
}

func Test_addOrUpdateStream_Migration_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	js, cleanup := setupTestJS(t)
	defer cleanup()

	originalConfig := &nats.StreamConfig{
		Name:      "TEST_MIGRATION_STREAM",
		Subjects:  []string{"test.migration.*"},
		MaxMsgs:   100,
		MaxAge:    time.Hour,
		Retention: nats.LimitsPolicy,
	}

	err := addOrUpdateStream(js, originalConfig)
	if err != nil {
		t.Fatalf("Failed to create original stream: %v", err)
	}

	newConfig := &nats.StreamConfig{
		Name:      "TEST_MIGRATION_STREAM",
		Subjects:  []string{"test.migration.*"},
		MaxMsgs:   100,
		MaxAge:    time.Hour,
		Retention: nats.WorkQueuePolicy,
	}

	err = addOrUpdateStream(js, newConfig)
	if err != nil {
		t.Errorf("addOrUpdateStream() migration failed: %v", err)
	}

	defer func() { _ = js.DeleteStream("TEST_MIGRATION_STREAM") }()

	info, err := js.StreamInfo("TEST_MIGRATION_STREAM")
	if err != nil {
		t.Errorf("Stream info failed after migration: %v", err)
	}

	if info.Config.Retention != nats.WorkQueuePolicy {
		t.Errorf("Expected retention WorkQueuePolicy after migration, got %v", info.Config.Retention)
	}
}

func Test_addOrUpdateStream_ErrorHandling_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	js, cleanup := setupTestJS(t)
	defer cleanup()

	invalidConfig := &nats.StreamConfig{
		Name:     "",
		Subjects: []string{},
		MaxMsgs:  -1,
	}

	err := addOrUpdateStream(js, invalidConfig)
	if err == nil {
		t.Error("addOrUpdateStream() should fail with invalid config")
	}
}

func TestStream_AllMethods_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	js, cleanup := setupTestJS(t)
	defer cleanup()

	stream := NewStream(js).(*Stream)

	metadata := &TFRunMetadata{
		RunID:        fmt.Sprintf("stream-test-%d", time.Now().UnixNano()),
		Organization: "test-org",
		Workspace:    "test-workspace",
		Action:       "plan",
	}

	err := stream.AddRunMeta(metadata)
	if err != nil {
		t.Errorf("AddRunMeta() failed: %v", err)
	}

	retrievedMeta, err := stream.GetRunMeta(metadata.RunID)
	if err != nil {
		t.Errorf("GetRunMeta() failed: %v", err)
	}

	if retrievedMeta.GetRunID() != metadata.RunID {
		t.Errorf("Expected RunID %s, got %s", metadata.RunID, retrievedMeta.GetRunID())
	}

	task := stream.NewTFRunPollingTask(metadata, TaskPollingDelayDefault)
	if task == nil {
		t.Error("NewTFRunPollingTask() returned nil")
	}

	if task.GetRunID() != metadata.RunID {
		t.Errorf("Expected task RunID %s, got %s", metadata.RunID, task.GetRunID())
	}

	err = stream.HealthCheck()
	if err != nil {
		t.Errorf("HealthCheck() failed: %v", err)
	}
}

func Test_configureTFRunEventsStream_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	js, cleanup := setupTestJS(t)
	defer cleanup()

	configureTFRunEventsStream(js)

	_, err := js.StreamInfo("RUN_EVENTS")
	if err != nil {
		t.Errorf("configureTFRunEventsStream() failed to create stream: %v", err)
	}
}
