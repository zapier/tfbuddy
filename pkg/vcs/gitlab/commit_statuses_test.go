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
