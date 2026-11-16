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

	"github.com/hashicorp/go-tfe"
	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/utils"
	"go.uber.org/mock/gomock"
)

// liveGitLab is an in-memory GitLab that keeps the discussions of one merge
// request, so a test can play overlapping runs against the real client: notes
// that are deleted are gone, and writes to a deleted thread get a 404.
type liveGitLab struct {
	t           *testing.T
	mu          sync.Mutex
	nextID      int
	discussions []*liveDiscussion
	// writes that reached a discussion that no longer exists, by kind
	missed map[string]int
}

type liveDiscussion struct {
	id       string
	notes    []fakeNote
	resolved bool
}

// testRunThread is one TFBuddy run: its TFC run and the thread the trigger
// created for it.
type testRunThread struct {
	runID        string
	workspace    string
	discussionID string
	rootNoteID   int
}

func newLiveGitLab(t *testing.T) *liveGitLab {
	return &liveGitLab{t: t, nextID: 1000, missed: map[string]int{}}
}

func (g *liveGitLab) newNoteID() int {
	g.nextID++
	return g.nextID
}

// startRun creates the thread the trigger posts before it creates a run.
func (g *liveGitLab) startRun(workspace, runID string) testRunThread {
	g.mu.Lock()
	defer g.mu.Unlock()
	d := &liveDiscussion{
		id:    "disc-" + runID,
		notes: []fakeNote{{ID: g.newNoteID(), Body: buildSeedBody(workspace, "plan", "zapier"), Username: botUsername}},
	}
	g.discussions = append(g.discussions, d)
	return testRunThread{runID: runID, workspace: workspace, discussionID: d.id, rootNoteID: d.notes[0].ID}
}

// addFinishedRun adds the thread of a plan that finished before the test.
func (g *liveGitLab) addFinishedRun(workspace, runID string) testRunThread {
	r := g.startRun(workspace, runID)
	g.mu.Lock()
	defer g.mu.Unlock()
	d := g.discussions[len(g.discussions)-1]
	d.notes[0].Body = buildNoteBody(workspace, "plan", runID, "https://app.terraform.io/app/zapier/workspaces/"+workspace+"/runs/"+runID, "planned_and_finished")
	d.notes = append(d.notes, fakeNote{ID: g.newNoteID(), Body: "Status: `planned_and_finished`", Username: botUsername})
	return r
}

func (g *liveGitLab) find(id string) *liveDiscussion {
	for _, d := range g.discussions {
		if d.id == id {
			return d
		}
	}
	return nil
}

func (g *liveGitLab) exists(id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.find(id) != nil
}

func (g *liveGitLab) missedWrites(kind string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.missed[kind]
}

// threads returns the discussions left for a workspace and action.
func (g *liveGitLab) threads(workspace, action string) []liveDiscussion {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []liveDiscussion
	for _, d := range g.discussions {
		ws, a, found := utils.ParseTFBuddyMarker(d.notes[0].Body)
		if found && ws == workspace && a == action {
			out = append(out, *d)
		}
	}
	return out
}

func (g *liveGitLab) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()

	path := r.URL.RawPath
	if path == "" {
		path = r.URL.Path
	}
	w.Header().Set("Content-Type", "application/json")
	if strings.HasSuffix(path, "/api/v4/user") {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "username": botUsername})
		return
	}
	mrPrefix := fmt.Sprintf("/merge_requests/%d/", testMRIID)
	i := strings.Index(path, mrPrefix)
	if i < 0 {
		g.t.Errorf("unexpected request: %s %s", r.Method, path)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	parts := strings.Split(path[i+len(mrPrefix):], "/")
	var body struct {
		Body string `json:"body"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	switch {
	// GET .../discussions
	case r.Method == http.MethodGet && len(parts) == 1 && parts[0] == "discussions":
		var out []map[string]any
		for _, d := range g.discussions {
			var notes []map[string]any
			for _, n := range d.notes {
				notes = append(notes, noteJSON(n))
			}
			out = append(out, map[string]any{"id": d.id, "notes": notes})
		}
		_ = json.NewEncoder(w).Encode(out)

	// DELETE .../notes/:note_id
	case r.Method == http.MethodDelete && len(parts) == 2 && parts[0] == "notes":
		noteID, _ := strconv.Atoi(parts[1])
		for di, d := range g.discussions {
			for ni, n := range d.notes {
				if n.ID != noteID {
					continue
				}
				d.notes = append(d.notes[:ni], d.notes[ni+1:]...)
				if len(d.notes) == 0 {
					g.discussions = append(g.discussions[:di], g.discussions[di+1:]...)
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)

	// PUT .../discussions/:id/notes/:note_id updates a note.
	case r.Method == http.MethodPut && len(parts) == 4 && parts[0] == "discussions" && parts[2] == "notes":
		noteID, _ := strconv.Atoi(parts[3])
		if d := g.find(parts[1]); d != nil {
			for ni := range d.notes {
				if d.notes[ni].ID == noteID {
					d.notes[ni].Body = body.Body
					_ = json.NewEncoder(w).Encode(noteJSON(d.notes[ni]))
					return
				}
			}
		}
		g.missed["update"]++
		w.WriteHeader(http.StatusNotFound)

	// POST .../discussions/:id/notes adds a reply.
	case r.Method == http.MethodPost && len(parts) == 3 && parts[0] == "discussions" && parts[2] == "notes":
		d := g.find(parts[1])
		if d == nil {
			g.missed["reply"]++
			w.WriteHeader(http.StatusNotFound)
			return
		}
		n := fakeNote{ID: g.newNoteID(), Body: body.Body, Username: botUsername}
		d.notes = append(d.notes, n)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(noteJSON(n))

	// PUT .../discussions/:id resolves a thread.
	case r.Method == http.MethodPut && len(parts) == 2 && parts[0] == "discussions":
		d := g.find(parts[1])
		if d == nil {
			g.missed["resolve"]++
			w.WriteHeader(http.StatusNotFound)
			return
		}
		d.resolved = true
		_ = json.NewEncoder(w).Encode(map[string]any{"id": d.id})

	default:
		g.t.Errorf("unexpected request: %s %s", r.Method, path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func noteJSON(n fakeNote) map[string]any {
	return map[string]any{
		"id":         n.ID,
		"body":       n.Body,
		"author":     map[string]any{"username": n.Username},
		"created_at": "2026-03-31T18:00:00.000Z",
	}
}

func (r testRunThread) finished() *tfe.Run {
	return &tfe.Run{
		ID:        r.runID,
		Status:    tfe.RunPlannedAndFinished,
		Workspace: &tfe.Workspace{Name: r.workspace, Organization: &tfe.Organization{Name: "zapier"}},
		Plan:      &tfe.Plan{ID: "plan-" + r.runID},
	}
}

func (r testRunThread) metadata() runstream.RunMetadata {
	return &runstream.TFRunMetadata{
		RunID:                                r.runID,
		Organization:                         "zapier",
		Workspace:                            r.workspace,
		Action:                               runstream.PlanAction,
		MergeRequestProjectNameWithNamespace: testProject,
		MergeRequestIID:                      testMRIID,
		DiscussionID:                         r.discussionID,
		RootNoteID:                           int64(r.rootNoteID),
		VcsProvider:                          "gitlab",
	}
}

// Two plans of one workspace can overlap, for example a push and a `tfc plan`
// comment in the same minute. Whichever finishes first, the MR must
// end up with the newer run's thread and result, and the earlier plan's
// thread must still be cleaned up.
func TestOverlappingPlanRunsKeepTheNewerRunsThread(t *testing.T) {
	t.Setenv("TFBUDDY_DELETE_OLD_COMMENTS", "true")
	config.Reload()

	for _, tc := range []struct {
		name               string
		olderFinishesFirst bool
	}{
		{name: "newer run finishes first", olderFinishesFirst: false},
		{name: "older run finishes first", olderFinishesFirst: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gl := newLiveGitLab(t)
			earlier := gl.addFinishedRun("workspace-a", "run-earlier")
			older := gl.startRun("workspace-a", "run-older")
			newer := gl.startRun("workspace-a", "run-newer")
			server := httptest.NewServer(gl)
			defer server.Close()

			ctrl := gomock.NewController(t)
			tfc := mocks.NewMockApiClient(ctrl)
			tfc.EXPECT().GetPlanOutput(gomock.Any()).Return([]byte("{}"), nil).AnyTimes()
			p := &RunStatusUpdater{cfg: config.C, tfc: tfc, client: newTestClient(t, server.URL)}

			first, second := newer, older
			if tc.olderFinishesFirst {
				first, second = older, newer
			}
			ctx := context.Background()
			p.postRunStatusComment(ctx, first.finished(), first.metadata())
			p.postRunStatusComment(ctx, second.finished(), second.metadata())

			threads := gl.threads("workspace-a", "plan")
			if len(threads) != 1 {
				var ids []string
				for _, d := range threads {
					ids = append(ids, d.id)
				}
				t.Fatalf("plan threads left = %v, want only %s", ids, newer.discussionID)
			}
			got := threads[0]
			if got.id != newer.discussionID {
				t.Fatalf("plan thread left = %s, want the newer run's %s", got.id, newer.discussionID)
			}
			if root := got.notes[0].Body; !strings.Contains(root, "**Run URL**: [run-newer]") || !strings.Contains(root, "`planned_and_finished`") {
				t.Errorf("root note does not show the newer run's final status:\n%s", root)
			}
			if len(got.notes) < 2 || !strings.Contains(got.notes[len(got.notes)-1].Body, "Status: `planned_and_finished`") {
				t.Errorf("the newer run's thread has no result reply: %+v", got.notes)
			}
			if !got.resolved {
				t.Error("the newer run's no-change plan thread was not resolved")
			}
			for _, id := range []string{earlier.discussionID, older.discussionID} {
				if gl.exists(id) {
					t.Errorf("thread %s was not deleted", id)
				}
			}
			// An older run whose thread is gone skips its reply and resolve
			// instead of failing them.
			if n := gl.missedWrites("reply") + gl.missedWrites("resolve"); n != 0 {
				t.Errorf("%d replies or resolves were sent to a deleted thread", n)
			}
		})
	}
}
