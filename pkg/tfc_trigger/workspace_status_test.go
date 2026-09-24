package tfc_trigger_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/go-tfe"
	"github.com/spf13/viper"
	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.uber.org/mock/gomock"
)

// captureStatuses records every status the trigger posts, keyed by workspace.
// It must be declared before InitTestSuite: gomock returns the earliest
// declared non-exhausted expectation, so the AnyTimes() default registered by
// InitTestSuite would otherwise swallow every call.
func captureStatuses(ts *mocks.TestSuite) *map[string]vcs.WorkspaceStatus {
	seen := map[string]vcs.WorkspaceStatus{}
	ts.MockGitClient.EXPECT().SetWorkspaceStatus(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s vcs.WorkspaceStatus) error {
			seen[s.Workspace] = s
			return nil
		}).AnyTimes()
	return &seen
}

func planTrigger(t *testing.T, ts *mocks.TestSuite, action tfc_trigger.TriggerAction) tfc_trigger.Trigger {
	t.Helper()
	tCfg, err := tfc_trigger.NewTFCTriggerConfig(&tfc_trigger.TFCTriggerOptions{
		Action:                   action,
		Branch:                   "test-branch",
		CommitSHA:                "abcd12233",
		ProjectNameWithNamespace: ts.MetaData.ProjectNameNS,
		MergeRequestIID:          ts.MetaData.MRIID,
		TriggerSource:            tfc_trigger.CommentTrigger,
	})
	if err != nil {
		t.Fatal(err)
	}
	return tfc_trigger.NewTFCTrigger(config.C, ts.MockGitClient, ts.MockApiClient, ts.MockStreamClient, tCfg)
}

// denyWorkspace points the deny list at one workspace for the duration of a test.
func denyWorkspace(t *testing.T, fullName string) {
	t.Helper()
	viper.Reset()
	config.Init()
	t.Cleanup(func() { viper.Reset(); config.Init() })
	viper.Set(config.KeyWorkspaceDenyList, fullName)
	config.Reload()
}

// A deny-listed workspace is an operator decision, not a mistake. It must show
// up as a skipped job so the author knows it did not run, without going red
// and permanently blocking the merge.
func TestDispatch_DeniedWorkspacePostsSkippedStatus(t *testing.T) {
	denyWorkspace(t, "zapier-test/service-tfbuddy")

	ws := &tfc_trigger.ProjectConfig{Workspaces: []*tfc_trigger.TFCWorkspace{{
		Name: "service-tfbuddy", Organization: "zapier-test", Mode: "apply-before-merge",
	}}}

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{ProjectConfig: ws}, t)
	seen := captureStatuses(testSuite)
	testSuite.InitTestSuite()

	trigger := planTrigger(t, testSuite, tfc_trigger.PlanAction)
	if _, err := trigger.TriggerTFCEvents(context.Background()); err != nil {
		t.Fatal(err)
	}

	got, ok := (*seen)["service-tfbuddy"]
	if !ok {
		t.Fatal("expected a commit status for the denied workspace, got none")
	}
	if got.State != vcs.CommitStateSkipped {
		t.Errorf("state = %q, want skipped", got.State)
	}
	if got.Action != "plan" {
		t.Errorf("action = %q, want plan", got.Action)
	}
	if got.Description == "" {
		t.Error("a skipped status must say why it was skipped")
	}
}

// A workspace that does not exist in TFC is a mistake, so it goes red.
func TestDispatch_MissingTFCWorkspacePostsFailedStatus(t *testing.T) {
	ws := &tfc_trigger.ProjectConfig{Workspaces: []*tfc_trigger.TFCWorkspace{{
		Name: "service-tfbuddy", Organization: "zapier-test", Mode: "apply-before-merge",
	}}}

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{ProjectConfig: ws}, t)
	testSuite.MockApiClient.EXPECT().GetWorkspaceByName(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, errors.New("resource not found")).AnyTimes()
	seen := captureStatuses(testSuite)
	testSuite.InitTestSuite()

	trigger := planTrigger(t, testSuite, tfc_trigger.PlanAction)
	if _, err := trigger.TriggerTFCEvents(context.Background()); err != nil {
		t.Fatal(err)
	}

	got, ok := (*seen)["service-tfbuddy"]
	if !ok {
		t.Fatal("expected a commit status for the missing workspace, got none")
	}
	if got.State != vcs.CommitStateFailed {
		t.Errorf("state = %q, want failed", got.State)
	}
}

// A workspace blocked because the target branch moved is transient and
// actionable, so it goes red with a rebase instruction rather than silently
// disappearing from the pipeline.
func TestDispatch_DivergedWorkspacePostsFailedStatus(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	// Root-level changes on both branches put the default workspace into the
	// blocked set, the same way TestTFCEvents_WorkspaceApplyModifiedBothSrcDstBranches does.
	testSuite.MockGitRepo.EXPECT().GetModifiedFileNamesBetweenCommits(testSuite.MetaData.CommonSHA, "main").
		Return([]string{"terraform.tf"}, nil)
	testSuite.MockGitClient.EXPECT().GetMergeRequestModifiedFiles(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS).
		Return([]string{"main.tf"}, nil)
	seen := captureStatuses(testSuite)
	testSuite.InitTestSuite()

	trigger := planTrigger(t, testSuite, tfc_trigger.PlanAction)
	if _, err := trigger.TriggerTFCEvents(context.Background()); err != nil {
		t.Fatal(err)
	}

	got, ok := (*seen)[mocks.TF_WORKSPACE_NAME]
	if !ok {
		t.Fatal("expected a commit status for the blocked workspace, got none")
	}
	if got.State != vcs.CommitStateFailed {
		t.Errorf("state = %q, want failed", got.State)
	}
	if !strings.Contains(got.Description, "rebase") {
		t.Errorf("a blocked status should tell the author how to resolve it, got %q", got.Description)
	}
}

// Lock and unlock must not invent TFC/lock/<ws> pipeline jobs.
//
// The workspace is deny-listed so the allow/deny exit in dispatchWorkspaces is
// genuinely reached: without that, no exit fires and the test would pass even
// if postWorkspaceStatus had no action guard at all. captureStatuses is used
// rather than Times(0) because a Times(0) expectation is already exhausted at
// declaration, so gomock skips it and falls through to the AnyTimes() default
// registered by InitTestSuite, which swallows the call.
func TestDispatch_LockActionPostsNoStatus(t *testing.T) {
	denyWorkspace(t, "zapier-test/service-tfbuddy")

	ws := &tfc_trigger.ProjectConfig{Workspaces: []*tfc_trigger.TFCWorkspace{{
		Name: "service-tfbuddy", Organization: "zapier-test", Mode: "apply-before-merge",
	}}}

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{ProjectConfig: ws}, t)
	seen := captureStatuses(testSuite)
	testSuite.InitTestSuite()

	trigger := planTrigger(t, testSuite, tfc_trigger.LockAction)
	triggered, err := trigger.TriggerTFCEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// The deny-list exit fired, so a status would have been posted if the
	// action guard were missing.
	if len(triggered.Errored) != 1 {
		t.Fatalf("expected the deny-list exit to fire, got Errored=%d", len(triggered.Errored))
	}
	if len(*seen) != 0 {
		t.Fatalf("lock must not post pipeline statuses, got %+v", *seen)
	}
}

// A status is a notification. Losing one must not change what TFBuddy does.
func TestDispatch_StatusFailureDoesNotBreakTheTrigger(t *testing.T) {
	denyWorkspace(t, "zapier-test/service-tfbuddy")

	ws := &tfc_trigger.ProjectConfig{Workspaces: []*tfc_trigger.TFCWorkspace{{
		Name: "service-tfbuddy", Organization: "zapier-test", Mode: "apply-before-merge",
	}}}

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{ProjectConfig: ws}, t)
	testSuite.MockGitClient.EXPECT().SetWorkspaceStatus(gomock.Any(), gomock.Any()).
		Return(errors.New("GitLab API down")).AnyTimes()
	testSuite.InitTestSuite()

	trigger := planTrigger(t, testSuite, tfc_trigger.PlanAction)
	triggered, err := trigger.TriggerTFCEvents(context.Background())
	if err != nil {
		t.Fatalf("a failed status write must not fail the trigger: %v", err)
	}
	if len(triggered.Errored) != 1 {
		t.Fatalf("expected the workspace still recorded as errored, got %d", len(triggered.Errored))
	}
}

// One bad workspace must not stop its neighbors from running.
func TestDispatch_DeniedWorkspaceDoesNotBlockHealthyOne(t *testing.T) {
	denyWorkspace(t, "zapier-test/svc-denied")

	ws := &tfc_trigger.ProjectConfig{Workspaces: []*tfc_trigger.TFCWorkspace{
		{Name: "svc-denied", Organization: "zapier-test", Dir: "denied", Mode: "apply-before-merge"},
		{Name: "svc-healthy", Organization: "zapier-test", Dir: "healthy", Mode: "apply-before-merge"},
	}}

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{ProjectConfig: ws}, t)
	testSuite.MockGitClient.EXPECT().GetMergeRequestModifiedFiles(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS).
		Return([]string{"denied/main.tf", "healthy/main.tf"}, nil).AnyTimes()
	testSuite.MockGitClient.EXPECT().CreateMergeRequestDiscussion(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(testSuite.MockGitDisc, nil).AnyTimes()
	// publishRunToStream reads run.Workspace.Organization.Name, so the fake run
	// needs a fully populated workspace or it panics on a nil dereference.
	testSuite.MockApiClient.EXPECT().CreateRunFromSource(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, opts *tfc_api.ApiRunOptions) (*tfe.Run, error) {
			return &tfe.Run{
				ID:                   "run-" + opts.Workspace,
				Workspace:            &tfe.Workspace{Name: opts.Workspace, Organization: &tfe.Organization{Name: "zapier-test"}},
				ConfigurationVersion: &tfe.ConfigurationVersion{Speculative: false},
			}, nil
		}).AnyTimes()
	seen := captureStatuses(testSuite)
	testSuite.InitTestSuite()

	trigger := planTrigger(t, testSuite, tfc_trigger.PlanAction)
	triggered, err := trigger.TriggerTFCEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if got := (*seen)["svc-denied"].State; got != vcs.CommitStateSkipped {
		t.Errorf("denied workspace state = %q, want skipped", got)
	}
	if _, posted := (*seen)["svc-healthy"]; posted {
		t.Error("the healthy workspace should get its status from the run-event path, not from dispatch")
	}
	if len(triggered.Executed) != 1 || triggered.Executed[0] != "svc-healthy" {
		t.Errorf("expected svc-healthy to run, got Executed=%v", triggered.Executed)
	}
}

// A run existing is not the same as the run-event path being able to report on
// it. AddRunMeta writes the metadata that waitForTFRunMetadata later reads; if
// that write fails, PublishTFRunEvent returns early and no run event is ever
// published, so nothing will ever update this workspace's status. Dispatch
// must post the failed status itself or the workspace disappears — the exact
// bug this feature exists to fix.
func TestDispatch_AddRunMetaFailureStillPostsFailedStatus(t *testing.T) {
	ws := &tfc_trigger.ProjectConfig{Workspaces: []*tfc_trigger.TFCWorkspace{{
		Name: "service-tfbuddy", Organization: "zapier-test", Mode: "apply-before-merge",
	}}}

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{ProjectConfig: ws}, t)

	testSuite.MockGitClient.EXPECT().CreateMergeRequestDiscussion(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(testSuite.MockGitDisc, nil).AnyTimes()
	testSuite.MockApiClient.EXPECT().CreateRunFromSource(gomock.Any(), gomock.Any()).
		Return(&tfe.Run{
			ID:                   "run-1",
			Workspace:            &tfe.Workspace{Name: "service-tfbuddy", Organization: &tfe.Organization{Name: "zapier-test"}},
			ConfigurationVersion: &tfe.ConfigurationVersion{Speculative: false},
		}, nil).AnyTimes()
	testSuite.MockStreamClient.EXPECT().AddRunMeta(gomock.Any()).
		Return(errors.New("NATS unavailable")).AnyTimes()

	seen := captureStatuses(testSuite)
	testSuite.InitTestSuite()

	trigger := planTrigger(t, testSuite, tfc_trigger.PlanAction)
	triggered, err := trigger.TriggerTFCEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(triggered.Errored) != 1 {
		t.Fatalf("the workspace should be recorded as errored, got %d", len(triggered.Errored))
	}
	got, ok := (*seen)["service-tfbuddy"]
	if !ok {
		t.Fatal("no run event can ever arrive without run metadata, so dispatch must post the status")
	}
	if got.State != vcs.CommitStateFailed {
		t.Errorf("state = %q, want failed", got.State)
	}
}

// A speculative plan gets no TFC webhooks at all, so the polling task is its
// only status source. If scheduling that task fails, dispatch must post.
func TestDispatch_SpeculativePollerScheduleFailureStillPostsFailedStatus(t *testing.T) {
	ws := &tfc_trigger.ProjectConfig{Workspaces: []*tfc_trigger.TFCWorkspace{{
		Name: "service-tfbuddy", Organization: "zapier-test", Mode: "apply-before-merge",
	}}}

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{ProjectConfig: ws}, t)

	testSuite.MockGitClient.EXPECT().CreateMergeRequestDiscussion(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(testSuite.MockGitDisc, nil).AnyTimes()
	testSuite.MockApiClient.EXPECT().CreateRunFromSource(gomock.Any(), gomock.Any()).
		Return(&tfe.Run{
			ID:                   "run-1",
			Workspace:            &tfe.Workspace{Name: "service-tfbuddy", Organization: &tfe.Organization{Name: "zapier-test"}},
			ConfigurationVersion: &tfe.ConfigurationVersion{Speculative: true},
		}, nil).AnyTimes()

	pollTask := mocks.NewMockRunPollingTask(mockCtrl)
	pollTask.EXPECT().Schedule(gomock.Any()).Return(errors.New("scheduler down")).AnyTimes()
	testSuite.MockStreamClient.EXPECT().NewTFRunPollingTask(gomock.Any(), gomock.Any()).Return(pollTask).AnyTimes()

	seen := captureStatuses(testSuite)
	testSuite.InitTestSuite()

	trigger := planTrigger(t, testSuite, tfc_trigger.PlanAction)
	if _, err := trigger.TriggerTFCEvents(context.Background()); err != nil {
		t.Fatal(err)
	}

	got, ok := (*seen)["service-tfbuddy"]
	if !ok {
		t.Fatal("a speculative plan with no poller has no status source; dispatch must post")
	}
	if got.State != vcs.CommitStateFailed {
		t.Errorf("state = %q, want failed", got.State)
	}
}

// The sentinel is still correct for failures raised once the run-event path is
// established: the metadata is live, so TFC webhooks will drive this
// workspace's status and only auto-merge coordination was lost. Posting failed
// here could strand the workspace red after a successful apply.
func TestDispatch_AutoMergeRegistrationFailureDoesNotPostFailedStatus(t *testing.T) {
	ws := &tfc_trigger.ProjectConfig{Workspaces: []*tfc_trigger.TFCWorkspace{{
		Name: "service-tfbuddy", Organization: "zapier-test", Mode: "apply-before-merge",
	}}}

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{ProjectConfig: ws}, t)

	testSuite.MockGitClient.EXPECT().CreateMergeRequestDiscussion(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(testSuite.MockGitDisc, nil).AnyTimes()
	testSuite.MockApiClient.EXPECT().GetWorkspaceByName(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&tfe.Workspace{ID: "service-tfbuddy"}, nil).AnyTimes()
	testSuite.MockApiClient.EXPECT().CreateRunFromSource(gomock.Any(), gomock.Any()).
		Return(&tfe.Run{
			ID:                   "run-1",
			Workspace:            &tfe.Workspace{Name: "service-tfbuddy", Organization: &tfe.Organization{Name: "zapier-test"}},
			ConfigurationVersion: &tfe.ConfigurationVersion{Speculative: false},
		}, nil).AnyTimes()
	testSuite.MockStreamClient.EXPECT().AddRunMeta(gomock.Any()).Return(nil).AnyTimes()
	testSuite.MockStreamClient.EXPECT().RegisterAutoMergeRun(gomock.Any()).
		Return(errors.New("KV write failed")).AnyTimes()

	seen := captureStatuses(testSuite)
	testSuite.InitTestSuite()

	tCfg, err := tfc_trigger.NewTFCTriggerConfig(&tfc_trigger.TFCTriggerOptions{
		Action:                   tfc_trigger.ApplyAction,
		Branch:                   "test-branch",
		CommitSHA:                "abcd12233",
		ProjectNameWithNamespace: testSuite.MetaData.ProjectNameNS,
		MergeRequestIID:          testSuite.MetaData.MRIID,
		TriggerSource:            tfc_trigger.CommentTrigger,
		VcsProvider:              "gitlab",
		AutoMergeGeneration:      "delivery-123",
	})
	if err != nil {
		t.Fatal(err)
	}
	trigger := tfc_trigger.NewTFCTrigger(config.C, testSuite.MockGitClient, testSuite.MockApiClient, testSuite.MockStreamClient, tCfg)

	triggered, err := trigger.TriggerTFCEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(triggered.Errored) != 1 {
		t.Fatalf("the workspace should still be recorded as errored, got %d", len(triggered.Errored))
	}
	if got, posted := (*seen)["service-tfbuddy"]; posted {
		t.Fatalf("run metadata is live so the run-event path owns this status, got %+v", got)
	}
}

// The budget that keeps the synchronous webhook handler inside its JetStream
// AckWait lives in the caller, not in the GitLab client: the asynchronous
// run-event path must keep the long window. This pins that the dispatch path
// actually imposes one, and that a hanging GitLab cannot outlast it.
func TestDispatch_StatusWriteIsBounded(t *testing.T) {
	denyWorkspace(t, "zapier-test/service-tfbuddy")

	ws := &tfc_trigger.ProjectConfig{Workspaces: []*tfc_trigger.TFCWorkspace{{
		Name: "service-tfbuddy", Organization: "zapier-test", Mode: "apply-before-merge",
	}}}

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{ProjectConfig: ws}, t)

	var hadDeadline bool
	var budget time.Duration
	testSuite.MockGitClient.EXPECT().SetWorkspaceStatus(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ vcs.WorkspaceStatus) error {
			deadline, ok := ctx.Deadline()
			hadDeadline = ok
			if ok {
				budget = time.Until(deadline)
			}
			// A GitLab that never answers must not hold the handler open.
			<-ctx.Done()
			return ctx.Err()
		}).AnyTimes()
	testSuite.InitTestSuite()

	trigger := planTrigger(t, testSuite, tfc_trigger.PlanAction)
	start := time.Now()
	if _, err := trigger.TriggerTFCEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)

	if !hadDeadline {
		t.Fatal("the dispatch path must bound its status writes; a hanging GitLab would otherwise outlast AckWait")
	}
	if budget > 10*time.Second {
		t.Errorf("status write budget is %s, too close to the 30s AckWait", budget)
	}
	if elapsed > 10*time.Second {
		t.Errorf("a hanging status write held dispatch for %s", elapsed)
	}
}
