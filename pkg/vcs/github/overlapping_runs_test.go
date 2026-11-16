package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/go-tfe"
	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/utils"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.uber.org/mock/gomock"
)

// liveGitHub is an in-memory GitHub that keeps the comments of one pull
// request, so a test can play overlapping runs against the real client. It
// pages comment listings like GitHub does.
type liveGitHub struct {
	t        *testing.T
	mu       sync.Mutex
	nextID   int64
	comments []fakeComment
	// beforeCreate, if set, runs before a new comment is stored, without
	// holding the lock, so it can play another run's requests in between.
	beforeCreate func(body string)
	// afterListPage, if set, runs with the lock held after a page of the
	// listing is read, so it can change the comments between pages.
	afterListPage func(page int)
}

func newLiveGitHub(t *testing.T) *liveGitHub {
	return &liveGitHub{t: t, nextID: 1000}
}

func (g *liveGitHub) add(body string) int64 {
	g.nextID++
	g.comments = append(g.comments, fakeComment{ID: g.nextID, Body: body, User: ghBotUser})
	return g.nextID
}

// removeLocked deletes comments; the caller holds the lock.
func (g *liveGitHub) removeLocked(ids ...int64) {
	g.comments = slices.DeleteFunc(g.comments, func(c fakeComment) bool {
		return slices.Contains(ids, c.ID)
	})
}

func (g *liveGitHub) setBeforeCreate(hook func(body string)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.beforeCreate = hook
}

// addHumanComments adds comments by a person, for example to fill the first
// page of the listing.
func (g *liveGitHub) addHumanComments(n int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i := 0; i < n; i++ {
		g.nextID++
		g.comments = append(g.comments, fakeComment{ID: g.nextID, Body: "Looks good to me", User: "reviewer"})
	}
}

// startRun posts the comment the trigger posts before it creates a run.
func (g *liveGitHub) startRun(workspace, runID string) ghRun {
	g.mu.Lock()
	defer g.mu.Unlock()
	id := g.add(fmt.Sprintf("Starting TFC plan for Workspace: `zapier/%s`.\n%s", workspace, utils.FormatTFBuddyMarker(workspace, "plan")))
	return ghRun{runID: runID, workspace: workspace, startingCommentID: id}
}

// addFinishedRun adds the comments a plan that finished before the test left.
func (g *liveGitHub) addFinishedRun(workspace, runID string) ghRun {
	r := g.startRun(workspace, runID)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.add(buildGHCommentBody(workspace, "plan", runID, "https://tfc/runs/"+runID, "planned_and_finished"))
	return r
}

func (g *liveGitHub) exists(id int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, c := range g.comments {
		if c.ID == id {
			return true
		}
	}
	return false
}

// matching returns the comments left for a workspace and action, oldest first.
func (g *liveGitHub) matching(workspace, action string) []fakeComment {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []fakeComment
	for _, c := range g.comments {
		ws, a, found := utils.ParseTFBuddyMarker(c.Body)
		if found && ws == workspace && a == action {
			out = append(out, c)
		}
	}
	return out
}

func commentJSON(c fakeComment) map[string]any {
	return map[string]any{
		"id":         c.ID,
		"body":       c.Body,
		"user":       map[string]any{"login": c.User},
		"created_at": "2026-03-31T18:00:00Z",
	}
}

func (g *liveGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	prComments := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", testOwner, testRepo, testPRID)
	commentPrefix := fmt.Sprintf("/repos/%s/%s/issues/comments/", testOwner, testRepo)
	var body struct {
		Body string `json:"body"`
	}
	if r.Method == http.MethodPost {
		_ = json.NewDecoder(r.Body).Decode(&body)
		g.mu.Lock()
		hook := g.beforeCreate
		g.mu.Unlock()
		if hook != nil {
			hook(body.Body)
		}
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/user":
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "login": ghBotUser})

	case r.URL.Path == prComments && r.Method == http.MethodGet:
		perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		if perPage <= 0 {
			perPage = 30
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page <= 0 {
			page = 1
		}
		from := min((page-1)*perPage, len(g.comments))
		to := min(from+perPage, len(g.comments))
		if to < len(g.comments) {
			w.Header().Set("Link", fmt.Sprintf(`<http://%s%s?page=%d&per_page=%d>; rel="next"`, r.Host, prComments, page+1, perPage))
		}
		out := []map[string]any{}
		for _, c := range g.comments[from:to] {
			out = append(out, commentJSON(c))
		}
		if g.afterListPage != nil {
			g.afterListPage(page)
		}
		_ = json.NewEncoder(w).Encode(out)

	case r.URL.Path == prComments && r.Method == http.MethodPost:
		g.add(body.Body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(commentJSON(g.comments[len(g.comments)-1]))

	case strings.HasPrefix(r.URL.Path, commentPrefix):
		id, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, commentPrefix), 10, 64)
		for i, c := range g.comments {
			if c.ID != id {
				continue
			}
			switch r.Method {
			case http.MethodGet:
				_ = json.NewEncoder(w).Encode(commentJSON(c))
			case http.MethodDelete:
				g.comments = append(g.comments[:i], g.comments[i+1:]...)
				w.WriteHeader(http.StatusNoContent)
			default:
				g.t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
			return
		}
		w.WriteHeader(http.StatusNotFound)

	default:
		g.t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

// ghRun is one TFBuddy run on GitHub: its TFC run and the comment the trigger
// posted when it started the run.
type ghRun struct {
	runID             string
	workspace         string
	startingCommentID int64
}

func (r ghRun) run(status tfe.RunStatus) *tfe.Run {
	return &tfe.Run{
		ID:        r.runID,
		Status:    status,
		Workspace: &tfe.Workspace{Name: r.workspace, Organization: &tfe.Organization{Name: "zapier"}},
		Plan:      &tfe.Plan{ID: "plan-" + r.runID},
	}
}

// metadata is what the trigger stores for a GitHub run: the discussion ID is
// the starting comment's ID, and there is no root note ID.
func (r ghRun) metadata() runstream.RunMetadata {
	return &runstream.TFRunMetadata{
		RunID:                                r.runID,
		Organization:                         "zapier",
		Workspace:                            r.workspace,
		Action:                               runstream.PlanAction,
		MergeRequestProjectNameWithNamespace: testFullName,
		MergeRequestIID:                      testPRID,
		DiscussionID:                         strconv.FormatInt(r.startingCommentID, 10),
		VcsProvider:                          "github",
	}
}

// Two plans of one workspace can overlap, for example a push and a `tfc plan`
// comment in the same minute. Whichever finishes first, the newer run's
// result must be the last comment for the workspace, its comments must stay,
// and no status update of a run in progress may be left behind.
func TestGH_OverlappingPlanRunsKeepTheNewerResult(t *testing.T) {
	t.Setenv("TFBUDDY_DELETE_OLD_COMMENTS", "true")
	config.Reload()

	for _, tc := range []struct {
		name               string
		olderFinishesFirst bool
		// comments posted before the runs, so that the runs' comments are
		// on a later page of the listing
		humanComments int
	}{
		{name: "newer run finishes first", olderFinishesFirst: false},
		{name: "older run finishes first", olderFinishesFirst: true},
		{name: "newer run finishes first, runs beyond the first page", olderFinishesFirst: false, humanComments: 150},
		{name: "older run finishes first, runs beyond the first page", olderFinishesFirst: true, humanComments: 150},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gh := newLiveGitHub(t)
			gh.addHumanComments(tc.humanComments)
			earlier := gh.addFinishedRun("workspace-a", "run-earlier")
			older := gh.startRun("workspace-a", "run-older")
			newer := gh.startRun("workspace-a", "run-newer")
			server := httptest.NewServer(gh)
			defer server.Close()

			ctrl := gomock.NewController(t)
			tfc := mocks.NewMockApiClient(ctrl)
			tfc.EXPECT().GetPlanOutput(gomock.Any()).Return([]byte("{}"), nil).AnyTimes()
			w := &RunEventsWorker{cfg: config.C, tfc: tfc, client: newGHTestClient(t, server.URL)}

			ctx := context.Background()
			w.postRunStatusComment(ctx, older.run(tfe.RunPlanning), older.metadata())
			w.postRunStatusComment(ctx, newer.run(tfe.RunPlanning), newer.metadata())
			first, second := newer, older
			if tc.olderFinishesFirst {
				first, second = older, newer
			}
			w.postRunStatusComment(ctx, first.run(tfe.RunPlannedAndFinished), first.metadata())
			w.postRunStatusComment(ctx, second.run(tfe.RunPlannedAndFinished), second.metadata())

			left := gh.matching("workspace-a", "plan")
			if len(left) == 0 {
				t.Fatal("no comments left for the workspace")
			}
			last := left[len(left)-1].Body
			if !strings.Contains(last, "**Run URL**: [run-newer]") || !strings.Contains(last, "`planned_and_finished`") {
				t.Errorf("the last comment is not the newer run's result:\n%s", last)
			}
			if !gh.exists(newer.startingCommentID) {
				t.Error("the newer run's starting comment was deleted")
			}
			for _, c := range left {
				if strings.Contains(c.Body, "**Status**: `planning`") {
					t.Errorf("a status update of a run in progress was left behind:\n%s", c.Body)
				}
				if strings.Contains(c.Body, "run-earlier") && !strings.Contains(c.Body, "Previous TFC Urls") {
					t.Errorf("the earlier run's comment was not deleted:\n%s", c.Body)
				}
			}
			if gh.exists(earlier.startingCommentID) {
				t.Error("the earlier run's starting comment was not deleted")
			}
			if !tc.olderFinishesFirst {
				// The older run finished after the newer one replaced it, so
				// it posts nothing.
				for _, c := range left {
					if strings.Contains(c.Body, "**Run URL**: [run-older]") {
						t.Errorf("the superseded older run left a comment:\n%s", c.Body)
					}
				}
			}
		})
	}
}

// A run whose starting comment a newer run has deleted is superseded: it
// deletes only its own status updates and reports ErrRunSuperseded, so its
// worker does not post a stale result.
func TestGH_GetOldRunUrls_SupersededRunOnlyDeletesItsOwnStatusUpdates(t *testing.T) {
	t.Setenv("TFBUDDY_DELETE_OLD_COMMENTS", "true")
	config.Reload()

	gh := newLiveGitHub(t)
	older := gh.startRun("workspace-a", "run-older")
	newer := gh.addFinishedRun("workspace-a", "run-newer")
	gh.mu.Lock()
	olderStatus := gh.add(buildGHCommentBody("workspace-a", "plan", "run-older", "https://tfc/runs/run-older", "planning"))
	gh.comments = gh.comments[1:] // the newer run deleted the older run's starting comment
	gh.mu.Unlock()
	server := httptest.NewServer(gh)
	defer server.Close()

	client := newGHTestClient(t, server.URL)
	_, err := client.GetOldRunUrls(context.Background(), testPRID, testFullName, int(older.startingCommentID), "run-older", "workspace-a", "plan")
	if !errors.Is(err, vcs.ErrRunSuperseded) {
		t.Fatalf("err = %v, want ErrRunSuperseded", err)
	}
	if gh.exists(olderStatus) {
		t.Error("the superseded run's status update was not deleted")
	}
	if left := gh.matching("workspace-a", "plan"); len(left) != 2 || left[0].ID != newer.startingCommentID {
		t.Errorf("the newer run's comments were touched: %+v", left)
	}
}

// GetOldRunUrls checks that the run is current before its result is posted,
// but a newer run handled on another replica can replace it in between: delete
// the older run's starting comment and post its own result. The older run's
// result, posted after that, must be deleted again rather than left last.
func TestGH_NewerRunReplacingTheOlderOneWhileItPostsItsResult(t *testing.T) {
	t.Setenv("TFBUDDY_DELETE_OLD_COMMENTS", "true")
	config.Reload()

	gh := newLiveGitHub(t)
	gh.addFinishedRun("workspace-a", "run-earlier")
	older := gh.startRun("workspace-a", "run-older")
	newer := gh.startRun("workspace-a", "run-newer")
	server := httptest.NewServer(gh)
	defer server.Close()

	ctrl := gomock.NewController(t)
	tfc := mocks.NewMockApiClient(ctrl)
	tfc.EXPECT().GetPlanOutput(gomock.Any()).Return([]byte("{}"), nil).AnyTimes()
	w := &RunEventsWorker{cfg: config.C, tfc: tfc, client: newGHTestClient(t, server.URL)}
	ctx := context.Background()

	// The newer run finishes on another replica just as the older run posts
	// its result: after the older run's check, before its comment is stored.
	var once sync.Once
	gh.setBeforeCreate(func(body string) {
		if strings.Contains(body, "**Run URL**: [run-older]") && strings.Contains(body, "`planned_and_finished`") {
			once.Do(func() {
				w.postRunStatusComment(ctx, newer.run(tfe.RunPlannedAndFinished), newer.metadata())
			})
		}
	})
	w.postRunStatusComment(ctx, older.run(tfe.RunPlannedAndFinished), older.metadata())

	left := gh.matching("workspace-a", "plan")
	if len(left) == 0 {
		t.Fatal("no comments left for the workspace")
	}
	if last := left[len(left)-1].Body; !strings.Contains(last, "**Run URL**: [run-newer]") {
		t.Errorf("the last comment is not the newer run's result:\n%s", last)
	}
	for _, c := range left {
		if strings.Contains(c.Body, "**Run URL**: [run-older]") {
			t.Errorf("the replaced older run's result was left:\n%s", c.Body)
		}
	}
}

// Pages are read by offset. When another run deletes comments while the run
// reads the pages, its starting comment can shift onto a page it has already
// read. It still exists, so the run is not superseded and cleans up as usual.
func TestGH_GetOldRunUrls_StartingCommentShiftedOntoAPageAlreadyRead(t *testing.T) {
	t.Setenv("TFBUDDY_DELETE_OLD_COMMENTS", "true")
	config.Reload()

	gh := newLiveGitHub(t)
	gh.addHumanComments(98)
	earlier := gh.addFinishedRun("workspace-a", "run-earlier") // the last two comments of page 1
	current := gh.startRun("workspace-a", "run-current")       // the first comment of page 2
	gh.afterListPage = func(page int) {
		if page == 1 {
			// Another run deletes the earlier run's comments, so the starting
			// comment moves to page 1, which has been read already.
			gh.removeLocked(earlier.startingCommentID, earlier.startingCommentID+1)
		}
	}
	server := httptest.NewServer(gh)
	defer server.Close()

	client := newGHTestClient(t, server.URL)
	if _, err := client.GetOldRunUrls(context.Background(), testPRID, testFullName, int(current.startingCommentID), "run-current", "workspace-a", "plan"); err != nil {
		t.Fatalf("err = %v, want the run to be current", err)
	}
	if !gh.exists(current.startingCommentID) {
		t.Error("the run's starting comment was deleted")
	}
}
