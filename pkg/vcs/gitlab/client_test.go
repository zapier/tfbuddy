package gitlab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

func TestMergeMRAtSHAPinsCommitAndUsesGitLabAutoMerge(t *testing.T) {
	const expectedSHA = "commit-123"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/merge_requests/101/merge") {
			t.Errorf("unexpected request path %s", r.URL.Path)
		}
		var body struct {
			SHA                       string `json:"sha"`
			MergeWhenPipelineSucceeds bool   `json:"merge_when_pipeline_succeeds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body.SHA != expectedSHA {
			t.Errorf("expected SHA %q, got %q", expectedSHA, body.SHA)
		}
		if !body.MergeWhenPipelineSucceeds {
			t.Error("expected GitLab auto-merge to wait for the pipeline")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"iid":101,"state":"opened"}`))
	}))
	t.Cleanup(server.Close)

	api, err := gogitlab.NewClient("token", gogitlab.WithBaseURL(server.URL+"/api/v4"))
	if err != nil {
		t.Fatal(err)
	}
	client := &GitlabClient{client: api}
	if err := client.MergeMRAtSHA(context.Background(), 101, "zapier/tfbuddy", expectedSHA); err != nil {
		t.Fatal(err)
	}
}
