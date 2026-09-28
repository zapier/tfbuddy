package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zapier/tfbuddy/pkg/mocks"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
	"go.uber.org/mock/gomock"
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

// newNoRetryTestClient points a GitlabClient at server. go-gitlab's own HTTP retries
// are disabled so each test observes only TFBuddy's retry loop.
func newNoRetryTestClient(t *testing.T, server *httptest.Server) *GitlabClient {
	t.Helper()
	api, err := gogitlab.NewClient("token",
		gogitlab.WithBaseURL(server.URL+"/api/v4"),
		gogitlab.WithoutRetries(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return &GitlabClient{client: api}
}

func TestRetriesStopWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		// The caller gives up while the first attempt is in flight. Without
		// the context the retry loop would sleep and try again.
		cancel()
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	_, err := newNoRetryTestClient(t, server).GetRepoFile(ctx, "zapier/tfbuddy", "README.md", "main")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("expected 1 request after cancellation, got %d", got)
	}
}

func TestCloneMergeRequestRetriesProjectLookup(t *testing.T) {
	var projectRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v4/projects/") {
			// The clone itself; failing it keeps the test off the network.
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if projectRequests.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"http_url_to_repo":"` + "http://" + r.Host + `/zapier/tfbuddy.git"}`))
	}))
	t.Cleanup(server.Close)

	mr := mocks.NewMockMR(gomock.NewController(t))
	mr.EXPECT().GetSourceBranch().Return("feature").AnyTimes()

	_, err := newNoRetryTestClient(t, server).CloneMergeRequest(context.Background(), "zapier/tfbuddy", mr, t.TempDir())
	if got := projectRequests.Load(); got != 2 {
		t.Fatalf("expected the project lookup to be retried once, got %d requests", got)
	}
	if err != nil && strings.Contains(err.Error(), "unable to read project details") {
		t.Fatalf("project lookup should have succeeded on retry: %v", err)
	}
}
