package tfc_trigger_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/go-tfe"
	"go.uber.org/mock/gomock"

	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/vcs"
)

// TestWorkspaceWorker_DrainsConcurrentlyWithoutLeakingDiscussionIDs drains a
// fan-out queue concurrently and asserts each per-workspace AddRunMeta carries
// that workspace's own DiscussionID/RootNoteID. This is the regression guard
// for the goroutine-local discussion fix — if discussion IDs ever leak across
// workspaces (the t.cfg race), the assertion below catches it.
func TestWorkspaceWorker_DrainsConcurrentlyWithoutLeakingDiscussionIDs(t *testing.T) {
	const numWorkspaces = 6
	cfg := buildFanoutWorkspaces(numWorkspaces)

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{ProjectConfig: cfg}, t)

	// Each workspace gets a unique discussion + note ID so a regression that
	// shares discussion state across goroutines surfaces as a wrong mapping.
	// Override default modified-files mock from InitTestSuite so each
	// workspace dir matches at least one changed file.
	modified := make([]string, 0, numWorkspaces)
	for i := 0; i < numWorkspaces; i++ {
		modified = append(modified, fmt.Sprintf("services/svc%02d/main.tf", i))
	}
	testSuite.MockGitClient.EXPECT().GetMergeRequestModifiedFiles(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS).Return(modified, nil).AnyTimes()

	wantDisc := map[string]string{}
	wantNote := map[string]int64{}
	for i := 0; i < numWorkspaces; i++ {
		wsName := fmt.Sprintf("service-tfbuddy-%02d", i)
		discID := fmt.Sprintf("disc-%02d", i)
		noteID := int64(1000 + i)
		wantDisc[wsName] = discID
		wantNote[wsName] = noteID

		dis := mocks.NewMockMRDiscussionNotes(mockCtrl)
		dis.EXPECT().GetDiscussionID().Return(discID).AnyTimes()
		note := mocks.NewMockMRNote(mockCtrl)
		note.EXPECT().GetNoteID().Return(noteID).AnyTimes()
		dis.EXPECT().GetMRNotes().Return([]vcs.MRNote{note}).AnyTimes()
		testSuite.MockGitClient.EXPECT().CreateMergeRequestDiscussion(
			gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS,
			fmt.Sprintf("Starting TFC apply for Workspace: `zapier-test/%s`.\n<!-- tfbuddy:ws=%s:action=apply -->", wsName, wsName),
		).Return(dis, nil).Times(1)
	}

	testSuite.MockApiClient.EXPECT().GetWorkspaceByName(gomock.Any(), "zapier-test", gomock.Any()).DoAndReturn(func(_ context.Context, _, name string) (*tfe.Workspace, error) {
		return &tfe.Workspace{ID: name}, nil
	}).AnyTimes()

	testSuite.MockApiClient.EXPECT().CreateRunFromSource(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, opts *tfc_api.ApiRunOptions) (*tfe.Run, error) {
		return &tfe.Run{
			ID:                   "run-" + opts.Workspace,
			Workspace:            &tfe.Workspace{Name: opts.Workspace, Organization: &tfe.Organization{Name: "zapier-test"}},
			ConfigurationVersion: &tfe.ConfigurationVersion{Speculative: false},
		}, nil
	}).Times(numWorkspaces)

	// Capture each workspace's metadata as it lands in AddRunMeta. The lock
	// keeps the assertions safe under -race.
	var seenMu sync.Mutex
	seenDisc := make(map[string]string)
	seenNote := make(map[string]int64)
	testSuite.MockStreamClient.EXPECT().AddRunMeta(gomock.Any()).DoAndReturn(func(rmd runstream.RunMetadata) error {
		seenMu.Lock()
		seenDisc[rmd.GetWorkspace()] = rmd.GetDiscussionID()
		seenNote[rmd.GetWorkspace()] = rmd.GetRootNoteID()
		seenMu.Unlock()
		return nil
	}).Times(numWorkspaces)
	testSuite.InitTestSuite()

	// Build the fan-out exactly as production does — TriggerTFCEvents enqueues,
	// then the worker drains.
	tCfg, _ := tfc_trigger.NewTFCTriggerConfig(&tfc_trigger.TFCTriggerOptions{
		Action:                   tfc_trigger.ApplyAction,
		Branch:                   testSuite.MetaData.SourceBranch,
		CommitSHA:                "abcd12233",
		ProjectNameWithNamespace: testSuite.MetaData.ProjectNameNS,
		MergeRequestIID:          testSuite.MetaData.MRIID,
		TriggerSource:            tfc_trigger.MergeRequestEventTrigger,
		VcsProvider:              "gitlab",
	})
	pub := &fakeWorkspacePublisher{}
	trigger := tfc_trigger.NewTFCTrigger(testSuite.MockGitClient, testSuite.MockApiClient, testSuite.MockStreamClient, tCfg)
	trigger.SetWorkspaceStream(pub)

	if _, err := trigger.TriggerTFCEvents(context.Background()); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	// Now drain. This is what NATS would do: invoke HandleMsg on each delivery.
	// We dispatch them in parallel to exercise the goroutine-local fix.
	worker := tfc_trigger.NewWorkspaceTriggerWorkerWithoutSubscription(
		map[string]vcs.GitClient{"gitlab": testSuite.MockGitClient},
		testSuite.MockApiClient, testSuite.MockStreamClient,
	)

	// The worker re-clones the repo and re-checks target-branch modifications
	// per workspace. The TestSuite's existing mocks already cover those calls
	// with AnyTimes, so all we need is the inner trigger to succeed.
	var wg sync.WaitGroup
	for _, msg := range pub.msgs {
		msg := msg
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := worker.HandleMsg(msg); err != nil {
				t.Errorf("worker run for %s failed: %v", msg.Workspace.Name, err)
			}
		}()
	}
	wg.Wait()

	seenMu.Lock()
	defer seenMu.Unlock()
	if len(seenDisc) != numWorkspaces {
		t.Fatalf("expected AddRunMeta for %d workspaces, got %d (%v)", numWorkspaces, len(seenDisc), seenDisc)
	}
	for ws, want := range wantDisc {
		got, ok := seenDisc[ws]
		if !ok {
			t.Fatalf("workspace %s did not publish AddRunMeta", ws)
		}
		if got != want {
			t.Fatalf("workspace %s leaked discussionID: got %q, want %q", ws, got, want)
		}
		if seenNote[ws] != wantNote[ws] {
			t.Fatalf("workspace %s leaked rootNoteID: got %d, want %d", ws, seenNote[ws], wantNote[ws])
		}
	}
}

// TestWorkspaceWorker_PostsErrorToMRAndACKsOnFailure verifies a workspace
// failure surfaces back to the MR via a top-level comment and the worker
// returns nil so the message is ACKed (we deliberately don't NAK because
// retries would create duplicate discussions and runs).
func TestWorkspaceWorker_PostsErrorToMRAndACKsOnFailure(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	// VCS API blows up while loading the MR — runWorkspace returns this error
	// before any clone or discussion happens.
	testSuite.MockGitClient.EXPECT().GetMergeRequest(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS).
		Return(nil, errors.New("VCS API down")).AnyTimes()

	var posted []string
	testSuite.MockGitClient.EXPECT().CreateMergeRequestComment(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ int, _, body string) error {
			posted = append(posted, body)
			return nil
		}).Times(1)

	testSuite.InitTestSuite()

	worker := tfc_trigger.NewWorkspaceTriggerWorkerWithoutSubscription(
		map[string]vcs.GitClient{"gitlab": testSuite.MockGitClient},
		testSuite.MockApiClient, testSuite.MockStreamClient,
	)
	msg := &tfc_trigger.WorkspaceTriggerMsg{
		Opts: tfc_trigger.TFCTriggerOptions{
			Action:                   tfc_trigger.PlanAction,
			ProjectNameWithNamespace: testSuite.MetaData.ProjectNameNS,
			MergeRequestIID:          testSuite.MetaData.MRIID,
			CommitSHA:                "deadbeef",
			TriggerSource:            tfc_trigger.MergeRequestEventTrigger,
			VcsProvider:              "gitlab",
		},
		Workspace: tfc_trigger.TFCWorkspace{Name: "service-tfbuddy", Organization: "zapier-test"},
	}

	if err := worker.HandleMsg(msg); err != nil {
		t.Fatalf("HandleMsg should ACK on workspace error, got: %v", err)
	}
	if len(posted) != 1 {
		t.Fatalf("expected 1 MR comment posted, got %d", len(posted))
	}
	if !strings.Contains(posted[0], "service-tfbuddy") || !strings.Contains(posted[0], "VCS API down") {
		t.Fatalf("MR comment should mention workspace and root cause, got: %q", posted[0])
	}
}

// TestWorkspaceWorker_RecoversFromPanic confirms a panic inside the worker
// (e.g. nil pointer in a downstream call) doesn't tear down the JetStream
// subscription. The worker logs, posts a generic error to the MR, and ACKs.
func TestWorkspaceWorker_RecoversFromPanic(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	// Force a panic by returning a nil MR — trigger.cloneGitRepo will
	// dereference it.
	testSuite.MockGitClient.EXPECT().GetMergeRequest(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS).
		DoAndReturn(func(_ context.Context, _ int, _ string) (vcs.DetailedMR, error) {
			panic("unexpected nil dereference in test")
		}).AnyTimes()

	var posted []string
	testSuite.MockGitClient.EXPECT().CreateMergeRequestComment(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ int, _, body string) error {
			posted = append(posted, body)
			return nil
		}).Times(1)

	testSuite.InitTestSuite()

	worker := tfc_trigger.NewWorkspaceTriggerWorkerWithoutSubscription(
		map[string]vcs.GitClient{"gitlab": testSuite.MockGitClient},
		testSuite.MockApiClient, testSuite.MockStreamClient,
	)
	msg := &tfc_trigger.WorkspaceTriggerMsg{
		Opts: tfc_trigger.TFCTriggerOptions{
			Action:                   tfc_trigger.PlanAction,
			ProjectNameWithNamespace: testSuite.MetaData.ProjectNameNS,
			MergeRequestIID:          testSuite.MetaData.MRIID,
			CommitSHA:                "deadbeef",
			TriggerSource:            tfc_trigger.MergeRequestEventTrigger,
			VcsProvider:              "gitlab",
		},
		Workspace: tfc_trigger.TFCWorkspace{Name: "service-tfbuddy", Organization: "zapier-test"},
	}

	if err := worker.HandleMsg(msg); err != nil {
		t.Fatalf("HandleMsg must swallow panics so JetStream stays subscribed, got: %v", err)
	}
	if len(posted) != 1 || !strings.Contains(posted[0], "internal error") {
		t.Fatalf("expected an internal-error MR comment, got: %v", posted)
	}
}

// TestWorkspaceWorker_RoutesByVcsProvider locks in the fan-out routing fix:
// the worker must dispatch to the client matching msg.Opts.VcsProvider, never
// to the other provider. A regression here would manifest in production as
// GitHub-origin triggers calling the GitLab API (404s, auth failures, MR
// comment spam) — exactly the class of bug the multi-client worker prevents.
func TestWorkspaceWorker_RoutesByVcsProvider(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	// Two distinct clients so we can prove dispatch picks the right one.
	// The GitHub mock is the one we expect to receive calls; a stray call
	// to the GitLab mock would fail the test via gomock's strict mode.
	githubClient := mocks.NewMockGitClient(mockCtrl)
	gitlabClient := mocks.NewMockGitClient(mockCtrl)

	githubClient.EXPECT().GetMergeRequest(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS).
		Return(nil, errors.New("forced failure to short-circuit run path")).Times(1)
	githubClient.EXPECT().CreateMergeRequestComment(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS, gomock.Any()).
		Return(nil).Times(1)

	testSuite.InitTestSuite()

	worker := tfc_trigger.NewWorkspaceTriggerWorkerWithoutSubscription(
		map[string]vcs.GitClient{"gitlab": gitlabClient, "github": githubClient},
		testSuite.MockApiClient, testSuite.MockStreamClient,
	)

	githubMsg := &tfc_trigger.WorkspaceTriggerMsg{
		Opts: tfc_trigger.TFCTriggerOptions{
			Action:                   tfc_trigger.PlanAction,
			ProjectNameWithNamespace: testSuite.MetaData.ProjectNameNS,
			MergeRequestIID:          testSuite.MetaData.MRIID,
			CommitSHA:                "deadbeef",
			TriggerSource:            tfc_trigger.MergeRequestEventTrigger,
			VcsProvider:              "github",
		},
		Workspace: tfc_trigger.TFCWorkspace{Name: "service-tfbuddy", Organization: "zapier-test"},
	}
	if err := worker.HandleMsg(githubMsg); err != nil {
		t.Fatalf("HandleMsg should ACK on workspace error, got: %v", err)
	}

	// Unknown provider must be dropped (ACKed) without invoking either
	// client — there's nothing actionable we can do, and using a default
	// would corrupt the receiving VCS.
	unknownMsg := &tfc_trigger.WorkspaceTriggerMsg{
		Opts: tfc_trigger.TFCTriggerOptions{
			Action:                   tfc_trigger.PlanAction,
			ProjectNameWithNamespace: testSuite.MetaData.ProjectNameNS,
			MergeRequestIID:          testSuite.MetaData.MRIID,
			CommitSHA:                "deadbeef",
			TriggerSource:            tfc_trigger.MergeRequestEventTrigger,
			VcsProvider:              "bitbucket",
		},
		Workspace: tfc_trigger.TFCWorkspace{Name: "service-tfbuddy", Organization: "zapier-test"},
	}
	if err := worker.HandleMsg(unknownMsg); err != nil {
		t.Fatalf("HandleMsg should ACK on unknown provider, got: %v", err)
	}
}
