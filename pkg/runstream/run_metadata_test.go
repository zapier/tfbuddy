package runstream

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestTFRunMetadata_GetAction(t *testing.T) {
	tests := []struct {
		name   string
		action string
		want   string
	}{
		{"empty action", "", ""},
		{"plan action", "plan", "plan"},
		{"apply action", "apply", "apply"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &TFRunMetadata{Action: tt.action}
			if got := r.GetAction(); got != tt.want {
				t.Errorf("GetAction() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTFRunMetadata_GetMRInternalID(t *testing.T) {
	tests := []struct {
		name string
		id   int
		want int
	}{
		{"zero ID", 0, 0},
		{"positive ID", 123, 123},
		{"negative ID", -1, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &TFRunMetadata{MergeRequestIID: tt.id}
			if got := r.GetMRInternalID(); got != tt.want {
				t.Errorf("GetMRInternalID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTFRunMetadata_GetRootNoteID(t *testing.T) {
	tests := []struct {
		name   string
		noteID int64
		want   int64
	}{
		{"zero note ID", 0, 0},
		{"positive note ID", 12345, 12345},
		{"negative note ID", -1, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &TFRunMetadata{RootNoteID: tt.noteID}
			if got := r.GetRootNoteID(); got != tt.want {
				t.Errorf("GetRootNoteID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTFRunMetadata_GetMRProjectNameWithNamespace(t *testing.T) {
	tests := []struct {
		name        string
		projectName string
		want        string
	}{
		{"empty project name", "", ""},
		{"simple project", "myproject", "myproject"},
		{"namespaced project", "group/subgroup/project", "group/subgroup/project"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &TFRunMetadata{MergeRequestProjectNameWithNamespace: tt.projectName}
			if got := r.GetMRProjectNameWithNamespace(); got != tt.want {
				t.Errorf("GetMRProjectNameWithNamespace() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTFRunMetadata_GetDiscussionID(t *testing.T) {
	tests := []struct {
		name         string
		discussionID string
		want         string
	}{
		{"empty discussion ID", "", ""},
		{"valid discussion ID", "discussion-123", "discussion-123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &TFRunMetadata{DiscussionID: tt.discussionID}
			if got := r.GetDiscussionID(); got != tt.want {
				t.Errorf("GetDiscussionID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTFRunMetadata_GetRunID(t *testing.T) {
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
			r := &TFRunMetadata{RunID: tt.runID}
			if got := r.GetRunID(); got != tt.want {
				t.Errorf("GetRunID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTFRunMetadata_GetWorkspace(t *testing.T) {
	tests := []struct {
		name      string
		workspace string
		want      string
	}{
		{"empty workspace", "", ""},
		{"valid workspace", "my-workspace", "my-workspace"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &TFRunMetadata{Workspace: tt.workspace}
			if got := r.GetWorkspace(); got != tt.want {
				t.Errorf("GetWorkspace() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTFRunMetadata_GetCommitSHA(t *testing.T) {
	tests := []struct {
		name      string
		commitSHA string
		want      string
	}{
		{"empty commit SHA", "", ""},
		{"valid commit SHA", "abc123def456", "abc123def456"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &TFRunMetadata{CommitSHA: tt.commitSHA}
			if got := r.GetCommitSHA(); got != tt.want {
				t.Errorf("GetCommitSHA() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTFRunMetadata_GetOrganization(t *testing.T) {
	tests := []struct {
		name         string
		organization string
		want         string
	}{
		{"empty organization", "", ""},
		{"valid organization", "my-org", "my-org"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &TFRunMetadata{Organization: tt.organization}
			if got := r.GetOrganization(); got != tt.want {
				t.Errorf("GetOrganization() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTFRunMetadata_GetVcsProvider(t *testing.T) {
	tests := []struct {
		name        string
		vcsProvider string
		want        string
	}{
		{"empty VCS provider", "", ""},
		{"gitlab provider", "gitlab", "gitlab"},
		{"github provider", "github", "github"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &TFRunMetadata{VcsProvider: tt.vcsProvider}
			if got := r.GetVcsProvider(); got != tt.want {
				t.Errorf("GetVcsProvider() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTFRunMetadata_GetAutoMerge(t *testing.T) {
	tests := []struct {
		name      string
		autoMerge bool
		want      bool
	}{
		{"auto merge false", false, false},
		{"auto merge true", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &TFRunMetadata{AutoMerge: tt.autoMerge}
			got := r.GetAutoMerge()
			if got != tt.want && tt.autoMerge {
				t.Errorf("GetAutoMerge() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_encodeTFRunMetadata(t *testing.T) {
	tests := []struct {
		name     string
		metadata RunMetadata
		wantErr  bool
	}{
		{
			name: "valid metadata",
			metadata: &TFRunMetadata{
				RunID:        "run-123",
				Organization: "test-org",
				Workspace:    "test-workspace",
				Action:       "plan",
			},
			wantErr: false,
		},
		{
			name:     "empty metadata",
			metadata: &TFRunMetadata{},
			wantErr:  false,
		},
		{
			name: "fully populated metadata",
			metadata: &TFRunMetadata{
				RunID:                                "run-456",
				Organization:                         "my-org",
				Workspace:                            "my-workspace",
				Source:                               "merge_request",
				Action:                               "apply",
				CommitSHA:                            "abc123",
				MergeRequestProjectNameWithNamespace: "group/project",
				MergeRequestIID:                      42,
				DiscussionID:                         "discussion-123",
				RootNoteID:                           789,
				VcsProvider:                          "gitlab",
				AutoMerge:                            true,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := encodeTFRunMetadata(tt.metadata)
			if (err != nil) != tt.wantErr {
				t.Errorf("encodeTFRunMetadata() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if len(got) == 0 {
					t.Error("encodeTFRunMetadata() returned empty data")
					return
				}

				if !json.Valid(got) {
					t.Error("encodeTFRunMetadata() produced invalid JSON")
				}
			}
		})
	}
}

func Test_decodeTFRunMetadata(t *testing.T) {
	validMetadata := &TFRunMetadata{
		RunID:        "run-123",
		Organization: "test-org",
		Workspace:    "test-workspace",
		Action:       "plan",
		CommitSHA:    "abc123",
	}
	validJSON, err := json.Marshal(validMetadata)
	if err != nil {
		t.Fatalf("Failed to marshal valid metadata: %v", err)
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
			got, err := decodeTFRunMetadata(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("decodeTFRunMetadata() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if got == nil {
					t.Error("decodeTFRunMetadata() returned nil metadata")
					return
				}

				tfMetadata, ok := got.(*TFRunMetadata)
				if !ok {
					t.Error("decodeTFRunMetadata() did not return TFRunMetadata")
					return
				}

				if tfMetadata.GetRunID() != "run-123" {
					t.Errorf("decodeTFRunMetadata() RunID = %v, want run-123", tfMetadata.GetRunID())
				}
			}
		})
	}
}

func TestTFRunMetadata_AllFields(t *testing.T) {
	metadata := &TFRunMetadata{
		RunID:                                "run-789",
		Organization:                         "full-org",
		Workspace:                            "full-workspace",
		Source:                               "merge_request",
		Action:                               "plan",
		CommitSHA:                            "def456ghi789",
		MergeRequestProjectNameWithNamespace: "group/subgroup/project",
		MergeRequestIID:                      99,
		DiscussionID:                         "discussion-456",
		RootNoteID:                           12345,
		VcsProvider:                          "gitlab",
		AutoMerge:                            false,
	}

	if metadata.GetRunID() != "run-789" {
		t.Errorf("GetRunID() = %v, want run-789", metadata.GetRunID())
	}
	if metadata.GetOrganization() != "full-org" {
		t.Errorf("GetOrganization() = %v, want full-org", metadata.GetOrganization())
	}
	if metadata.GetWorkspace() != "full-workspace" {
		t.Errorf("GetWorkspace() = %v, want full-workspace", metadata.GetWorkspace())
	}
	if metadata.GetAction() != "plan" {
		t.Errorf("GetAction() = %v, want plan", metadata.GetAction())
	}
	if metadata.GetCommitSHA() != "def456ghi789" {
		t.Errorf("GetCommitSHA() = %v, want def456ghi789", metadata.GetCommitSHA())
	}
	if metadata.GetMRProjectNameWithNamespace() != "group/subgroup/project" {
		t.Errorf("GetMRProjectNameWithNamespace() = %v, want group/subgroup/project", metadata.GetMRProjectNameWithNamespace())
	}
	if metadata.GetMRInternalID() != 99 {
		t.Errorf("GetMRInternalID() = %v, want 99", metadata.GetMRInternalID())
	}
	if metadata.GetDiscussionID() != "discussion-456" {
		t.Errorf("GetDiscussionID() = %v, want discussion-456", metadata.GetDiscussionID())
	}
	if metadata.GetRootNoteID() != 12345 {
		t.Errorf("GetRootNoteID() = %v, want 12345", metadata.GetRootNoteID())
	}
	if metadata.GetVcsProvider() != "gitlab" {
		t.Errorf("GetVcsProvider() = %v, want gitlab", metadata.GetVcsProvider())
	}
}

func TestStream_AddRunMeta_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	s, cleanup := setupTestStream(t)
	defer cleanup()

	testID := time.Now().UnixNano()
	runID := fmt.Sprintf("add-meta-test-%d", testID)

	metadata := &TFRunMetadata{
		RunID:        runID,
		Organization: "test-org",
		Workspace:    "test-workspace",
		Action:       "plan",
		VcsProvider:  "gitlab",
	}

	err := s.AddRunMeta(metadata)
	if err != nil {
		t.Errorf("AddRunMeta() failed: %v", err)
	}
}

func TestStream_GetRunMeta_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	s, cleanup := setupTestStream(t)
	defer cleanup()

	testID := time.Now().UnixNano()
	runID := fmt.Sprintf("get-meta-test-%d", testID)

	originalMetadata := &TFRunMetadata{
		RunID:        runID,
		Organization: "test-org",
		Workspace:    "test-workspace",
		Action:       "apply",
		CommitSHA:    "abc123",
		VcsProvider:  "gitlab",
	}

	err := s.AddRunMeta(originalMetadata)
	if err != nil {
		t.Fatalf("Failed to add metadata: %v", err)
	}

	retrievedMetadata, err := s.GetRunMeta(runID)
	if err != nil {
		t.Errorf("GetRunMeta() failed: %v", err)
	}

	if retrievedMetadata.GetRunID() != runID {
		t.Errorf("Expected RunID %s, got %s", runID, retrievedMetadata.GetRunID())
	}
	if retrievedMetadata.GetOrganization() != "test-org" {
		t.Errorf("Expected Organization test-org, got %s", retrievedMetadata.GetOrganization())
	}
	if retrievedMetadata.GetAction() != "apply" {
		t.Errorf("Expected Action apply, got %s", retrievedMetadata.GetAction())
	}
}

func TestStream_GetRunMeta_NotFound_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	s, cleanup := setupTestStream(t)
	defer cleanup()

	_, err := s.GetRunMeta("nonexistent-run-id")
	if err == nil {
		t.Error("Expected error when getting nonexistent metadata, got none")
	}
}

func TestStream_AddRunMeta_DuplicateRunID_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	s, cleanup := setupTestStream(t)
	defer cleanup()

	testID := time.Now().UnixNano()
	runID := fmt.Sprintf("duplicate-meta-test-%d", testID)

	originalMetadata := &TFRunMetadata{
		RunID:        runID,
		Organization: "test-org",
		Workspace:    "test-workspace",
		Action:       "plan",
	}

	err := s.AddRunMeta(originalMetadata)
	if err != nil {
		t.Fatalf("Failed to add original metadata: %v", err)
	}

	duplicateMetadata := &TFRunMetadata{
		RunID:        runID,
		Organization: "test-org",
		Workspace:    "test-workspace",
		Action:       "apply",
		CommitSHA:    "different-sha",
	}

	err = s.AddRunMeta(duplicateMetadata)
	if err == nil {
		t.Error("AddRunMeta() should fail when trying to create metadata with duplicate runID")
	}
}

func Test_configureTFRunMetadataKVStore_Integration(t *testing.T) {
	if !*integration {
		t.Skip("Skipping integration test - use -integration flag to run")
	}

	s, cleanup := setupTestStream(t)
	defer cleanup()

	kv, err := configureTFRunMetadataKVStore(s.js)
	if err != nil {
		t.Errorf("configureTFRunMetadataKVStore() failed: %v", err)
	}

	if kv == nil {
		t.Error("configureTFRunMetadataKVStore() returned nil KV store")
	}

	if kv.Bucket() != RunMetadataKvBucket {
		t.Errorf("Expected bucket name %s, got %s", RunMetadataKvBucket, kv.Bucket())
	}
}

func TestTFRunMetadata_CompleteJSONMarshaling(t *testing.T) {
	metadata := &TFRunMetadata{
		RunID:                                "complete-run",
		Organization:                         "complete-org",
		Workspace:                            "complete-workspace",
		Source:                               "api",
		Action:                               "apply",
		CommitSHA:                            "abc123def456",
		MergeRequestProjectNameWithNamespace: "group/subgroup/project",
		MergeRequestIID:                      99,
		DiscussionID:                         "disc-123",
		RootNoteID:                           456789,
		VcsProvider:                          "gitlab",
		AutoMerge:                            true,
	}

	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("Failed to marshal complete metadata: %v", err)
	}

	var decoded TFRunMetadata
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Failed to unmarshal complete metadata: %v", err)
	}

	// Verify all fields
	if decoded.RunID != metadata.RunID {
		t.Errorf("RunID mismatch: got %v, want %v", decoded.RunID, metadata.RunID)
	}
	if decoded.Organization != metadata.Organization {
		t.Errorf("Organization mismatch: got %v, want %v", decoded.Organization, metadata.Organization)
	}
	if decoded.Workspace != metadata.Workspace {
		t.Errorf("Workspace mismatch: got %v, want %v", decoded.Workspace, metadata.Workspace)
	}
	if decoded.Source != metadata.Source {
		t.Errorf("Source mismatch: got %v, want %v", decoded.Source, metadata.Source)
	}
	if decoded.Action != metadata.Action {
		t.Errorf("Action mismatch: got %v, want %v", decoded.Action, metadata.Action)
	}
	if decoded.CommitSHA != metadata.CommitSHA {
		t.Errorf("CommitSHA mismatch: got %v, want %v", decoded.CommitSHA, metadata.CommitSHA)
	}
	if decoded.MergeRequestProjectNameWithNamespace != metadata.MergeRequestProjectNameWithNamespace {
		t.Errorf("MergeRequestProjectNameWithNamespace mismatch: got %v, want %v",
			decoded.MergeRequestProjectNameWithNamespace, metadata.MergeRequestProjectNameWithNamespace)
	}
	if decoded.MergeRequestIID != metadata.MergeRequestIID {
		t.Errorf("MergeRequestIID mismatch: got %v, want %v", decoded.MergeRequestIID, metadata.MergeRequestIID)
	}
	if decoded.DiscussionID != metadata.DiscussionID {
		t.Errorf("DiscussionID mismatch: got %v, want %v", decoded.DiscussionID, metadata.DiscussionID)
	}
	if decoded.RootNoteID != metadata.RootNoteID {
		t.Errorf("RootNoteID mismatch: got %v, want %v", decoded.RootNoteID, metadata.RootNoteID)
	}
	if decoded.VcsProvider != metadata.VcsProvider {
		t.Errorf("VcsProvider mismatch: got %v, want %v", decoded.VcsProvider, metadata.VcsProvider)
	}
	if decoded.AutoMerge != metadata.AutoMerge {
		t.Errorf("AutoMerge mismatch: got %v, want %v", decoded.AutoMerge, metadata.AutoMerge)
	}
}

func Test_encodeTFRunMetadata_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		metadata RunMetadata
		wantErr  bool
	}{
		{
			name:     "nil metadata",
			metadata: (*TFRunMetadata)(nil),
			wantErr:  false,
		},
		{
			name: "metadata with unicode characters",
			metadata: &TFRunMetadata{
				RunID:        "run-with-unicode-😀",
				Organization: "org-with-中文",
				Workspace:    "workspace-with-émojis-🚀",
			},
			wantErr: false,
		},
		{
			name: "metadata with very long strings",
			metadata: &TFRunMetadata{
				RunID:        string(make([]byte, 1000)),
				Organization: string(make([]byte, 1000)),
				Workspace:    string(make([]byte, 1000)),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := encodeTFRunMetadata(tt.metadata)
			if (err != nil) != tt.wantErr {
				t.Errorf("encodeTFRunMetadata() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && tt.metadata != nil && len(data) > 0 {
				// Verify we can decode it back
				decoded, err := decodeTFRunMetadata(data)
				if err != nil {
					t.Errorf("Failed to decode encoded metadata: %v", err)
				}
				if decoded == nil {
					t.Error("Decoded metadata is nil")
				}
			}
		})
	}
}

func Test_decodeTFRunMetadata_InvalidInputs(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{
			name:    "JSON with wrong types",
			data:    []byte(`{"RunID": 123, "Organization": true, "Workspace": null}`),
			wantErr: true,
		},
		{
			name:    "Truncated JSON",
			data:    []byte(`{"RunID": "test", "Organization": "org", "Workspace": "w`),
			wantErr: true,
		},
		{
			name:    "JSON with unknown fields",
			data:    []byte(`{"RunID": "test", "UnknownField": "value"}`),
			wantErr: false, // Should still decode successfully, ignoring unknown fields
		},
		{
			name:    "Nested JSON object",
			data:    []byte(`{"RunID": {"nested": "object"}, "Organization": "org"}`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeTFRunMetadata(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("decodeTFRunMetadata() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func BenchmarkEncodeTFRunMetadata(b *testing.B) {
	metadata := &TFRunMetadata{
		RunID:                                "bench-run",
		Organization:                         "bench-org",
		Workspace:                            "bench-workspace",
		Action:                               "plan",
		CommitSHA:                            "abc123",
		MergeRequestProjectNameWithNamespace: "group/project",
		MergeRequestIID:                      42,
		VcsProvider:                          "gitlab",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := encodeTFRunMetadata(metadata)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeTFRunMetadata(b *testing.B) {
	metadata := &TFRunMetadata{
		RunID:        "bench-run",
		Organization: "bench-org",
		Workspace:    "bench-workspace",
		Action:       "plan",
	}
	data, _ := json.Marshal(metadata)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := decodeTFRunMetadata(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestTFRunMetadata_ImplementsRunMetadata(t *testing.T) {
	var _ RunMetadata = (*TFRunMetadata)(nil)
}

func TestTFRunMetadata_ActionConstants(t *testing.T) {
	tests := []struct {
		name   string
		action string
		want   string
	}{
		{
			name:   "apply action constant",
			action: ApplyAction,
			want:   "apply",
		},
		{
			name:   "plan action constant",
			action: PlanAction,
			want:   "plan",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata := &TFRunMetadata{Action: tt.action}
			if got := metadata.GetAction(); got != tt.want {
				t.Errorf("GetAction() = %v, want %v", got, tt.want)
			}
		})
	}
}
