package tfc_trigger_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"go.opentelemetry.io/otel"
	"go.uber.org/mock/gomock"

	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
)

// fakeWorkspacePublisher is an in-memory stand-in for WorkspaceStream.
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

// TestTriggerTFCEvents_FansOutPerWorkspace asserts one publish per touched
// workspace and no inline clone/API work. Unmocked inline calls would panic.
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
	trigger := tfc_trigger.NewTFCTrigger(config.Config{}, testSuite.MockGitClient, testSuite.MockApiClient, testSuite.MockStreamClient, tCfg)
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

// TestTriggerTFCEvents_PublishFailureReportsErrored asserts that a publish
// failure surfaces in Errored so comment-trigger callers can surface it.
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
	trigger := tfc_trigger.NewTFCTrigger(config.Config{}, testSuite.MockGitClient, testSuite.MockApiClient, testSuite.MockStreamClient, tCfg)
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

// TestTriggerTFCEvents_NoChangesWithStreamSet asserts the fan-out path
// short-circuits when no workspaces are touched.
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
	trigger := tfc_trigger.NewTFCTrigger(config.Config{}, testSuite.MockGitClient, testSuite.MockApiClient, testSuite.MockStreamClient, tCfg)
	trigger.SetWorkspaceStream(pub)

	ctx, _ := otel.Tracer("FAKE").Start(context.Background(), "TEST")
	status, err := trigger.TriggerTFCEvents(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status == nil {
		t.Fatal("expected non-nil empty status when no workspaces touched, got nil")
	}
	if len(status.Errored) != 0 || len(status.Executed) != 0 {
		t.Fatalf("expected empty status when no workspaces touched, got: %+v", status)
	}
	if got := len(pub.msgs); got != 0 {
		t.Fatalf("expected zero published messages, got %d", got)
	}
}

// TestWorkspaceTriggerMsg_GetIdIsStable asserts the dedup key is deterministic
// for the same tuple and distinct across workspace / action / commit changes.
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

	other := mk()
	other.Workspace.Name = "service-tfbuddy-staging"
	if other.GetId(ctx) == a {
		t.Fatal("GetId collided across workspaces")
	}

	otherAction := mk()
	otherAction.Opts.Action = tfc_trigger.ApplyAction
	if otherAction.GetId(ctx) == a {
		t.Fatal("GetId collided across actions")
	}

	otherCommit := mk()
	otherCommit.Opts.CommitSHA = "feedface"
	if otherCommit.GetId(ctx) == a {
		t.Fatal("GetId collided across commits")
	}

	// Regression guard for the previous pipe-delimited encoding.
	weird := mk()
	weird.Workspace.Name = "service|name|with|pipes"
	weird2 := mk()
	weird2.Opts.ProjectNameWithNamespace = "service|name|with|pipes"
	if weird.GetId(ctx) == weird2.GetId(ctx) {
		t.Fatal("GetId collides when the workspace name contains the old delimiter")
	}
}

// TestWorkspaceTriggerMsg_GetIdAnchorsOnDeliveryID asserts the dedup key is
// anchored to the webhook delivery so retriggers aren't silently dropped.
func TestWorkspaceTriggerMsg_GetIdAnchorsOnDeliveryID(t *testing.T) {
	ctx := context.Background()
	mk := func(deliveryID string) *tfc_trigger.WorkspaceTriggerMsg {
		return &tfc_trigger.WorkspaceTriggerMsg{
			Opts: tfc_trigger.TFCTriggerOptions{
				Action:                   tfc_trigger.PlanAction,
				ProjectNameWithNamespace: "zapier/tfbuddy",
				MergeRequestIID:          42,
				CommitSHA:                "deadbeef",
				VcsProvider:              "gitlab",
				DeliveryID:               deliveryID,
			},
			Workspace: tfc_trigger.TFCWorkspace{Name: "service-tfbuddy", Organization: "zapier-test"},
		}
	}

	first := mk("delivery-aaa").GetId(ctx)
	second := mk("delivery-bbb").GetId(ctx)
	if first == second {
		t.Fatalf("retrigger from a different webhook delivery must produce a fresh dedup key, got %q", first)
	}

	dup := mk("delivery-aaa").GetId(ctx)
	if dup != first {
		t.Fatalf("same delivery+workspace must produce the same dedup key (%q vs %q)", first, dup)
	}

	siblingWS := mk("delivery-aaa")
	siblingWS.Workspace.Name = "service-tfbuddy-staging"
	if siblingWS.GetId(ctx) == first {
		t.Fatal("same delivery across workspaces must not collide")
	}

	// Delivery ID stays embedded for greppability during incident triage.
	if !strings.Contains(first, "delivery-aaa") {
		t.Fatalf("expected delivery ID embedded in dedup key, got %q", first)
	}

	// Workspace name and org are embedded directly (no hash).
	if !strings.Contains(first, "service-tfbuddy") {
		t.Fatalf("expected workspace name embedded in dedup key, got %q", first)
	}
	if !strings.Contains(first, "zapier-test") {
		t.Fatalf("expected org embedded in dedup key, got %q", first)
	}

	// Verify the exact format: DeliveryID/Workspace.Name/Workspace.Organization
	expected := "delivery-aaa/service-tfbuddy/zapier-test"
	if first != expected {
		t.Fatalf("expected key %q, got %q", expected, first)
	}
}

// TestWorkspaceTriggerMsg_GetIdNoDelimiterCollision regresses encoding bugs
// where ambiguous splits could produce the same key for different tuples.
func TestWorkspaceTriggerMsg_GetIdNoDelimiterCollision(t *testing.T) {
	ctx := context.Background()
	mk := func(deliveryID, org, name string) *tfc_trigger.WorkspaceTriggerMsg {
		return &tfc_trigger.WorkspaceTriggerMsg{
			Opts: tfc_trigger.TFCTriggerOptions{
				Action:                   tfc_trigger.PlanAction,
				ProjectNameWithNamespace: "zapier/tfbuddy",
				MergeRequestIID:          42,
				CommitSHA:                "deadbeef",
				VcsProvider:              "gitlab",
				DeliveryID:               deliveryID,
			},
			Workspace: tfc_trigger.TFCWorkspace{Organization: org, Name: name},
		}
	}

	cases := []struct {
		name string
		a, b *tfc_trigger.WorkspaceTriggerMsg
	}{
		{
			name: "dash in delivery vs start of workspace name",
			a:    mk("abc-def", "ws1", "prod"),
			b:    mk("abc", "def-ws1", "prod"),
		},
		{
			name: "dash in org vs start of workspace name",
			a:    mk("abc", "ws1", "prod-extra"),
			b:    mk("abc", "ws1-prod", "extra"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.a.GetId(ctx) == tc.b.GetId(ctx) {
				t.Fatalf("GetId collided across distinct tuples:\n  %+v\n  %+v", tc.a, tc.b)
			}
		})
	}
}

// TestWorkspaceTriggerMsg_EncodeDecodeRoundtrips asserts the over-the-wire
// payload preserves the state the worker needs.
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

// TestTriggerTFCEvents_FanoutIsConcurrencySafe drives concurrent enqueues to
// catch shared-state regressions (run with -race).
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
	trigger := tfc_trigger.NewTFCTrigger(config.Config{}, testSuite.MockGitClient, testSuite.MockApiClient, testSuite.MockStreamClient, tCfg)

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
