package gitlab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/go-tfe"
	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.uber.org/mock/gomock"
)

// client-go v0.129.0 drops approved_at, which is the whole point of automode's
// approval check, so the client decodes the response itself.
func TestGetMergeRequestApprovalStateReadsApprovedAt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.RawPath
		if path == "" {
			path = r.URL.Path
		}
		if !strings.HasSuffix(path, "/merge_requests/101/approvals") {
			t.Errorf("unexpected request %s %s", r.Method, path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"approved": true,
			"approved_by": [
				{"user": {"username": "approver-bot"}, "approved_at": "2026-11-02T10:01:00.000Z"},
				{"user": {"username": "old-gitlab"}}
			]
		}`))
	}))
	defer srv.Close()

	state, err := newTestClient(t, srv.URL).GetMergeRequestApprovalState(context.Background(), 101, testProject)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Approved || len(state.ApprovedBy) != 2 {
		t.Fatalf("unexpected approval state: %+v", state)
	}
	want := time.Date(2026, 11, 2, 10, 1, 0, 0, time.UTC)
	if a := state.ApprovedBy[0]; a.Username != "approver-bot" || a.ApprovedAt == nil || !a.ApprovedAt.Equal(want) {
		t.Errorf("first approval = %+v, want approver-bot at %s", a, want)
	}
	if a := state.ApprovedBy[1]; a.ApprovedAt != nil {
		t.Errorf("an approval without approved_at must have no time, got %s", a.ApprovedAt)
	}
}

func TestSetClassStatusPostsTFCClass(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.RawPath
		if path == "" {
			path = r.URL.Path
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(path, "/pipelines"):
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": 900, "source": "merge_request_event", "status": "running"},
			})
		case strings.Contains(path, "/statuses/"):
			json.NewDecoder(r.Body).Decode(&got)
			json.NewEncoder(w).Encode(map[string]any{"id": 1, "name": "TFC/class", "sha": "abc123", "author": map[string]any{"username": "tfbuddy"}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	err := newTestClient(t, srv.URL).SetClassStatus(context.Background(), vcs.ClassStatus{
		Project: testProject, CommitSHA: "abc123", MergeRequestIID: 101,
		State: vcs.CommitStateSuccess, Description: "class tags: every plan passed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["name"] != "TFC/class" || got["state"] != "success" || got["pipeline_id"] != float64(900) {
		t.Errorf("unexpected status posted: %v", got)
	}
}

type fakeJobStatus struct{ name, status string }

func (f fakeJobStatus) GetName() string       { return f.name }
func (f fakeJobStatus) GetStatus() string     { return f.status }
func (f fakeJobStatus) GetPipelineID() int    { return 900 }
func (f fakeJobStatus) GetAllowFailure() bool { return false }

func TestClassResult(t *testing.T) {
	plan := func(ws, status string) vcs.CommitJobStatus { return fakeJobStatus{"TFC/plan/" + ws, status} }
	tests := []struct {
		name      string
		statuses  []vcs.CommitJobStatus
		wantState vcs.CommitState
		wantDesc  string
	}{
		{
			name:      "every plan passed",
			statuses:  []vcs.CommitJobStatus{plan("a", "success"), plan("b", "success"), fakeJobStatus{"TFC/apply/a", "failed"}},
			wantState: vcs.CommitStateSuccess,
			wantDesc:  "class tags: every plan passed",
		},
		{
			name:     "a plan still running",
			statuses: []vcs.CommitJobStatus{plan("a", "success"), plan("b", "running")},
		},
		{
			name:     "a plan not posted yet",
			statuses: []vcs.CommitJobStatus{plan("a", "success")},
		},
		{
			name:      "a failed plan fails the class even while others run",
			statuses:  []vcs.CommitJobStatus{plan("a", "failed"), plan("b", "running")},
			wantState: vcs.CommitStateFailed,
			wantDesc:  "the plan for a did not pass; the MR falls back to reviewed",
		},
		{
			name:      "a skipped workspace is not a clean one",
			statuses:  []vcs.CommitJobStatus{plan("a", "success"), plan("b", "skipped")},
			wantState: vcs.CommitStateFailed,
			wantDesc:  "the plan for b did not pass",
		},
		{
			name:      "the worst of several statuses counts",
			statuses:  []vcs.CommitJobStatus{plan("a", "success"), plan("b", "success"), plan("b", "failed")},
			wantState: vcs.CommitStateFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, desc := classResult("tags", []string{"a", "b"}, tt.statuses)
			if state != tt.wantState {
				t.Fatalf("state = %q, want %q", state, tt.wantState)
			}
			if !strings.Contains(desc, tt.wantDesc) {
				t.Errorf("description = %q, want it to contain %q", desc, tt.wantDesc)
			}
		})
	}
}

func TestUpdateClassStatusAfterLastPlan(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockGitClient(ctrl)
	r := &RunStatusUpdater{cfg: config.C, client: client}
	rmd := &runstream.TFRunMetadata{
		Action: runstream.PlanAction, Workspace: "a", CommitSHA: "abc123",
		MergeRequestProjectNameWithNamespace: testProject, MergeRequestIID: 101,
		ChangeClass: "tags", ClassWorkspaces: []string{"a"},
	}

	// A run that has not finished, a person's run and an apply run make no calls.
	r.updateClassStatus(context.Background(), &tfe.Run{Status: tfe.RunPlanning}, rmd)
	r.updateClassStatus(context.Background(), &tfe.Run{Status: tfe.RunPlannedAndFinished}, &runstream.TFRunMetadata{Action: runstream.PlanAction})
	r.updateClassStatus(context.Background(), &tfe.Run{Status: tfe.RunPlannedAndFinished}, &runstream.TFRunMetadata{Action: runstream.ApplyAction, ChangeClass: "tags"})

	client.EXPECT().GetCommitJobStatuses(gomock.Any(), testProject, "abc123").
		Return([]vcs.CommitJobStatus{fakeJobStatus{"TFC/plan/a", "success"}}, nil)
	client.EXPECT().SetClassStatus(gomock.Any(), vcs.ClassStatus{
		Project: testProject, CommitSHA: "abc123", MergeRequestIID: 101,
		State: vcs.CommitStateSuccess, Description: "class tags: every plan passed",
	}).Return(nil)
	r.updateClassStatus(context.Background(), &tfe.Run{Status: tfe.RunPlannedAndFinished}, rmd)
}

// An automode plan that soft-fails a policy fails, whatever
// fail-ci-on-sentinel-soft-fail says, so that its class fails too.
func TestAutomodeSoftFailFailsThePlan(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockGitClient(ctrl)
	r := &RunStatusUpdater{cfg: config.C, client: client}
	var got vcs.WorkspaceStatus
	client.EXPECT().SetWorkspaceStatus(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, s vcs.WorkspaceStatus) error {
		got = s
		return nil
	})

	err := r.updateCommitStatusForRun(context.Background(), &tfe.Run{Status: tfe.RunPolicySoftFailed}, &runstream.TFRunMetadata{
		Action: runstream.PlanAction, Workspace: "a", ChangeClass: "tags",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Action != "plan" || got.State != vcs.CommitStateFailed {
		t.Errorf("status = %+v, want a failed plan", got)
	}
}
