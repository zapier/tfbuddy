package tfc_trigger_test

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"go.opentelemetry.io/otel"
	"go.uber.org/mock/gomock"

	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
)

// fakeWorkspacePublisher is a thread-safe in-memory stand-in for the
// production NATS-backed WorkspaceStream. Tests use it to assert exactly which
// workspaces would have been enqueued without spinning up JetStream.
type fakeWorkspacePublisher struct {
	mu       sync.Mutex
	msgs     []*tfc_trigger.WorkspaceTriggerMsg
	failOnce *string
}

func (f *fakeWorkspacePublisher) Publish(ctx context.Context, msg *tfc_trigger.WorkspaceTriggerMsg) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOnce != nil && *f.failOnce == msg.Workspace.Name {
		f.failOnce = nil
		return fmt.Errorf("simulated publish failure")
	}
	f.msgs = append(f.msgs, msg)
	return nil
}

func (f *fakeWorkspacePublisher) names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.msgs))
	for _, m := range f.msgs {
		out = append(out, m.Workspace.Name)
	}
	sort.Strings(out)
	return out
}

// buildFanoutWorkspaces builds an N-workspace ProjectConfig where each
// workspace points at a unique service directory.
func buildFanoutWorkspaces(n int) *tfc_trigger.ProjectConfig {
	wss := make([]*tfc_trigger.TFCWorkspace, 0, n)
	for i := 0; i < n; i++ {
		wss = append(wss, &tfc_trigger.TFCWorkspace{
			Name:         fmt.Sprintf("service-tfbuddy-%02d", i),
			Organization: "zapier-test",
			Mode:         "apply-before-merge",
			Dir:          fmt.Sprintf("services/svc%02d/", i),
		})
	}
	return &tfc_trigger.ProjectConfig{Workspaces: wss}
}

// TestTriggerTFCEvents_FansOutPerWorkspace verifies the production path
// publishes one message per touched workspace and does no inline TFC work
// (no clone, no per-workspace API calls). This is the durability boundary
// that lets the JetStream subscriber ACK quickly regardless of batch size.
func TestTriggerTFCEvents_FansOutPerWorkspace(t *testing.T) {
	const numWorkspaces = 20
	cfg := buildFanoutWorkspaces(numWorkspaces)

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{ProjectConfig: cfg}, t)

	modified := make([]string, 0, numWorkspaces)
	for i := 0; i < numWorkspaces; i++ {
		modified = append(modified, fmt.Sprintf("services/svc%02d/main.tf", i))
	}
	testSuite.MockGitClient.EXPECT().GetMergeRequestModifiedFiles(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS).Return(modified, nil).AnyTimes()

	// No inline run/discussion/clone work — those would all be expectation
	// failures because the mocks do not allow them. Setting up the mocks this
	// way doubles as a regression guard: if TriggerTFCEvents ever falls back
	// to the inline path while a publisher is set, the unmocked call panics.
	testSuite.InitTestSuite()

	pub := &fakeWorkspacePublisher{}
	tCfg, _ := tfc_trigger.NewTFCTriggerConfig(&tfc_trigger.TFCTriggerOptions{
		Action:                   tfc_trigger.ApplyAction,
		Branch:                   testSuite.MetaData.SourceBranch,
		CommitSHA:                "abcd12233",
		ProjectNameWithNamespace: testSuite.MetaData.ProjectNameNS,
		MergeRequestIID:          testSuite.MetaData.MRIID,
		TriggerSource:            tfc_trigger.MergeRequestEventTrigger,
	})
	trigger := tfc_trigger.NewTFCTrigger(testSuite.MockGitClient, testSuite.MockApiClient, testSuite.MockStreamClient, tCfg)
	trigger.SetWorkspaceStream(pub)

	ctx, _ := otel.Tracer("FAKE").Start(context.Background(), "TEST")
	status, err := trigger.TriggerTFCEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Errored) != 0 {
		t.Fatalf("expected no errored workspaces, got: %v", status.Errored)
	}
	if len(status.Executed) != numWorkspaces {
		t.Fatalf("expected %d enqueued workspaces, got %d (%v)", numWorkspaces, len(status.Executed), status.Executed)
	}
	got := pub.names()
	if len(got) != numWorkspaces {
		t.Fatalf("expected %d published messages, got %d", numWorkspaces, len(got))
	}
	for i, name := range got {
		want := fmt.Sprintf("service-tfbuddy-%02d", i)
		if name != want {
			t.Fatalf("publish[%d] = %s, want %s", i, name, want)
		}
	}
}

// TestTriggerTFCEvents_PublishFailureReportsErrored verifies that when the
// stream rejects a publish (NATS down, etc.) the failed workspace surfaces in
// Errored so the comment-trigger flow can still tell the user.
func TestTriggerTFCEvents_PublishFailureReportsErrored(t *testing.T) {
	cfg := buildFanoutWorkspaces(3)

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{ProjectConfig: cfg}, t)

	testSuite.MockGitClient.EXPECT().GetMergeRequestModifiedFiles(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS).Return([]string{
		"services/svc00/main.tf", "services/svc01/main.tf", "services/svc02/main.tf",
	}, nil).AnyTimes()
	testSuite.InitTestSuite()

	failName := "service-tfbuddy-01"
	pub := &fakeWorkspacePublisher{failOnce: &failName}

	tCfg, _ := tfc_trigger.NewTFCTriggerConfig(&tfc_trigger.TFCTriggerOptions{
		Action:                   tfc_trigger.PlanAction,
		Branch:                   testSuite.MetaData.SourceBranch,
		CommitSHA:                "abcd12233",
		ProjectNameWithNamespace: testSuite.MetaData.ProjectNameNS,
		MergeRequestIID:          testSuite.MetaData.MRIID,
		TriggerSource:            tfc_trigger.CommentTrigger,
	})
	trigger := tfc_trigger.NewTFCTrigger(testSuite.MockGitClient, testSuite.MockApiClient, testSuite.MockStreamClient, tCfg)
	trigger.SetWorkspaceStream(pub)

	ctx, _ := otel.Tracer("FAKE").Start(context.Background(), "TEST")
	status, err := trigger.TriggerTFCEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(status.Errored) != 1 || status.Errored[0].Name != failName {
		t.Fatalf("expected exactly one errored workspace (%s), got: %+v", failName, status.Errored)
	}
	if len(status.Executed) != 2 {
		t.Fatalf("expected the other two workspaces to enqueue, got executed=%v", status.Executed)
	}
}

// TestTriggerTFCEvents_NoChangesWithStreamSet verifies the fan-out path still
// short-circuits when no workspaces are touched. This must not enqueue any
// messages — otherwise we'd get useless TFC runs against unchanged paths.
func TestTriggerTFCEvents_NoChangesWithStreamSet(t *testing.T) {
	cfg := buildFanoutWorkspaces(2)

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{ProjectConfig: cfg}, t)

	testSuite.MockGitClient.EXPECT().GetMergeRequestModifiedFiles(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS).Return([]string{"README.md"}, nil).AnyTimes()
	testSuite.InitTestSuite()

	pub := &fakeWorkspacePublisher{}
	tCfg, _ := tfc_trigger.NewTFCTriggerConfig(&tfc_trigger.TFCTriggerOptions{
		Action:                   tfc_trigger.PlanAction,
		Branch:                   testSuite.MetaData.SourceBranch,
		CommitSHA:                "abcd12233",
		ProjectNameWithNamespace: testSuite.MetaData.ProjectNameNS,
		MergeRequestIID:          testSuite.MetaData.MRIID,
		TriggerSource:            tfc_trigger.MergeRequestEventTrigger,
	})
	trigger := tfc_trigger.NewTFCTrigger(testSuite.MockGitClient, testSuite.MockApiClient, testSuite.MockStreamClient, tCfg)
	trigger.SetWorkspaceStream(pub)

	ctx, _ := otel.Tracer("FAKE").Start(context.Background(), "TEST")
	status, err := trigger.TriggerTFCEvents(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != nil {
		t.Fatalf("expected nil status when no workspaces touched, got: %+v", status)
	}
	if got := len(pub.msgs); got != 0 {
		t.Fatalf("expected zero published messages, got %d", got)
	}
}

// TestWorkspaceTriggerMsg_GetIdIsStable guards the JetStream dedup key: a
// duplicate webhook (or redelivery) must produce the same ID for the same
// (project, MR, commit, action, workspace) tuple, otherwise NATS will accept
// the duplicate and we'll enqueue twice.
func TestWorkspaceTriggerMsg_GetIdIsStable(t *testing.T) {
	mk := func() *tfc_trigger.WorkspaceTriggerMsg {
		return &tfc_trigger.WorkspaceTriggerMsg{
			Opts: tfc_trigger.TFCTriggerOptions{
				Action:                   tfc_trigger.PlanAction,
				ProjectNameWithNamespace: "zapier/tfbuddy",
				MergeRequestIID:          42,
				CommitSHA:                "deadbeef",
				VcsProvider:              "gitlab",
			},
			Workspace: tfc_trigger.TFCWorkspace{Name: "service-tfbuddy", Organization: "zapier-test"},
		}
	}
	ctx := context.Background()
	a, b := mk().GetId(ctx), mk().GetId(ctx)
	if a != b {
		t.Fatalf("GetId not deterministic: %s vs %s", a, b)
	}

	// Different workspace must produce a different ID, otherwise we'd dedupe
	// across workspaces and silently drop fan-out messages.
	other := mk()
	other.Workspace.Name = "service-tfbuddy-staging"
	if other.GetId(ctx) == a {
		t.Fatal("GetId collided across workspaces")
	}

	// Different action (plan vs apply) must dedup independently — otherwise
	// commenting `tfc apply` after `tfc plan` on the same commit would be
	// silently dropped within the dedup window.
	otherAction := mk()
	otherAction.Opts.Action = tfc_trigger.ApplyAction
	if otherAction.GetId(ctx) == a {
		t.Fatal("GetId collided across actions")
	}

	// Different commit must dedup independently — otherwise re-pushing a
	// branch with new code wouldn't re-trigger plans during the dedup window.
	otherCommit := mk()
	otherCommit.Opts.CommitSHA = "feedface"
	if otherCommit.GetId(ctx) == a {
		t.Fatal("GetId collided across commits")
	}

	// Pipe characters in workspace names mustn't collide with other tuples
	// (regression guard for the previous delimiter-based encoding).
	weird := mk()
	weird.Workspace.Name = "service|name|with|pipes"
	weird2 := mk()
	weird2.Opts.ProjectNameWithNamespace = "service|name|with|pipes"
	if weird.GetId(ctx) == weird2.GetId(ctx) {
		t.Fatal("GetId collides when the workspace name contains the old delimiter")
	}
}

// TestWorkspaceTriggerMsg_EncodeDecodeRoundtrips ensures the over-the-wire
// representation preserves enough state to fully reconstruct the trigger in
// the workspace worker (no shared state between MR worker and ws worker).
func TestWorkspaceTriggerMsg_EncodeDecodeRoundtrips(t *testing.T) {
	original := &tfc_trigger.WorkspaceTriggerMsg{
		Opts: tfc_trigger.TFCTriggerOptions{
			Action:                   tfc_trigger.ApplyAction,
			Branch:                   "feature-x",
			CommitSHA:                "deadbeef",
			ProjectNameWithNamespace: "zapier/tfbuddy",
			MergeRequestIID:          42,
			TriggerSource:            tfc_trigger.MergeRequestEventTrigger,
			VcsProvider:              "gitlab",
			TFVersion:                "1.5.0",
			AllowEmptyRun:            true,
		},
		Workspace: tfc_trigger.TFCWorkspace{
			Name:         "service-tfbuddy",
			Organization: "zapier-test",
			Mode:         "apply-before-merge",
			Dir:          "production/",
			TriggerDirs:  []string{"production/**"},
			AutoMerge:    true,
		},
	}
	data := original.EncodeEventData(context.Background())
	if len(data) == 0 {
		t.Fatal("encoded payload is empty")
	}

	decoded := &tfc_trigger.WorkspaceTriggerMsg{}
	if err := decoded.DecodeEventData(data); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if decoded.Opts.Action != original.Opts.Action ||
		decoded.Opts.MergeRequestIID != original.Opts.MergeRequestIID ||
		decoded.Opts.CommitSHA != original.Opts.CommitSHA ||
		decoded.Opts.VcsProvider != original.Opts.VcsProvider ||
		decoded.Workspace.Name != original.Workspace.Name ||
		decoded.Workspace.Mode != original.Workspace.Mode ||
		decoded.Workspace.AutoMerge != original.Workspace.AutoMerge {
		t.Fatalf("roundtrip lost state.\n original=%+v\n decoded=%+v", original, decoded)
	}
}

// TestTriggerTFCEvents_FanoutIsConcurrencySafe runs many enqueue rounds
// concurrently; the workspace-local cfg/discussion/note fix means each
// trigger keeps its own state and the publisher never sees crossed wires.
// Run with -race to catch regressions.
func TestTriggerTFCEvents_FanoutIsConcurrencySafe(t *testing.T) {
	const numWorkspaces = 8
	cfg := buildFanoutWorkspaces(numWorkspaces)

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{ProjectConfig: cfg}, t)

	modified := make([]string, 0, numWorkspaces)
	for i := 0; i < numWorkspaces; i++ {
		modified = append(modified, fmt.Sprintf("services/svc%02d/main.tf", i))
	}
	testSuite.MockGitClient.EXPECT().GetMergeRequestModifiedFiles(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS).Return(modified, nil).AnyTimes()
	testSuite.InitTestSuite()

	tCfg, _ := tfc_trigger.NewTFCTriggerConfig(&tfc_trigger.TFCTriggerOptions{
		Action:                   tfc_trigger.PlanAction,
		Branch:                   testSuite.MetaData.SourceBranch,
		CommitSHA:                "abcd12233",
		ProjectNameWithNamespace: testSuite.MetaData.ProjectNameNS,
		MergeRequestIID:          testSuite.MetaData.MRIID,
		TriggerSource:            tfc_trigger.MergeRequestEventTrigger,
	})
	trigger := tfc_trigger.NewTFCTrigger(testSuite.MockGitClient, testSuite.MockApiClient, testSuite.MockStreamClient, tCfg)

	pub := &fakeWorkspacePublisher{}
	trigger.SetWorkspaceStream(pub)

	const rounds = 16
	var wg sync.WaitGroup
	var failures int32
	for i := 0; i < rounds; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, _ := otel.Tracer("FAKE").Start(context.Background(), "TEST")
			if _, err := trigger.TriggerTFCEvents(ctx); err != nil {
				atomic.AddInt32(&failures, 1)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&failures); got != 0 {
		t.Fatalf("%d concurrent TriggerTFCEvents calls failed", got)
	}
	if got := len(pub.msgs); got != rounds*numWorkspaces {
		t.Fatalf("expected %d publish calls, got %d", rounds*numWorkspaces, got)
	}
}
