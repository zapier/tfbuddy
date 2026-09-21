package vcs

import "testing"

type stubPipeline struct {
	id     int
	source string
}

func (s stubPipeline) GetID() int        { return s.id }
func (s stubPipeline) GetSource() string { return s.source }
func (s stubPipeline) GetStatus() string { return "" }
func (s stubPipeline) GetWebURL() string { return "" }

func TestSelectPipelineForCommit(t *testing.T) {
	tests := []struct {
		name      string
		pipelines []ProjectPipeline
		wantID    int
		wantNil   bool
	}{
		{
			name:    "no pipelines",
			wantNil: true,
		},
		{
			name: "prefers the merge request pipeline over a later push pipeline",
			pipelines: []ProjectPipeline{
				stubPipeline{id: 1, source: "push"},
				stubPipeline{id: 2, source: PipelineSourceMergeRequestEvent},
				stubPipeline{id: 3, source: "push"},
			},
			wantID: 2,
		},
		{
			// GitLab lists pipelines newest first (order_by=id, sort=desc), so the
			// fallback has to take the head of the list, not the tail.
			name: "falls back to the newest pipeline when none is a merge request pipeline",
			pipelines: []ProjectPipeline{
				stubPipeline{id: 9, source: "push"},
				stubPipeline{id: 5, source: "schedule"},
				stubPipeline{id: 1, source: "push"},
			},
			wantID: 9,
		},
		{
			name: "single non merge request pipeline",
			pipelines: []ProjectPipeline{
				stubPipeline{id: 3, source: "push"},
			},
			wantID: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SelectPipelineForCommit(tt.pipelines)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got pipeline %d", got.GetID())
				}
				return
			}
			if got == nil {
				t.Fatalf("expected pipeline %d, got nil", tt.wantID)
			}
			if got.GetID() != tt.wantID {
				t.Errorf("expected pipeline %d, got %d", tt.wantID, got.GetID())
			}
		})
	}
}

type stubJobStatus struct{ name string }

func (s stubJobStatus) GetName() string       { return s.name }
func (s stubJobStatus) GetStatus() string     { return "" }
func (s stubJobStatus) GetPipelineID() int    { return 0 }
func (s stubJobStatus) GetAllowFailure() bool { return false }

func TestIsTFBuddyCommitStatus(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "TFC/plan/service-tfbuddy", want: true},
		{name: "TFC/apply/service-tfbuddy", want: true},
		{name: "lint", want: false},
		{name: "terraform-fmt", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTFBuddyCommitStatus(stubJobStatus{name: tt.name}); got != tt.want {
				t.Errorf("IsTFBuddyCommitStatus(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}
