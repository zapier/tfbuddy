package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	gogithub "github.com/google/go-github/v69/github"
	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/utils"
)

const (
	testOwner    = "test-org"
	testRepo     = "test-repo"
	testFullName = testOwner + "/" + testRepo
	testPRID     = 1
	ghBotUser    = "tfbuddy-bot[bot]"
)

func buildGHCommentBody(workspace, action, runID, runURL, status string) string {
	return fmt.Sprintf(
		"\n### Terraform Cloud\n**Workspace**: `%s`<br>\n**Command**: %s <br>\n**Status**: `%s`<br>\n**Run URL**: [%s](%s) <br>\n\n%s",
		workspace, action, status, runID, runURL,
		utils.FormatTFBuddyMarker(workspace, action),
	)
}

type fakeComment struct {
	ID   int64
	Body string
	User string
}

func setupGHTestServer(t *testing.T, comments []fakeComment, deletedIDs *[]int64, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/issues/%d/comments", testOwner, testRepo, testPRID), func(w http.ResponseWriter, r *http.Request) {
		var result []map[string]interface{}
		for _, c := range comments {
			result = append(result, map[string]interface{}{
				"id":         c.ID,
				"body":       c.Body,
				"user":       map[string]interface{}{"login": c.User},
				"created_at": "2026-03-31T18:00:00Z",
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})

	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":    1,
			"login": ghBotUser,
		})
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/comments/") {
			parts := strings.Split(r.URL.Path, "/comments/")
			if len(parts) == 2 {
				id, _ := strconv.ParseInt(parts[1], 10, 64)
				mu.Lock()
				*deletedIDs = append(*deletedIDs, id)
				mu.Unlock()
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		t.Logf("unhandled request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	})

	return httptest.NewServer(mux)
}

func newGHTestClient(t *testing.T, serverURL string) *Client {
	t.Helper()
	ghClient := gogithub.NewClient(nil)
	ghClient.BaseURL, _ = ghClient.BaseURL.Parse(serverURL + "/")
	return &Client{client: ghClient, ctx: context.Background(), token: "test-token"}
}

func TestGH_GetOldRunUrls_SingleWorkspace_DeletesOlderPlan(t *testing.T) {
	t.Setenv("TFBUDDY_DELETE_OLD_COMMENTS", "true")
	config.Reload()

	currentID := int64(300)
	comments := []fakeComment{
		{ID: 100, Body: buildGHCommentBody("ws-a", "plan", "run-old1", "https://tfc/run-old1", "planned_and_finished"), User: ghBotUser},
		{ID: 200, Body: buildGHCommentBody("ws-a", "plan", "run-old2", "https://tfc/run-old2", "planned_and_finished"), User: ghBotUser},
		{ID: currentID, Body: buildGHCommentBody("ws-a", "plan", "run-current", "https://tfc/run-current", "planned_and_finished"), User: ghBotUser},
	}

	var deletedIDs []int64
	var mu sync.Mutex
	server := setupGHTestServer(t, comments, &deletedIDs, &mu)
	defer server.Close()

	client := newGHTestClient(t, server.URL)
	_, err := client.GetOldRunUrls(context.Background(), testPRID, testFullName, int(currentID), "ws-a", "plan")
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(deletedIDs) != 2 {
		t.Fatalf("expected 2 comments deleted, got %d: %v", len(deletedIDs), deletedIDs)
	}
	deleted := map[int64]bool{}
	for _, id := range deletedIDs {
		deleted[id] = true
	}
	if !deleted[100] || !deleted[200] {
		t.Fatalf("expected comments 100 and 200 deleted, got %v", deletedIDs)
	}
	if deleted[currentID] {
		t.Fatal("current comment should NOT be deleted")
	}
}

func TestGH_GetOldRunUrls_MultiWorkspace_KeepsOnePerWorkspaceAndAction(t *testing.T) {
	t.Setenv("TFBUDDY_DELETE_OLD_COMMENTS", "true")
	config.Reload()

	currentPlanA := int64(500)
	comments := []fakeComment{
		// Old plan for ws-a
		{ID: 100, Body: buildGHCommentBody("ws-a", "plan", "run-a-old", "https://tfc/run-a-old", "planned_and_finished"), User: ghBotUser},
		// Plan for ws-b (different workspace, keep)
		{ID: 200, Body: buildGHCommentBody("ws-b", "plan", "run-b", "https://tfc/run-b", "planned_and_finished"), User: ghBotUser},
		// Apply for ws-a (different action, keep)
		{ID: 300, Body: buildGHCommentBody("ws-a", "apply", "run-a-apply", "https://tfc/run-a-apply", "applied"), User: ghBotUser},
		// Human comment (keep)
		{ID: 400, Body: "LGTM!", User: "human-user"},
		// Current plan for ws-a
		{ID: currentPlanA, Body: buildGHCommentBody("ws-a", "plan", "run-a-current", "https://tfc/run-a-current", "planned_and_finished"), User: ghBotUser},
	}

	var deletedIDs []int64
	var mu sync.Mutex
	server := setupGHTestServer(t, comments, &deletedIDs, &mu)
	defer server.Close()

	client := newGHTestClient(t, server.URL)
	_, err := client.GetOldRunUrls(context.Background(), testPRID, testFullName, int(currentPlanA), "ws-a", "plan")
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(deletedIDs) != 1 {
		t.Fatalf("expected 1 comment deleted, got %d: %v", len(deletedIDs), deletedIDs)
	}
	if deletedIDs[0] != 100 {
		t.Fatalf("expected comment 100 deleted, got %v", deletedIDs)
	}
}

func TestGH_GetOldRunUrls_ApplyAction_OnlyDeletesMatchingApply(t *testing.T) {
	t.Setenv("TFBUDDY_DELETE_OLD_COMMENTS", "true")
	config.Reload()

	currentApplyA := int64(500)
	comments := []fakeComment{
		// Old apply ws-a
		{ID: 100, Body: buildGHCommentBody("ws-a", "apply", "run-apply-old", "https://tfc/run-apply-old", "applied"), User: ghBotUser},
		// Plan ws-a (different action, keep)
		{ID: 200, Body: buildGHCommentBody("ws-a", "plan", "run-plan", "https://tfc/run-plan", "planned_and_finished"), User: ghBotUser},
		// Apply ws-b (different ws, keep)
		{ID: 300, Body: buildGHCommentBody("ws-b", "apply", "run-b-apply", "https://tfc/run-b-apply", "applied"), User: ghBotUser},
		// Current apply ws-a
		{ID: currentApplyA, Body: buildGHCommentBody("ws-a", "apply", "run-apply-current", "https://tfc/run-apply-current", "applied"), User: ghBotUser},
	}

	var deletedIDs []int64
	var mu sync.Mutex
	server := setupGHTestServer(t, comments, &deletedIDs, &mu)
	defer server.Close()

	client := newGHTestClient(t, server.URL)
	_, err := client.GetOldRunUrls(context.Background(), testPRID, testFullName, int(currentApplyA), "ws-a", "apply")
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(deletedIDs) != 1 {
		t.Fatalf("expected 1 deletion, got %d: %v", len(deletedIDs), deletedIDs)
	}
	if deletedIDs[0] != 100 {
		t.Fatalf("expected comment 100 deleted, got %v", deletedIDs)
	}
}

func TestGH_GetOldRunUrls_DeleteDisabled_NoDeletion(t *testing.T) {
	t.Setenv("TFBUDDY_DELETE_OLD_COMMENTS", "false")
	config.Reload()

	currentID := int64(200)
	comments := []fakeComment{
		{ID: 100, Body: buildGHCommentBody("ws-a", "plan", "run-old", "https://tfc/run-old", "planned_and_finished"), User: ghBotUser},
		{ID: currentID, Body: buildGHCommentBody("ws-a", "plan", "run-current", "https://tfc/run-current", "planned_and_finished"), User: ghBotUser},
	}

	var deletedIDs []int64
	var mu sync.Mutex
	server := setupGHTestServer(t, comments, &deletedIDs, &mu)
	defer server.Close()

	client := newGHTestClient(t, server.URL)
	result, err := client.GetOldRunUrls(context.Background(), testPRID, testFullName, int(currentID), "ws-a", "plan")
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(deletedIDs) != 0 {
		t.Fatalf("expected no deletions, got %d", len(deletedIDs))
	}
	if !strings.Contains(result, "run-old") {
		t.Fatalf("expected old run URL collected, got %q", result)
	}
}

func TestGH_GetOldRunUrls_FullMultiWorkspaceScenario(t *testing.T) {
	t.Setenv("TFBUDDY_DELETE_OLD_COMMENTS", "true")
	config.Reload()

	currentPlanA := int64(1000)
	currentPlanB := int64(1100)
	currentApplyA := int64(1200)

	comments := []fakeComment{
		{ID: 10, Body: buildGHCommentBody("ws-a", "plan", "run-a-p1", "https://tfc/run-a-p1", "planned_and_finished"), User: ghBotUser},
		{ID: 20, Body: buildGHCommentBody("ws-a", "plan", "run-a-p2", "https://tfc/run-a-p2", "planned_and_finished"), User: ghBotUser},
		{ID: 30, Body: buildGHCommentBody("ws-b", "plan", "run-b-p1", "https://tfc/run-b-p1", "planned_and_finished"), User: ghBotUser},
		{ID: 40, Body: buildGHCommentBody("ws-a", "apply", "run-a-ap1", "https://tfc/run-a-ap1", "applied"), User: ghBotUser},
		{ID: currentPlanA, Body: buildGHCommentBody("ws-a", "plan", "run-a-latest", "https://tfc/run-a-latest", "planned_and_finished"), User: ghBotUser},
		{ID: currentPlanB, Body: buildGHCommentBody("ws-b", "plan", "run-b-latest", "https://tfc/run-b-latest", "planned_and_finished"), User: ghBotUser},
		{ID: currentApplyA, Body: buildGHCommentBody("ws-a", "apply", "run-a-ap-latest", "https://tfc/run-a-ap-latest", "applied"), User: ghBotUser},
	}

	t.Run("cleanup plan for ws-a", func(t *testing.T) {
		var deletedIDs []int64
		var mu sync.Mutex
		server := setupGHTestServer(t, comments, &deletedIDs, &mu)
		defer server.Close()

		client := newGHTestClient(t, server.URL)
		_, err := client.GetOldRunUrls(context.Background(), testPRID, testFullName, int(currentPlanA), "ws-a", "plan")
		if err != nil {
			t.Fatal(err)
		}

		mu.Lock()
		defer mu.Unlock()
		deleted := map[int64]bool{}
		for _, id := range deletedIDs {
			deleted[id] = true
		}
		if !deleted[10] || !deleted[20] {
			t.Fatalf("expected 10,20 deleted, got %v", deletedIDs)
		}
		for _, id := range []int64{30, 40, currentPlanA, currentPlanB, currentApplyA} {
			if deleted[id] {
				t.Fatalf("comment %d should NOT be deleted", id)
			}
		}
	})

	t.Run("cleanup plan for ws-b", func(t *testing.T) {
		var deletedIDs []int64
		var mu sync.Mutex
		server := setupGHTestServer(t, comments, &deletedIDs, &mu)
		defer server.Close()

		client := newGHTestClient(t, server.URL)
		_, err := client.GetOldRunUrls(context.Background(), testPRID, testFullName, int(currentPlanB), "ws-b", "plan")
		if err != nil {
			t.Fatal(err)
		}

		mu.Lock()
		defer mu.Unlock()
		deleted := map[int64]bool{}
		for _, id := range deletedIDs {
			deleted[id] = true
		}
		if !deleted[30] {
			t.Fatalf("expected 30 deleted, got %v", deletedIDs)
		}
		for _, id := range []int64{10, 20, 40, currentPlanA, currentPlanB, currentApplyA} {
			if deleted[id] {
				t.Fatalf("comment %d should NOT be deleted", id)
			}
		}
	})

	t.Run("cleanup apply for ws-a", func(t *testing.T) {
		var deletedIDs []int64
		var mu sync.Mutex
		server := setupGHTestServer(t, comments, &deletedIDs, &mu)
		defer server.Close()

		client := newGHTestClient(t, server.URL)
		_, err := client.GetOldRunUrls(context.Background(), testPRID, testFullName, int(currentApplyA), "ws-a", "apply")
		if err != nil {
			t.Fatal(err)
		}

		mu.Lock()
		defer mu.Unlock()
		deleted := map[int64]bool{}
		for _, id := range deletedIDs {
			deleted[id] = true
		}
		if !deleted[40] {
			t.Fatalf("expected 40 deleted, got %v", deletedIDs)
		}
		for _, id := range []int64{10, 20, 30, currentPlanA, currentPlanB, currentApplyA} {
			if deleted[id] {
				t.Fatalf("comment %d should NOT be deleted", id)
			}
		}
	})
}
