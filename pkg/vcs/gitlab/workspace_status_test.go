package gitlab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/zapier/tfbuddy/pkg/vcs"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

func TestBuildStateForCoversEveryCommitState(t *testing.T) {
	tests := []struct {
		state vcs.CommitState
		want  gogitlab.BuildStateValue
	}{
		{vcs.CommitStatePending, gogitlab.Pending},
		{vcs.CommitStateRunning, gogitlab.Running},
		{vcs.CommitStateSuccess, gogitlab.Success},
		{vcs.CommitStateFailed, gogitlab.Failed},
		{vcs.CommitStateCanceled, gogitlab.Canceled},
		{vcs.CommitStateSkipped, gogitlab.Skipped},
	}
	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			if got := buildStateFor(tt.state); got != tt.want {
				t.Errorf("buildStateFor(%q) = %q, want %q", tt.state, got, tt.want)
			}
		})
	}
}

// GitLab validates description length and rejects anything over 255
// characters, so an untruncated wrapped TFC error would lose the status
// entirely rather than post a long one.
func TestTruncateDescriptionRespectsGitlabLimit(t *testing.T) {
	long := strings.Repeat("a", 400)
	got := truncateDescription(long)
	if len(got) > 255 {
		t.Fatalf("description is %d bytes, GitLab rejects anything over 255", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("truncated description should end with an ellipsis, got %q", got[len(got)-10:])
	}
}

func TestTruncateDescriptionLeavesShortStringsAlone(t *testing.T) {
	const short = "skipped: excluded by TFBuddy configuration"
	if got := truncateDescription(short); got != short {
		t.Errorf("truncateDescription(%q) = %q, want it unchanged", short, got)
	}
}

// Cutting a byte slice mid-rune produces invalid UTF-8, which GitLab rejects.
func TestTruncateDescriptionCutsOnRuneBoundary(t *testing.T) {
	// "é" is two bytes, so a naive s[:252] lands mid-rune for some lengths.
	got := truncateDescription(strings.Repeat("é", 200))
	if !utf8.ValidString(got) {
		t.Fatalf("truncated description is not valid UTF-8: %q", got)
	}
	if len(got) > 255 {
		t.Fatalf("description is %d bytes, want <= 255", len(got))
	}
}

// TestSetWorkspaceStatusPostsSkippedToTheMRPipeline is the core of this
// change: a workspace TFBuddy declines to run must still appear in the
// merge request's pipeline, as a skipped job rather than a missing one.
func TestSetWorkspaceStatusPostsSkippedToTheMRPipeline(t *testing.T) {
	const wantPipelineID = 4242

	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.RawPath
		if path == "" {
			path = r.URL.Path
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		// The pipeline lookup must succeed, otherwise the backoff in
		// SetWorkspaceStatus retries for 30 seconds and the test hangs.
		case strings.HasSuffix(path, "/pipelines"):
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": wantPipelineID, "source": "merge_request_event", "status": "running"},
			})
		case strings.Contains(path, "/statuses/"):
			json.NewDecoder(r.Body).Decode(&got)
			json.NewEncoder(w).Encode(map[string]any{
				"id": 1, "name": "TFC/plan/svc-a", "sha": "abc123", "status": "skipped",
				"author": map[string]any{"username": "tfbuddy"},
			})
		default:
			t.Logf("unhandled request: %s %s", r.Method, path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL)

	err := client.SetWorkspaceStatus(context.Background(), vcs.WorkspaceStatus{
		Project:         testProject,
		CommitSHA:       "abc123",
		MergeRequestIID: 101,
		Workspace:       "svc-a",
		Action:          "plan",
		State:           vcs.CommitStateSkipped,
		Description:     "skipped: excluded by TFBuddy configuration",
	})
	if err != nil {
		t.Fatal(err)
	}

	if got["state"] != "skipped" {
		t.Errorf("state = %v, want skipped", got["state"])
	}
	if got["name"] != "TFC/plan/svc-a" {
		t.Errorf("name = %v, want TFC/plan/svc-a", got["name"])
	}
	if got["description"] != "skipped: excluded by TFBuddy configuration" {
		t.Errorf("description = %v", got["description"])
	}
	// Guards the pipeline-ID shadowing regression this assertion inherited from
	// TestUpdateStatusAttachesPipelineID: when the resolved ID is discarded and
	// the status goes out with PipelineID == nil, GitLab cannot associate it
	// with the current MR pipeline, leaving the "apply" check stuck and status
	// links pointing at stale runs.
	if got["pipeline_id"] != float64(wantPipelineID) {
		t.Errorf("pipeline_id = %v, want %d", got["pipeline_id"], wantPipelineID)
	}
}

// pipelinelessServer serves a project with no pipelines at all — the shape of
// a Terraform repo that has .tfbuddy.yaml but no .gitlab-ci.yml.
func pipelinelessServer(t *testing.T, lookups *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.RawPath
		if path == "" {
			path = r.URL.Path
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(path, "/pipelines"):
			*lookups++
			json.NewEncoder(w).Encode([]map[string]any{})
		case strings.Contains(path, "/statuses/"):
			json.NewEncoder(w).Encode(map[string]any{
				"id": 1, "name": "TFC/plan/svc-a", "sha": "abc123", "status": "skipped",
				"author": map[string]any{"username": "tfbuddy"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// SetWorkspaceStatus runs synchronously inside the merge-request webhook
// handler, whose JetStream AckWait is 30s. A long pipeline-ID backoff there
// gets the delivery redelivered and the whole MR reprocessed, producing the
// duplicate discussions and duplicate TFC runs that the per-workspace fan-out
// exists to prevent. Giving up quickly is correct: the status posts anyway,
// onto the implicit "external" pipeline.
func TestSetWorkspaceStatusGivesUpQuicklyWhenNoPipelineExists(t *testing.T) {
	var lookups int
	srv := pipelinelessServer(t, &lookups)
	defer srv.Close()

	client := newTestClient(t, srv.URL)

	start := time.Now()
	err := client.SetWorkspaceStatus(context.Background(), vcs.WorkspaceStatus{
		Project:   testProject,
		CommitSHA: "abc123",
		Workspace: "svc-a",
		Action:    "plan",
		State:     vcs.CommitStateSkipped,
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("the status must still post without a pipeline ID: %v", err)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("pipeline lookup blocked for %s; that exceeds the 30s JetStream AckWait "+
			"budget once clone time is added, risking redelivery and duplicate runs "+
			"(%d lookups)", elapsed, lookups)
	}
}

// backoff.Retry ignores context, so without an explicit context-aware backoff
// nothing upstream can bound this call.
func TestSetWorkspaceStatusHonorsContextCancellation(t *testing.T) {
	var lookups int
	srv := pipelinelessServer(t, &lookups)
	defer srv.Close()

	client := newTestClient(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_ = client.SetWorkspaceStatus(ctx, vcs.WorkspaceStatus{
		Project:   testProject,
		CommitSHA: "abc123",
		Workspace: "svc-a",
		Action:    "plan",
		State:     vcs.CommitStateFailed,
	})
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("a cancelled context did not shorten the call: took %s", elapsed)
	}
}
