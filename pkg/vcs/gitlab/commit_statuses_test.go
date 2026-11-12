package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestGetCommitJobStatusesPaginates guards against the gate failing open: a
// pipeline with more jobs than one page would otherwise hide a failing job,
// and an apply that should be blocked would be allowed through.
func TestGetCommitJobStatusesPaginates(t *testing.T) {
	const totalStatuses = 150
	const perPage = 100

	var requestedPages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.RawPath
		if path == "" {
			path = r.URL.Path
		}
		if !strings.HasSuffix(path, "/statuses") {
			t.Logf("unhandled request: %s %s", r.Method, path)
			w.WriteHeader(http.StatusNotFound)
			return
		}

		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		}
		requestedPages = append(requestedPages, r.URL.Query().Get("page"))

		start := (page - 1) * perPage
		end := min(start+perPage, totalStatuses)

		result := make([]map[string]any, 0, end-start)
		for i := start; i < end; i++ {
			result = append(result, map[string]any{
				"id":            i,
				"name":          fmt.Sprintf("job-%d", i),
				"status":        "success",
				"pipeline_id":   900,
				"allow_failure": false,
			})
		}

		if end < totalStatuses {
			w.Header().Set("X-Next-Page", strconv.Itoa(page+1))
		} else {
			w.Header().Set("X-Next-Page", "")
		}
		w.Header().Set("X-Page", strconv.Itoa(page))
		w.Header().Set("X-Per-Page", strconv.Itoa(perPage))
		w.Header().Set("X-Total", strconv.Itoa(totalStatuses))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL)

	statuses, err := client.GetCommitJobStatuses(context.Background(), testProject, "abc123")
	if err != nil {
		t.Fatal(err)
	}

	if len(statuses) != totalStatuses {
		t.Fatalf("expected %d statuses across all pages, got %d (pages requested: %v)",
			totalStatuses, len(statuses), requestedPages)
	}
	if got := statuses[totalStatuses-1].GetName(); got != fmt.Sprintf("job-%d", totalStatuses-1) {
		t.Errorf("expected the last status from the final page, got %q", got)
	}
}

func TestGetProjectSettingsReadsOnlyAllowMergeIfPipelineSucceeds(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "enabled", want: true},
		{name: "disabled", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"id":                                    7,
					"path_with_namespace":                   testProject,
					"only_allow_merge_if_pipeline_succeeds": tt.want,
				})
			}))
			defer srv.Close()

			client := newTestClient(t, srv.URL)

			settings, err := client.GetProjectSettings(context.Background(), testProject)
			if err != nil {
				t.Fatal(err)
			}
			if got := settings.OnlyAllowMergeIfPipelineSucceeds(); got != tt.want {
				t.Errorf("OnlyAllowMergeIfPipelineSucceeds() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestTransportErrorsSurfaceAsErrors guards the nil-response path: GitLab
// returns a nil *Response when the request fails before headers arrive, which
// must surface as an error rather than a nil value with a nil error.
func TestTransportErrorsSurfaceAsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	unreachableURL := srv.URL
	srv.Close()

	client := newTestClient(t, unreachableURL)

	t.Run("GetProjectSettings", func(t *testing.T) {
		settings, err := client.GetProjectSettings(context.Background(), testProject)
		if err == nil {
			t.Fatalf("expected an error for an unreachable host, got settings=%v err=nil", settings)
		}
		if settings != nil {
			t.Errorf("expected nil settings alongside the error, got %v", settings)
		}
	})

	t.Run("GetCommitJobStatuses", func(t *testing.T) {
		statuses, err := client.GetCommitJobStatuses(context.Background(), testProject, "abc123")
		if err == nil {
			t.Fatalf("expected an error for an unreachable host, got %d statuses and err=nil", len(statuses))
		}
	})

	t.Run("GetPipelinesForCommit", func(t *testing.T) {
		pipelines, err := client.GetPipelinesForCommit(context.Background(), testProject, "abc123")
		if err == nil {
			t.Fatalf("expected an error for an unreachable host, got %d pipelines and err=nil", len(pipelines))
		}
	})
}

// The author of each status is what tells TFBuddy's own TFC/* statuses apart
// from ones anyone else posts with the same name.
func TestGetCommitJobStatusesReadsAuthor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 1, "name": "TFC/plan/a", "status": "success", "pipeline_id": 900, "author": map[string]any{"username": "tfbuddy"}},
			{"id": 2, "name": "TFC/class", "status": "success", "pipeline_id": 900, "author": map[string]any{"username": "a-developer"}},
		})
	}))
	defer srv.Close()

	statuses, err := newTestClient(t, srv.URL).GetCommitJobStatuses(context.Background(), testProject, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 2 || statuses[0].GetAuthor() != "tfbuddy" || statuses[1].GetAuthor() != "a-developer" {
		t.Fatalf("unexpected authors: %v", statuses)
	}
}

// TFBuddy's account is read once and then cached; a failed read is not.
func TestGetAuthenticatedUsernameIsCached(t *testing.T) {
	calls := 0
	fail := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if fail {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": 7, "username": "tfbuddy", "name": "TFBuddy Bot"})
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := client.GetAuthenticatedUsername(ctx); err == nil {
		t.Fatal("expected an error while GitLab refuses the read")
	}
	fail = false
	calls = 0
	for range 3 {
		got, err := client.GetAuthenticatedUsername(context.Background())
		if err != nil || got != "tfbuddy" {
			t.Fatalf("GetAuthenticatedUsername() = %q, %v, want tfbuddy", got, err)
		}
	}
	if name, err := client.GetAuthenticatedAccountName(context.Background()); err != nil || name != "TFBuddy Bot" {
		t.Fatalf("GetAuthenticatedAccountName() = %q, %v, want the display name", name, err)
	}
	if calls != 1 {
		t.Errorf("expected one read of the current user once it succeeded, got %d requests", calls)
	}
}

func TestGetMergeRequestApprovalsReadsApprovers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"approved": true,
			"approved_by": []map[string]any{
				{"user": map[string]any{"username": "approver-bot"}},
				{"user": map[string]any{"username": "alice"}},
			},
		})
	}))
	defer srv.Close()

	approvals, err := newTestClient(t, srv.URL).GetMergeRequestApprovals(context.Background(), 101, testProject)
	if err != nil {
		t.Fatal(err)
	}
	if got := approvals.GetApprovers(); len(got) != 2 || got[0] != "approver-bot" || got[1] != "alice" {
		t.Errorf("GetApprovers() = %q, want both approvers", got)
	}
}

// The required-jobs gate reads the pipeline's CI jobs, across pages, and only
// their latest runs.
func TestGetPipelineJobsPaginates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.RawPath
		if path == "" {
			path = r.URL.Path
		}
		if !strings.HasSuffix(path, "/pipelines/900/jobs") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("include_retried") != "false" {
			t.Errorf("include_retried = %q, want false", r.URL.Query().Get("include_retried"))
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			json.NewEncoder(w).Encode([]map[string]any{{"id": 2, "name": "aa:code-review", "status": "manual"}})
			return
		}
		w.Header().Set("X-Next-Page", "2")
		json.NewEncoder(w).Encode([]map[string]any{{"id": 1, "name": "lint", "status": "success"}})
	}))
	defer srv.Close()

	jobs, err := newTestClient(t, srv.URL).GetPipelineJobs(context.Background(), testProject, 900)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[1].GetName() != "aa:code-review" || jobs[1].GetStatus() != "manual" || jobs[0].GetID() != 1 {
		t.Fatalf("unexpected jobs %+v", jobs)
	}
}
