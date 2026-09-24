package tfc_trigger_test

import (
	"context"
	"errors"
	"strings"
	"testing"

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

// Once CreateRunFromSource succeeds the TFC run is live and the run-event path
// owns its commit status, driving it running -> success. A failure raised
// after that point (publishing run metadata, registering for auto-merge,
// scheduling the poll) must not also post a failed status: if that write lands
// after the run-event success, the workspace reads red forever despite a
// successful apply.
func TestDispatch_FailureAfterRunCreationDoesNotPostFailedStatus(t *testing.T) {
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
	// The run exists; publishing its metadata is what fails.
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
		t.Fatalf("the workspace should still be recorded as errored, got %d", len(triggered.Errored))
	}
	if got, posted := (*seen)["service-tfbuddy"]; posted {
		t.Fatalf("no status may be posted once the run exists; the run-event path owns it, got %+v", got)
	}
}

// A failure that kills the whole merge request — cloning the repo, or
// initializing auto-merge state — happens after the triggered workspaces are
// known but before dispatch. cloneGitRepo wraps its error with
// utils.CreatePermanentError, so the delivery is ACKed and never redelivered:
// without a status here the MR-event path discloses nothing at all, which is
// the exact silence this feature exists to remove.
func TestTriggerTFCEvents_CloneFailurePostsFailedStatusForEveryWorkspace(t *testing.T) {
	ws := &tfc_trigger.ProjectConfig{Workspaces: []*tfc_trigger.TFCWorkspace{
		{Name: "svc-a", Organization: "zapier-test", Dir: "a", Mode: "apply-before-merge"},
		{Name: "svc-b", Organization: "zapier-test", Dir: "b", Mode: "apply-before-merge"},
	}}

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{ProjectConfig: ws}, t)

	testSuite.MockGitClient.EXPECT().GetMergeRequestModifiedFiles(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS).
		Return([]string{"a/main.tf", "b/main.tf"}, nil).AnyTimes()
	testSuite.MockGitClient.EXPECT().CloneMergeRequest(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, errors.New("repository not found")).AnyTimes()

	seen := captureStatuses(testSuite)
	testSuite.InitTestSuite()

	trigger := planTrigger(t, testSuite, tfc_trigger.PlanAction)
	if _, err := trigger.TriggerTFCEvents(context.Background()); err == nil {
		t.Fatal("expected the clone failure to be returned")
	}

	for _, name := range []string{"svc-a", "svc-b"} {
		got, ok := (*seen)[name]
		if !ok {
			t.Fatalf("%s got no status; a permanent clone failure is never redelivered, so this is silent forever", name)
		}
		if got.State != vcs.CommitStateFailed {
			t.Errorf("%s state = %q, want failed", name, got.State)
		}
	}
}
