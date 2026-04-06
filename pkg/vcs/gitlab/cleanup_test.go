package gitlab

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

	"github.com/zapier/tfbuddy/pkg/utils"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

const (
	testProject = "test-group/test-project"
	testMRIID   = 1
	botUsername  = "tfbuddy-bot"
)

type fakeNote struct {
	ID       int
	Body     string
	Username string
}

type fakeDiscussion struct {
	ID    string
	Notes []fakeNote
}

func buildNoteBody(workspace, action, runID, runURL, status string) string {
	return fmt.Sprintf(
		"\n### Terraform Cloud\n**Workspace**: `%s`<br>\n**Command**: %s <br>\n**Status**: `%s`<br>\n**Run URL**: [%s](%s) <br>\n\n%s",
		workspace, action, status, runID, runURL,
		utils.FormatTFBuddyMarker(workspace, action),
	)
}

func buildSeedBody(workspace, action, org string) string {
	return fmt.Sprintf(
		"Starting TFC %s for Workspace: `%s/%s`.\n%s",
		action, org, workspace,
		utils.FormatTFBuddyMarker(workspace, action),
	)
}

func setupTestServer(t *testing.T, discussions []fakeDiscussion, deletedNotes *[]int, mu *sync.Mutex) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.RawPath
		if path == "" {
			path = r.URL.Path
		}

		switch {
		case r.Method == http.MethodGet && strings.Contains(path, "/discussions"):
			var result []map[string]interface{}
			for _, d := range discussions {
				var notes []map[string]interface{}
				for _, n := range d.Notes {
					notes = append(notes, map[string]interface{}{
						"id":         n.ID,
						"body":       n.Body,
						"author":     map[string]interface{}{"username": n.Username},
						"created_at": "2026-03-31T18:00:00.000Z",
					})
				}
				result = append(result, map[string]interface{}{
					"id":    d.ID,
					"notes": notes,
				})
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(result)

		case r.Method == http.MethodGet && strings.HasSuffix(path, "/user"):
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":       1,
				"username": botUsername,
			})

		case r.Method == http.MethodDelete && strings.Contains(path, "/notes/"):
			parts := strings.Split(path, "/notes/")
			if len(parts) == 2 {
				noteID, _ := strconv.Atoi(parts[1])
				mu.Lock()
				*deletedNotes = append(*deletedNotes, noteID)
				mu.Unlock()
			}
			w.WriteHeader(http.StatusOK)

		default:
			t.Logf("unhandled request: %s %s", r.Method, path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func newTestClient(t *testing.T, serverURL string) *GitlabClient {
	t.Helper()
	glClient, err := gogitlab.NewClient("test-token", gogitlab.WithBaseURL(serverURL+"/api/v4"))
	if err != nil {
		t.Fatal(err)
	}
	return &GitlabClient{client: glClient, token: "test-token", tokenUser: botUsername}
}

func TestGetOldRunUrls_SingleWorkspace_DeletesOlderPlanKeepsNewest(t *testing.T) {
	t.Setenv("TFBUDDY_DELETE_OLD_COMMENTS", "true")

	currentNoteID := 300
	discussions := []fakeDiscussion{
		{
			ID: "disc-old-1",
			Notes: []fakeNote{
				{ID: 100, Body: buildSeedBody("brave-phoenix", "plan", "zapier"), Username: botUsername},
				{ID: 101, Body: buildNoteBody("brave-phoenix", "plan", "run-old1", "https://tfc/run-old1", "planned_and_finished"), Username: botUsername},
			},
		},
		{
			ID: "disc-old-2",
			Notes: []fakeNote{
				{ID: 200, Body: buildSeedBody("brave-phoenix", "plan", "zapier"), Username: botUsername},
				{ID: 201, Body: buildNoteBody("brave-phoenix", "plan", "run-old2", "https://tfc/run-old2", "planned_and_finished"), Username: botUsername},
			},
		},
		{
			ID: "disc-current",
			Notes: []fakeNote{
				{ID: currentNoteID, Body: buildSeedBody("brave-phoenix", "plan", "zapier"), Username: botUsername},
			},
		},
	}

	var deletedNotes []int
	var mu sync.Mutex
	server := setupTestServer(t, discussions, &deletedNotes, &mu)
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.GetOldRunUrls(context.Background(), testMRIID, testProject, currentNoteID, "brave-phoenix", "plan")
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()

	// disc-old-1 notes (101 reply first, then 100 root) and disc-old-2 notes (201, 200)
	if len(deletedNotes) != 4 {
		t.Fatalf("expected 4 notes deleted, got %d: %v", len(deletedNotes), deletedNotes)
	}

	deleted := map[int]bool{}
	for _, id := range deletedNotes {
		deleted[id] = true
	}
	for _, id := range []int{100, 101, 200, 201} {
		if !deleted[id] {
			t.Fatalf("expected note %d to be deleted", id)
		}
	}
	if deleted[currentNoteID] {
		t.Fatal("current note should NOT be deleted")
	}
}

func TestGetOldRunUrls_MultiWorkspace_KeepsOnePerWorkspaceAndAction(t *testing.T) {
	t.Setenv("TFBUDDY_DELETE_OLD_COMMENTS", "true")

	currentPlanA := 500
	discussions := []fakeDiscussion{
		// Old plan for workspace-a
		{
			ID: "disc-plan-a-old",
			Notes: []fakeNote{
				{ID: 100, Body: buildSeedBody("workspace-a", "plan", "org"), Username: botUsername},
				{ID: 101, Body: buildNoteBody("workspace-a", "plan", "run-a1", "https://tfc/run-a1", "planned_and_finished"), Username: botUsername},
			},
		},
		// Plan for workspace-b (different workspace, should NOT be deleted)
		{
			ID: "disc-plan-b",
			Notes: []fakeNote{
				{ID: 200, Body: buildSeedBody("workspace-b", "plan", "org"), Username: botUsername},
				{ID: 201, Body: buildNoteBody("workspace-b", "plan", "run-b1", "https://tfc/run-b1", "planned_and_finished"), Username: botUsername},
			},
		},
		// Apply for workspace-a (different action, should NOT be deleted)
		{
			ID: "disc-apply-a",
			Notes: []fakeNote{
				{ID: 300, Body: buildSeedBody("workspace-a", "apply", "org"), Username: botUsername},
				{ID: 301, Body: buildNoteBody("workspace-a", "apply", "run-a-apply", "https://tfc/run-a-apply", "applied"), Username: botUsername},
			},
		},
		// A discussion from a different user (should NOT be touched)
		{
			ID: "disc-human",
			Notes: []fakeNote{
				{ID: 400, Body: "Hey, looks good to me!", Username: "human-user"},
			},
		},
		// Current plan for workspace-a (the newest, should be kept)
		{
			ID: "disc-plan-a-current",
			Notes: []fakeNote{
				{ID: currentPlanA, Body: buildSeedBody("workspace-a", "plan", "org"), Username: botUsername},
			},
		},
	}

	var deletedNotes []int
	var mu sync.Mutex
	server := setupTestServer(t, discussions, &deletedNotes, &mu)
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.GetOldRunUrls(context.Background(), testMRIID, testProject, currentPlanA, "workspace-a", "plan")
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()

	// Only disc-plan-a-old notes should be deleted (101 reply, then 100 root)
	if len(deletedNotes) != 2 {
		t.Fatalf("expected 2 notes deleted (old plan for workspace-a), got %d: %v", len(deletedNotes), deletedNotes)
	}

	deleted := map[int]bool{}
	for _, id := range deletedNotes {
		deleted[id] = true
	}
	if !deleted[100] || !deleted[101] {
		t.Fatalf("expected notes 100 and 101 deleted, got %v", deletedNotes)
	}

	// Verify other discussions' notes are untouched
	for _, id := range []int{200, 201, 300, 301, 400, currentPlanA} {
		if deleted[id] {
			t.Fatalf("note %d should NOT have been deleted", id)
		}
	}
}

func TestGetOldRunUrls_ApplyAction_DeletesOlderApplyOnly(t *testing.T) {
	t.Setenv("TFBUDDY_DELETE_OLD_COMMENTS", "true")

	currentApplyA := 500
	discussions := []fakeDiscussion{
		// Old apply for workspace-a
		{
			ID: "disc-apply-a-old",
			Notes: []fakeNote{
				{ID: 100, Body: buildSeedBody("workspace-a", "apply", "org"), Username: botUsername},
				{ID: 101, Body: buildNoteBody("workspace-a", "apply", "run-a-apply-old", "https://tfc/run-a-apply-old", "applied"), Username: botUsername},
			},
		},
		// Plan for workspace-a (different action, should NOT be deleted)
		{
			ID: "disc-plan-a",
			Notes: []fakeNote{
				{ID: 200, Body: buildSeedBody("workspace-a", "plan", "org"), Username: botUsername},
				{ID: 201, Body: buildNoteBody("workspace-a", "plan", "run-a-plan", "https://tfc/run-a-plan", "planned_and_finished"), Username: botUsername},
			},
		},
		// Old apply for workspace-b (different workspace, should NOT be deleted)
		{
			ID: "disc-apply-b",
			Notes: []fakeNote{
				{ID: 300, Body: buildSeedBody("workspace-b", "apply", "org"), Username: botUsername},
			},
		},
		// Current apply for workspace-a
		{
			ID: "disc-apply-a-current",
			Notes: []fakeNote{
				{ID: currentApplyA, Body: buildSeedBody("workspace-a", "apply", "org"), Username: botUsername},
			},
		},
	}

	var deletedNotes []int
	var mu sync.Mutex
	server := setupTestServer(t, discussions, &deletedNotes, &mu)
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.GetOldRunUrls(context.Background(), testMRIID, testProject, currentApplyA, "workspace-a", "apply")
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(deletedNotes) != 2 {
		t.Fatalf("expected 2 notes deleted (old apply for workspace-a), got %d: %v", len(deletedNotes), deletedNotes)
	}

	deleted := map[int]bool{}
	for _, id := range deletedNotes {
		deleted[id] = true
	}
	if !deleted[100] || !deleted[101] {
		t.Fatalf("expected notes 100 and 101 deleted, got %v", deletedNotes)
	}
	for _, id := range []int{200, 201, 300, currentApplyA} {
		if deleted[id] {
			t.Fatalf("note %d should NOT have been deleted", id)
		}
	}
}

func TestGetOldRunUrls_NoMatchingDiscussions_DeletesNothing(t *testing.T) {
	t.Setenv("TFBUDDY_DELETE_OLD_COMMENTS", "true")

	currentNoteID := 100
	discussions := []fakeDiscussion{
		{
			ID: "disc-current",
			Notes: []fakeNote{
				{ID: currentNoteID, Body: buildSeedBody("workspace-a", "plan", "org"), Username: botUsername},
			},
		},
		// Different workspace
		{
			ID: "disc-other",
			Notes: []fakeNote{
				{ID: 200, Body: buildSeedBody("workspace-b", "plan", "org"), Username: botUsername},
			},
		},
	}

	var deletedNotes []int
	var mu sync.Mutex
	server := setupTestServer(t, discussions, &deletedNotes, &mu)
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.GetOldRunUrls(context.Background(), testMRIID, testProject, currentNoteID, "workspace-a", "plan")
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(deletedNotes) != 0 {
		t.Fatalf("expected no deletions, got %d: %v", len(deletedNotes), deletedNotes)
	}
}

func TestGetOldRunUrls_DeleteDisabled_CollectsUrlsButNoDeletion(t *testing.T) {
	t.Setenv("TFBUDDY_DELETE_OLD_COMMENTS", "false")

	currentNoteID := 300
	discussions := []fakeDiscussion{
		{
			ID: "disc-old",
			Notes: []fakeNote{
				{ID: 100, Body: buildSeedBody("workspace-a", "plan", "org"), Username: botUsername},
				{ID: 101, Body: buildNoteBody("workspace-a", "plan", "run-old", "https://tfc/run-old", "planned_and_finished"), Username: botUsername},
			},
		},
		{
			ID: "disc-current",
			Notes: []fakeNote{
				{ID: currentNoteID, Body: buildSeedBody("workspace-a", "plan", "org"), Username: botUsername},
			},
		},
	}

	var deletedNotes []int
	var mu sync.Mutex
	server := setupTestServer(t, discussions, &deletedNotes, &mu)
	defer server.Close()

	client := newTestClient(t, server.URL)
	result, err := client.GetOldRunUrls(context.Background(), testMRIID, testProject, currentNoteID, "workspace-a", "plan")
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(deletedNotes) != 0 {
		t.Fatalf("expected no deletions when TFBUDDY_DELETE_OLD_COMMENTS is unset, got %d", len(deletedNotes))
	}

	if !strings.Contains(result, "run-old") {
		t.Fatalf("expected old run URL to be collected, got %q", result)
	}
}

func TestGetOldRunUrls_CollectsOldRunUrlsInTable(t *testing.T) {
	t.Setenv("TFBUDDY_DELETE_OLD_COMMENTS", "true")

	currentNoteID := 300
	discussions := []fakeDiscussion{
		{
			ID: "disc-old",
			Notes: []fakeNote{
				{ID: 100, Body: buildSeedBody("workspace-a", "plan", "org"), Username: botUsername},
				{ID: 101, Body: buildNoteBody("workspace-a", "plan", "run-old1", "https://tfc/runs/run-old1", "planned_and_finished"), Username: botUsername},
			},
		},
		{
			ID: "disc-current",
			Notes: []fakeNote{
				{ID: currentNoteID, Body: buildSeedBody("workspace-a", "plan", "org"), Username: botUsername},
			},
		},
	}

	var deletedNotes []int
	var mu sync.Mutex
	server := setupTestServer(t, discussions, &deletedNotes, &mu)
	defer server.Close()

	client := newTestClient(t, server.URL)
	result, err := client.GetOldRunUrls(context.Background(), testMRIID, testProject, currentNoteID, "workspace-a", "plan")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result, "Previous TFC Urls") {
		t.Fatalf("expected 'Previous TFC Urls' header in result, got:\n%s", result)
	}
	if !strings.Contains(result, "run-old1") {
		t.Fatalf("expected run-old1 in table, got:\n%s", result)
	}
	if !strings.Contains(result, "Planned and Finished") {
		t.Fatalf("expected formatted status in table, got:\n%s", result)
	}
}

func TestGetOldRunUrls_MultiWorkspace_FullScenario(t *testing.T) {
	t.Setenv("TFBUDDY_DELETE_OLD_COMMENTS", "true")

	currentPlanA := 1000
	currentPlanB := 1100
	currentApplyA := 1200

	discussions := []fakeDiscussion{
		// 2 old plans for workspace-a
		{ID: "disc-plan-a-1", Notes: []fakeNote{{ID: 10, Body: buildSeedBody("ws-a", "plan", "org"), Username: botUsername}}},
		{ID: "disc-plan-a-2", Notes: []fakeNote{{ID: 20, Body: buildSeedBody("ws-a", "plan", "org"), Username: botUsername}}},
		// 1 old plan for workspace-b
		{ID: "disc-plan-b-1", Notes: []fakeNote{{ID: 30, Body: buildSeedBody("ws-b", "plan", "org"), Username: botUsername}}},
		// 1 old apply for workspace-a
		{ID: "disc-apply-a-1", Notes: []fakeNote{{ID: 40, Body: buildSeedBody("ws-a", "apply", "org"), Username: botUsername}}},
		// Current discussions
		{ID: "disc-plan-a-current", Notes: []fakeNote{{ID: currentPlanA, Body: buildSeedBody("ws-a", "plan", "org"), Username: botUsername}}},
		{ID: "disc-plan-b-current", Notes: []fakeNote{{ID: currentPlanB, Body: buildSeedBody("ws-b", "plan", "org"), Username: botUsername}}},
		{ID: "disc-apply-a-current", Notes: []fakeNote{{ID: currentApplyA, Body: buildSeedBody("ws-a", "apply", "org"), Username: botUsername}}},
	}

	var mu sync.Mutex

	t.Run("cleanup plan for ws-a keeps only current, does not touch ws-b or apply", func(t *testing.T) {
		var deletedNotes []int
		server := setupTestServer(t, discussions, &deletedNotes, &mu)
		defer server.Close()

		client := newTestClient(t, server.URL)
		_, err := client.GetOldRunUrls(context.Background(), testMRIID, testProject, currentPlanA, "ws-a", "plan")
		if err != nil {
			t.Fatal(err)
		}

		mu.Lock()
		defer mu.Unlock()
		deleted := map[int]bool{}
		for _, id := range deletedNotes {
			deleted[id] = true
		}

		if !deleted[10] || !deleted[20] {
			t.Fatalf("expected notes 10, 20 deleted for old ws-a plans, got %v", deletedNotes)
		}
		for _, id := range []int{30, 40, currentPlanA, currentPlanB, currentApplyA} {
			if deleted[id] {
				t.Fatalf("note %d should NOT have been deleted", id)
			}
		}
	})

	t.Run("cleanup plan for ws-b keeps only current, does not touch ws-a", func(t *testing.T) {
		var deletedNotes []int
		server := setupTestServer(t, discussions, &deletedNotes, &mu)
		defer server.Close()

		client := newTestClient(t, server.URL)
		_, err := client.GetOldRunUrls(context.Background(), testMRIID, testProject, currentPlanB, "ws-b", "plan")
		if err != nil {
			t.Fatal(err)
		}

		mu.Lock()
		defer mu.Unlock()
		deleted := map[int]bool{}
		for _, id := range deletedNotes {
			deleted[id] = true
		}

		if !deleted[30] {
			t.Fatalf("expected note 30 deleted for old ws-b plan, got %v", deletedNotes)
		}
		for _, id := range []int{10, 20, 40, currentPlanA, currentPlanB, currentApplyA} {
			if deleted[id] {
				t.Fatalf("note %d should NOT have been deleted", id)
			}
		}
	})

	t.Run("cleanup apply for ws-a keeps only current, does not touch plans", func(t *testing.T) {
		var deletedNotes []int
		server := setupTestServer(t, discussions, &deletedNotes, &mu)
		defer server.Close()

		client := newTestClient(t, server.URL)
		_, err := client.GetOldRunUrls(context.Background(), testMRIID, testProject, currentApplyA, "ws-a", "apply")
		if err != nil {
			t.Fatal(err)
		}

		mu.Lock()
		defer mu.Unlock()
		deleted := map[int]bool{}
		for _, id := range deletedNotes {
			deleted[id] = true
		}

		if !deleted[40] {
			t.Fatalf("expected note 40 deleted for old ws-a apply, got %v", deletedNotes)
		}
		for _, id := range []int{10, 20, 30, currentPlanA, currentPlanB, currentApplyA} {
			if deleted[id] {
				t.Fatalf("note %d should NOT have been deleted", id)
			}
		}
	})
}
