package runstream

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"go.opentelemetry.io/otel/propagation"
)

type testPollingContextKey string

func TestStream_NewTFRunPollingTask(t *testing.T) {
	s := &Stream{}
	metadata := &TFRunMetadata{
		RunID:        "run-123",
		Organization: "test-org",
		Workspace:    "test-workspace",
	}

	tests := []struct {
		name      string
		delay     time.Duration
		wantDelay time.Duration
	}{
		{
			name:      "default delay",
			delay:     0,
			wantDelay: TaskPollingDelayDefault,
		},
		{
			name:      "minimum delay",
			delay:     TaskPollingDelayMinimum,
			wantDelay: TaskPollingDelayMinimum,
		},
		{
			name:      "custom delay",
			delay:     30 * time.Second,
			wantDelay: 30 * time.Second,
		},
		{
			name:      "below minimum delay uses default",
			delay:     500 * time.Millisecond,
			wantDelay: TaskPollingDelayDefault,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			startTime := time.Now()
			got := s.NewTFRunPollingTask(metadata, tt.delay)

			task, ok := got.(*TFRunPollingTask)
			if !ok {
				t.Error("NewTFRunPollingTask() did not return *TFRunPollingTask")
				return
			}

			if task.GetRunID() != "run-123" {
				t.Errorf("Expected RunID run-123, got %s", task.GetRunID())
			}
			if task.GetLastStatus() != "new" {
				t.Errorf("Expected LastStatus new, got %s", task.GetLastStatus())
			}
			if task.Processing {
				t.Error("Expected Processing to be false")
			}
			if task.stream != s {
				t.Error("Expected stream to be set")
			}
			if task.Revision != 0 {
				t.Errorf("Expected Revision 0, got %d", task.Revision)
			}

			expectedNextPoll := startTime.Add(tt.wantDelay)
			timeDiff := task.NextPoll.Sub(expectedNextPoll)
			if timeDiff < -time.Second || timeDiff > time.Second {
				t.Errorf("NextPoll time not within expected range, got %v, expected around %v", task.NextPoll, expectedNextPoll)
			}

			timeDiff = task.LastUpdate.Sub(startTime)
			if timeDiff < 0 || timeDiff > time.Second {
				t.Errorf("LastUpdate time not within expected range, got %v, expected around %v", task.LastUpdate, startTime)
			}
		})
	}
}

func TestTFRunPollingTask_GetRunID(t *testing.T) {
	tests := []struct {
		name     string
		metadata RunMetadata
		want     string
	}{
		{
			name:     "empty run ID",
			metadata: &TFRunMetadata{RunID: ""},
			want:     "",
		},
		{
			name:     "valid run ID",
			metadata: &TFRunMetadata{RunID: "run-456"},
			want:     "run-456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := &TFRunPollingTask{RunMetadata: tt.metadata}
			if got := task.GetRunID(); got != tt.want {
				t.Errorf("GetRunID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTFRunPollingTask_GetContext(t *testing.T) {
	bgCtx := context.Background()
	valueCtx := context.WithValue(context.Background(), testPollingContextKey("key"), "value")

	tests := []struct {
		name string
		ctx  context.Context
		want context.Context
	}{
		{
			name: "nil context",
			ctx:  nil,
			want: nil,
		},
		{
			name: "background context",
			ctx:  bgCtx,
			want: bgCtx,
		},
		{
			name: "context with value",
			ctx:  valueCtx,
			want: valueCtx,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := &TFRunPollingTask{ctx: tt.ctx}
			if got := task.GetContext(); got != tt.want {
				t.Errorf("GetContext() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTFRunPollingTask_SetCarrier(t *testing.T) {
	tests := []struct {
		name    string
		carrier map[string]string
	}{
		{
			name:    "nil carrier",
			carrier: nil,
		},
		{
			name:    "empty carrier",
			carrier: map[string]string{},
		},
		{
			name: "carrier with values",
			carrier: map[string]string{
				"traceparent": "00-trace-span-01",
				"tracestate":  "state=value",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := &TFRunPollingTask{}
			task.SetCarrier(tt.carrier)

			if tt.carrier == nil {
				if task.Carrier != nil {
					t.Errorf("SetCarrier() expected nil carrier, got %v", task.Carrier)
				}
			} else {
				if len(task.Carrier) != len(tt.carrier) {
					t.Errorf("SetCarrier() carrier length = %v, want %v", len(task.Carrier), len(tt.carrier))
				}
				for k, v := range tt.carrier {
					if task.Carrier[k] != v {
						t.Errorf("SetCarrier() carrier[%s] = %v, want %v", k, task.Carrier[k], v)
					}
				}
			}
		})
	}
}

func TestTFRunPollingTask_GetLastStatus(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   string
	}{
		{"empty status", "", ""},
		{"new status", "new", "new"},
		{"processing status", "processing", "processing"},
		{"completed status", "completed", "completed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := &TFRunPollingTask{LastStatus: tt.status}
			if got := task.GetLastStatus(); got != tt.want {
				t.Errorf("GetLastStatus() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTFRunPollingTask_SetLastStatus(t *testing.T) {
	tests := []struct {
		name   string
		status string
	}{
		{"set empty status", ""},
		{"set new status", "new"},
		{"set processing status", "processing"},
		{"set completed status", "completed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := &TFRunPollingTask{}
			task.SetLastStatus(tt.status)
			if task.LastStatus != tt.status {
				t.Errorf("SetLastStatus() set status = %v, want %v", task.LastStatus, tt.status)
			}
		})
	}
}

func TestTFRunPollingTask_GetRunMetaData(t *testing.T) {
	metadata := &TFRunMetadata{
		RunID:        "run-789",
		Organization: "test-org",
		Workspace:    "test-workspace",
	}

	tests := []struct {
		name     string
		metadata RunMetadata
		want     RunMetadata
	}{
		{
			name:     "nil metadata",
			metadata: nil,
			want:     nil,
		},
		{
			name:     "valid metadata",
			metadata: metadata,
			want:     metadata,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := &TFRunPollingTask{RunMetadata: tt.metadata}
			if got := task.GetRunMetaData(); got != tt.want {
				t.Errorf("GetRunMetaData() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_encodeTFRunPollingTask(t *testing.T) {
	metadata := &TFRunMetadata{
		RunID:        "run-123",
		Organization: "test-org",
		Workspace:    "test-workspace",
	}

	tests := []struct {
		name     string
		ctx      context.Context
		task     *TFRunPollingTask
		wantErr  bool
		checkKey string
	}{
		{
			name: "valid task with background context",
			ctx:  context.Background(),
			task: &TFRunPollingTask{
				RunMetadata: metadata,
				LastStatus:  "new",
				Processing:  false,
			},
			wantErr:  false,
			checkKey: "LastStatus",
		},
		{
			name: "valid task with valued context",
			ctx:  context.WithValue(context.Background(), testPollingContextKey("key"), "value"),
			task: &TFRunPollingTask{
				RunMetadata: metadata,
				LastStatus:  "processing",
				Processing:  true,
			},
			wantErr:  false,
			checkKey: "Processing",
		},
		{
			name:    "empty task",
			ctx:     context.Background(),
			task:    &TFRunPollingTask{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := encodeTFRunPollingTask(tt.ctx, tt.task)
			if (err != nil) != tt.wantErr {
				t.Errorf("encodeTFRunPollingTask() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if len(got) == 0 {
					t.Error("encodeTFRunPollingTask() returned empty data")
					return
				}

				if !json.Valid(got) {
					t.Error("encodeTFRunPollingTask() produced invalid JSON")
					return
				}

				if tt.task.Carrier == nil {
					t.Error("encodeTFRunPollingTask() should have set Carrier")
					return
				}

				var decoded map[string]interface{}
				if err := json.Unmarshal(got, &decoded); err != nil {
					t.Errorf("Failed to unmarshal encoded task: %v", err)
					return
				}

				if tt.checkKey != "" && decoded[tt.checkKey] == nil {
					t.Errorf("encodeTFRunPollingTask() missing expected key %s", tt.checkKey)
				}
			}
		})
	}
}

func TestStream_decodeTFRunPollingTask(t *testing.T) {
	s := &Stream{}
	validTask := &TFRunPollingTask{
		RunMetadata: &TFRunMetadata{
			RunID:        "run-123",
			Organization: "test-org",
		},
		LastStatus: "new",
		Processing: false,
		Carrier:    propagation.MapCarrier{"key": "value"},
	}
	validJSON, err := encodeTFRunPollingTask(context.Background(), validTask)
	if err != nil {
		t.Fatalf("Failed to encode valid task: %v", err)
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
			got, err := s.decodeTFRunPollingTask(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("decodeTFRunPollingTask() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if got == nil {
					t.Error("decodeTFRunPollingTask() returned nil task")
					return
				}
				if got.stream != s {
					t.Error("decodeTFRunPollingTask() stream not set correctly")
				}
				if got.GetContext() == nil {
					t.Error("decodeTFRunPollingTask() context should be set from carrier")
				}
			}
		})
	}
}

func Test_pollingKVKey(t *testing.T) {
	tests := []struct {
		name  string
		runID string
		want  string
	}{
		{"empty run ID", "", ""},
		{"valid run ID", "run-123", "run-123"},
		{"complex run ID", "run-org-workspace-123", "run-org-workspace-123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := &TFRunPollingTask{
				RunMetadata: &TFRunMetadata{RunID: tt.runID},
			}
			if got := pollingKVKey(task); got != tt.want {
				t.Errorf("pollingKVKey() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_pollingStreamKey(t *testing.T) {
	tests := []struct {
		name  string
		runID string
		want  string
	}{
		{"empty run ID", "", "RUN_POLLING."},
		{"valid run ID", "run-123", "RUN_POLLING.run-123"},
		{"complex run ID", "run-org-workspace-123", "RUN_POLLING.run-org-workspace-123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := &TFRunPollingTask{
				RunMetadata: &TFRunMetadata{RunID: tt.runID},
			}
			if got := pollingStreamKey(task); got != tt.want {
				t.Errorf("pollingStreamKey() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTFRunPollingTask_AllFields(t *testing.T) {
	metadata := &TFRunMetadata{
		RunID:        "run-999",
		Organization: "full-org",
		Workspace:    "full-workspace",
		Action:       "plan",
	}

	ctx := context.WithValue(context.Background(), testPollingContextKey("test"), "value")
	carrier := map[string]string{"trace": "value"}
	nextPoll := time.Now().Add(time.Hour)
	lastUpdate := time.Now()

	task := &TFRunPollingTask{
		RunMetadata: metadata,
		LastStatus:  "processing",
		NextPoll:    nextPoll,
		Processing:  true,
		LastUpdate:  lastUpdate,
		Revision:    42,
		ctx:         ctx,
		Carrier:     carrier,
	}

	if task.GetRunID() != "run-999" {
		t.Errorf("GetRunID() = %v, want run-999", task.GetRunID())
	}
	if task.GetLastStatus() != "processing" {
		t.Errorf("GetLastStatus() = %v, want processing", task.GetLastStatus())
	}
	if task.GetContext() != ctx {
		t.Errorf("GetContext() = %v, want %v", task.GetContext(), ctx)
	}
	if task.GetRunMetaData() != metadata {
		t.Errorf("GetRunMetaData() = %v, want %v", task.GetRunMetaData(), metadata)
	}

	newCarrier := map[string]string{"new": "carrier"}
	task.SetCarrier(newCarrier)
	if len(task.Carrier) != 1 || task.Carrier["new"] != "carrier" {
		t.Errorf("After SetCarrier(), Carrier = %v, want %v", task.Carrier, newCarrier)
	}

	task.SetLastStatus("completed")
	if task.GetLastStatus() != "completed" {
		t.Errorf("After SetLastStatus(), GetLastStatus() = %v, want completed", task.GetLastStatus())
	}
}

func TestTFRunPollingTask_Schedule_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	s, cleanup := setupTestStream(t)
	defer cleanup()

	testID := time.Now().UnixNano()
	runID := fmt.Sprintf("schedule-test-%d", testID)

	metadata := &TFRunMetadata{
		RunID:        runID,
		Organization: "test-org",
		Workspace:    "test-workspace",
	}

	task := s.NewTFRunPollingTask(metadata, TaskPollingDelayDefault).(*TFRunPollingTask)

	err := task.Schedule(context.Background())
	if err != nil {
		t.Errorf("Schedule() failed: %v", err)
	}

	if task.Revision == 0 {
		t.Error("Schedule() should set Revision")
	}
}

func TestTFRunPollingTask_Reschedule_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	s, cleanup := setupTestStream(t)
	defer cleanup()

	testID := time.Now().UnixNano()
	runID := fmt.Sprintf("reschedule-test-%d", testID)

	metadata := &TFRunMetadata{
		RunID:        runID,
		Organization: "test-org",
		Workspace:    "test-workspace",
	}

	task := s.NewTFRunPollingTask(metadata, TaskPollingDelayDefault).(*TFRunPollingTask)

	err := task.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Failed to schedule task: %v", err)
	}

	oldNextPoll := task.NextPoll
	task.Processing = true

	err = task.Reschedule(context.Background())
	if err != nil {
		t.Errorf("Reschedule() failed: %v", err)
	}

	if !task.NextPoll.After(oldNextPoll) {
		t.Error("Reschedule() should update NextPoll to a future time")
	}
	if task.Processing {
		t.Error("Reschedule() should set Processing to false")
	}
}

func TestTFRunPollingTask_Completed_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	s, cleanup := setupTestStream(t)
	defer cleanup()

	testID := time.Now().UnixNano()
	runID := fmt.Sprintf("completed-test-%d", testID)

	metadata := &TFRunMetadata{
		RunID:        runID,
		Organization: "test-org",
		Workspace:    "test-workspace",
	}

	task := s.NewTFRunPollingTask(metadata, TaskPollingDelayDefault).(*TFRunPollingTask)

	err := task.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Failed to schedule task: %v", err)
	}

	err = task.Completed()
	if err != nil {
		t.Errorf("Completed() failed: %v", err)
	}

	_, err = s.pollingKV.Get(runID)
	if err == nil {
		t.Error("Task should be deleted from KV store after Completed()")
	}
}

func TestStream_SubscribeTFRunPollingTasks_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	s, cleanup := setupTestStream(t)
	defer cleanup()

	testID := time.Now().UnixNano()
	runID := fmt.Sprintf("subscribe-test-%d", testID)

	received := make(chan RunPollingTask, 1)
	closer, err := s.SubscribeTFRunPollingTasks(func(task RunPollingTask) bool {
		received <- task
		return true
	})
	if err != nil {
		t.Fatalf("SubscribeTFRunPollingTasks() failed: %v", err)
	}
	defer closer()

	metadata := &TFRunMetadata{
		RunID:        runID,
		Organization: "test-org",
		Workspace:    "test-workspace",
	}

	task := s.NewTFRunPollingTask(metadata, TaskPollingDelayDefault).(*TFRunPollingTask)
	task.Processing = true

	b, err := encodeTFRunPollingTask(context.Background(), task)
	if err != nil {
		t.Fatalf("Failed to encode task: %v", err)
	}

	_, err = s.js.Publish(pollingStreamKey(task), b)
	if err != nil {
		t.Fatalf("Failed to publish task: %v", err)
	}

	select {
	case receivedTask := <-received:
		if receivedTask.GetRunID() != runID {
			t.Errorf("Expected RunID %s, got %s", runID, receivedTask.GetRunID())
		}
	case <-time.After(3 * time.Second):
		t.Error("Timeout waiting for subscribed task")
	}
}

func TestStream_SubscribeTFRunPollingTasks_Nack_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	s, cleanup := setupTestStream(t)
	defer cleanup()

	testID := time.Now().UnixNano()
	runID := fmt.Sprintf("nack-test-%d", testID)

	nackReceived := make(chan bool, 1)
	closer, err := s.SubscribeTFRunPollingTasks(func(task RunPollingTask) bool {
		nackReceived <- true
		return false // NACK the message
	})
	if err != nil {
		t.Fatalf("SubscribeTFRunPollingTasks() failed: %v", err)
	}
	defer closer()

	metadata := &TFRunMetadata{
		RunID:        runID,
		Organization: "test-org",
		Workspace:    "test-workspace",
	}

	task := s.NewTFRunPollingTask(metadata, TaskPollingDelayDefault).(*TFRunPollingTask)

	b, err := encodeTFRunPollingTask(context.Background(), task)
	if err != nil {
		t.Fatalf("Failed to encode task: %v", err)
	}

	_, err = s.js.Publish(pollingStreamKey(task), b)
	if err != nil {
		t.Fatalf("Failed to publish task: %v", err)
	}

	select {
	case <-nackReceived:
		// Success - callback was called
	case <-time.After(3 * time.Second):
		t.Error("Timeout waiting for NACK callback")
	}
}

func Test_configureRunPollingKVStore_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	s, cleanup := setupTestStream(t)
	defer cleanup()

	kv, err := configureRunPollingKVStore(s.js)
	if err != nil {
		t.Errorf("configureRunPollingKVStore() failed: %v", err)
	}

	if kv == nil {
		t.Error("configureRunPollingKVStore() returned nil KV store")
	}

	if kv.Bucket() != RunPollingKvName {
		t.Errorf("Expected bucket name %s, got %s", RunPollingKvName, kv.Bucket())
	}
}

func Test_configureTFRunPollingTaskStream_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	s, cleanup := setupTestStream(t)
	defer cleanup()

	configureTFRunPollingTaskStream(s.js)

	info, err := s.js.StreamInfo(RunPollingStreamNameV0)
	if err != nil {
		t.Errorf("configureTFRunPollingTaskStream() failed to create stream: %v", err)
	}

	if info.Config.Name != RunPollingStreamNameV0 {
		t.Errorf("Expected stream name %s, got %s", RunPollingStreamNameV0, info.Config.Name)
	}
}
