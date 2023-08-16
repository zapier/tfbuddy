package tfc_trigger_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/hashicorp/go-tfe"
	"github.com/rs/zerolog/log"
	"github.com/rzajac/zltest"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"go.opentelemetry.io/otel"
)

func TestTriggerAction_String(t *testing.T) {
	tests := []struct {
		name string
		a    tfc_trigger.TriggerAction
		want string
	}{
		{
			"apply",
			tfc_trigger.ApplyAction,
			"apply",
		},
		{
			"plan",
			tfc_trigger.PlanAction,
			"plan",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.String(); got != tt.want {
				t.Fatalf("String() = %v, want %v", got, tt.want)
			}
			if got := fmt.Sprintf("%v", tt.a); got != tt.want {
				t.Fatalf("String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFindLockingMR(t *testing.T) {
	tests := []struct {
		name string
		tags []string
		MR   string
		want string
	}{
		{
			"no MR",
			[]string{"foo", "bar"},
			"1",
			"",
		},
		{
			"different MR locked",
			[]string{"tfbuddylock-2", "foo"},
			"1",
			"2",
		},
		{
			"This and other MR locked",
			[]string{"tfbuddylock-2", "tfbuddylock-1", "bar"},
			"2",
			"1",
		},
		{
			"Two digit lock",
			[]string{"tfbuddylock-20", "tfbuddylock-1", "foo"},
			"1",
			"20",
		},
		{
			"Word after prefix",
			[]string{"tfbuddylock-pineapple", "tfbuddylock-1"},
			"1",
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tfc_trigger.FindLockingMR(context.Background(), tt.tags, tt.MR)
			if got != tt.want {
				t.Fatalf("didn't match got: %s, want: %s", got, tt.want)
			}
		})
	}
}

func TestTFCEvents_SingleWorkspacePlan(t *testing.T) {

	ws := &tfc_trigger.ProjectConfig{
		Workspaces: []*tfc_trigger.TFCWorkspace{{
			Name:         "service-tfbuddy",
			Organization: "zapier-test",
			Mode:         "apply-before-merge",
		}}}

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{ProjectConfig: ws}, t)
	testSuite.MockGitClient.EXPECT().CreateMergeRequestDiscussion(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS, "Starting TFC plan for Workspace: `zapier-test/service-tfbuddy`.").Return(testSuite.MockGitDisc, nil)
	testSuite.MockApiClient.EXPECT().CreateRunFromSource(gomock.Any(), gomock.Any()).Return(&tfe.Run{
		ID: "101",
		Workspace: &tfe.Workspace{Name: "service-tfbuddy",
			Organization: &tfe.Organization{Name: "zapier-test"},
		},
		ConfigurationVersion: &tfe.ConfigurationVersion{Speculative: true}}, nil)

	mockRunPollingTask := mocks.NewMockRunPollingTask(mockCtrl)
	mockRunPollingTask.EXPECT().Schedule(gomock.Any())

	testSuite.MockStreamClient.EXPECT().NewTFRunPollingTask(gomock.Any(), time.Second*1).Return(mockRunPollingTask)

	testSuite.InitTestSuite()

	tCfg, _ := tfc_trigger.NewTFCTriggerConfig(&tfc_trigger.TFCTriggerOptions{
		Action:                   tfc_trigger.PlanAction,
		Branch:                   testSuite.MetaData.SourceBranch,
		CommitSHA:                "abcd12233",
		ProjectNameWithNamespace: testSuite.MetaData.ProjectNameNS,
		MergeRequestIID:          testSuite.MetaData.MRIID,
		TriggerSource:            tfc_trigger.CommentTrigger,
	})
	trigger := tfc_trigger.NewTFCTrigger(testSuite.MockGitClient, testSuite.MockApiClient, testSuite.MockStreamClient, tCfg)
	triggeredWS, err := trigger.TriggerTFCEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(triggeredWS.Errored) > 0 {
		t.Fatal("unexpected failed workspaces", triggeredWS.Errored)
	}
	if len(triggeredWS.Executed) != 1 {
		t.Fatal("expected a single TF workspace run")
	}
	if triggeredWS.Executed[0] != "service-tfbuddy" {
		t.Fatal("expected workspace", triggeredWS.Executed[0])
	}
}

func TestTFCEvents_SingleWorkspacePlanError(t *testing.T) {

	ws := &tfc_trigger.ProjectConfig{
		Workspaces: []*tfc_trigger.TFCWorkspace{{
			Name:         "service-tfbuddy",
			Organization: "zapier-test",
			Mode:         "apply-before-merge",
		}}}

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{ProjectConfig: ws}, t)

	testSuite.MockGitClient.EXPECT().CreateMergeRequestDiscussion(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS, "Starting TFC plan for Workspace: `zapier-test/service-tfbuddy`.").Return(testSuite.MockGitDisc, nil)

	testSuite.MockApiClient.EXPECT().CreateRunFromSource(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("could not create run from source"))

	testSuite.InitTestSuite()

	testLogger := zltest.New(t)
	log.Logger = log.Logger.Output(testLogger)

	tCfg, _ := tfc_trigger.NewTFCTriggerConfig(&tfc_trigger.TFCTriggerOptions{
		Action:                   tfc_trigger.PlanAction,
		Branch:                   "test-branch",
		CommitSHA:                "abcd12233",
		ProjectNameWithNamespace: testSuite.MetaData.ProjectNameNS,
		MergeRequestIID:          testSuite.MetaData.MRIID,
		TriggerSource:            tfc_trigger.CommentTrigger,
	})
	trigger := tfc_trigger.NewTFCTrigger(testSuite.MockGitClient, testSuite.MockApiClient, testSuite.MockStreamClient, tCfg)
	triggeredWS, err := trigger.TriggerTFCEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lastLogEntry := testLogger.LastEntry()
	if lastLogEntry == nil {
		t.Fatal("expected log entry")
	}
	lastLogEntry.ExpMsg("could not trigger Run for Workspace")

	if len(triggeredWS.Errored) == 0 {
		t.Fatal("unexpected no failed workspaces")
	}
	if len(triggeredWS.Executed) != 0 {
		t.Fatal("expected no successful triggers")
	}
	if triggeredWS.Errored[0].Name != "service-tfbuddy" {
		t.Fatal("expected workspace", triggeredWS.Errored[0].Name)
	}
	if triggeredWS.Errored[0].Error != "could not trigger Run for Workspace. could not create TFC run. could not create run from source" {
		t.Fatal("expected error", triggeredWS.Errored[0].Error)
	}
}
func TestTFCEvents_SingleWorkspaceApply(t *testing.T) {
	ws := &tfc_trigger.ProjectConfig{
		Workspaces: []*tfc_trigger.TFCWorkspace{{
			Name:         "service-tfbuddy",
			Organization: "zapier-test",
			Mode:         "apply-before-merge",
		}}}

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{ProjectConfig: ws}, t)
	testSuite.MockGitRepo.EXPECT().GetModifiedFileNamesBetweenCommits(testSuite.MetaData.CommonSHA, "main").Return([]string{}, nil)
	testSuite.MockApiClient.EXPECT().CreateRunFromSource(gomock.Any(), gomock.Any()).Return(&tfe.Run{
		ID: "101",
		Workspace: &tfe.Workspace{Name: "service-tfbuddy",
			Organization: &tfe.Organization{Name: "zapier-test"},
		},
		ConfigurationVersion: &tfe.ConfigurationVersion{Speculative: false}}, nil)

	testSuite.MockStreamClient.EXPECT().AddRunMeta(gomock.Any())

	testSuite.InitTestSuite()
	testLogger := zltest.New(t)
	log.Logger = log.Logger.Output(testLogger)

	tCfg, _ := tfc_trigger.NewTFCTriggerConfig(&tfc_trigger.TFCTriggerOptions{
		Action:                   tfc_trigger.ApplyAction,
		Branch:                   "test-branch",
		CommitSHA:                "abcd12233",
		ProjectNameWithNamespace: testSuite.MetaData.ProjectNameNS,
		MergeRequestIID:          testSuite.MetaData.MRIID,
		TriggerSource:            tfc_trigger.CommentTrigger,
	})
	trigger := tfc_trigger.NewTFCTrigger(testSuite.MockGitClient, testSuite.MockApiClient, testSuite.MockStreamClient, tCfg)
	ctx, _ := otel.Tracer("FAKE").Start(context.Background(), "TEST")
	triggeredWS, err := trigger.TriggerTFCEvents(ctx)
	if err != nil {
		t.Fatal(err)
		return
	}
	lastEntry := testLogger.LastEntry()
	if lastEntry == nil {
		t.Fatal("expected log message not nil")
		return
	}
	lastEntry.ExpMsg("created TFC run")

	if len(triggeredWS.Errored) != 0 {
		t.Fatal("expected no failed workspaces")
	}
	if len(triggeredWS.Executed) == 0 {
		t.Fatal("expected successful triggers")
	}
	if triggeredWS.Executed[0] != "service-tfbuddy" {
		t.Fatal("expected workspace", triggeredWS.Errored[0].Name)
	}

}

func TestTFCEvents_MultiWorkspaceApply(t *testing.T) {

	ws := &tfc_trigger.ProjectConfig{
		Workspaces: []*tfc_trigger.TFCWorkspace{{
			Name:         "service-tfbuddy",
			Organization: "zapier-test",
			Mode:         "apply-before-merge",
		}, {
			Name:         "service-tfbuddy-staging",
			Organization: "zapier-test",
			Mode:         "apply-before-merge",
			Dir:          "staging/",
		}}}

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{ProjectConfig: ws}, t)
	testSuite.MockGitClient.EXPECT().GetMergeRequestModifiedFiles(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS).Return([]string{"main.tf", "staging/terraform.tf"}, nil)

	testSuite.MockGitClient.EXPECT().CreateMergeRequestDiscussion(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS, "Starting TFC apply for Workspace: `zapier-test/service-tfbuddy`.").Return(testSuite.MockGitDisc, nil)
	testSuite.MockGitClient.EXPECT().CreateMergeRequestDiscussion(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS, "Starting TFC apply for Workspace: `zapier-test/service-tfbuddy-staging`.").Return(testSuite.MockGitDisc, nil)

	testSuite.MockApiClient.EXPECT().GetWorkspaceByName(gomock.Any(), "zapier-test", gomock.Any()).DoAndReturn(func(a interface{}, c, d string) (*tfe.Workspace, error) {
		return &tfe.Workspace{ID: c}, nil
	}).AnyTimes()

	testSuite.MockApiClient.EXPECT().CreateRunFromSource(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, opts *tfc_api.ApiRunOptions) (*tfe.Run, error) {
		return &tfe.Run{
			ID: "101",
			Workspace: &tfe.Workspace{Name: opts.Workspace,
				Organization: &tfe.Organization{Name: "zapier-test"},
			},
			ConfigurationVersion: &tfe.ConfigurationVersion{Speculative: false}}, nil
	}).Times(2)

	testSuite.MockStreamClient.EXPECT().AddRunMeta(gomock.Any()).Times(2)
	testSuite.InitTestSuite()
	testLogger := zltest.New(t)
	log.Logger = log.Logger.Output(testLogger)

	tCfg, _ := tfc_trigger.NewTFCTriggerConfig(&tfc_trigger.TFCTriggerOptions{
		Action:                   tfc_trigger.ApplyAction,
		Branch:                   testSuite.MetaData.SourceBranch,
		CommitSHA:                "abcd12233",
		ProjectNameWithNamespace: testSuite.MetaData.ProjectNameNS,
		MergeRequestIID:          testSuite.MetaData.MRIID,
		TriggerSource:            tfc_trigger.CommentTrigger,
	})
	trigger := tfc_trigger.NewTFCTrigger(testSuite.MockGitClient, testSuite.MockApiClient, testSuite.MockStreamClient, tCfg)
	ctx, _ := otel.Tracer("FAKE").Start(context.Background(), "TEST")
	triggeredWS, err := trigger.TriggerTFCEvents(ctx)
	if err != nil {
		t.Fatal(err)
		return
	}
	lastEntry := testLogger.LastEntry()
	if lastEntry == nil {
		t.Fatal("expected log message not nil")
		return
	}
	lastEntry.ExpMsg("created TFC run")

	if len(triggeredWS.Errored) != 0 {
		t.Fatal("expected no failed workspaces", triggeredWS.Errored[0].Error)
	}
	if len(triggeredWS.Executed) != 2 {
		t.Fatal("expected successful triggers")
	}
	hits := 0
	for _, exec := range triggeredWS.Executed {
		if exec == "service-tfbuddy" || exec == "service-tfbuddy-staging" {
			hits++
		}
	}
	if hits != 2 {
		t.Fatal("expected workspaces service-tfbuddy & service-tfbuddy-staging", triggeredWS.Executed)
	}

}

func TestTFCEvents_SingleWorkspaceApplyError(t *testing.T) {
	ws := &tfc_trigger.ProjectConfig{
		Workspaces: []*tfc_trigger.TFCWorkspace{{
			Name:         "service-tfbuddy",
			Organization: "zapier-test",
			Mode:         "apply-before-merge",
		}}}

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{ProjectConfig: ws}, t)

	testSuite.MockGitClient.EXPECT().GetMergeRequestModifiedFiles(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS).Return([]string{"main.tf"}, nil)
	testSuite.MockGitClient.EXPECT().CloneMergeRequest(gomock.Any(), testSuite.MetaData.ProjectNameNS, gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("could not clone repo"))
	testSuite.MockGitClient.EXPECT().CreateMergeRequestComment(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS, "Error: could not clone repo: could not clone repo").MaxTimes(2)

	testSuite.InitTestSuite()

	testLogger := zltest.New(t)
	log.Logger = log.Logger.Output(testLogger)

	tCfg, _ := tfc_trigger.NewTFCTriggerConfig(&tfc_trigger.TFCTriggerOptions{
		Action:                   tfc_trigger.ApplyAction,
		Branch:                   "test-branch",
		CommitSHA:                "abcd12233",
		ProjectNameWithNamespace: testSuite.MetaData.ProjectNameNS,
		MergeRequestIID:          testSuite.MetaData.MRIID,
		TriggerSource:            tfc_trigger.CommentTrigger,
	})
	trigger := tfc_trigger.NewTFCTrigger(testSuite.MockGitClient, testSuite.MockApiClient, testSuite.MockStreamClient, tCfg)
	triggeredWS, err := trigger.TriggerTFCEvents(context.Background())
	if err == nil {
		t.Fatal("expected error to be returned")
		return
	}
	if err.Error() != "could not clone repo permanent error. cannot be retried" {
		t.Fatal("unexpected error returned", err)
	}
	lastEntry := testLogger.LastEntry()
	if lastEntry == nil {
		t.Fatal("expected log message not nil")
		return
	}
	lastEntry.ExpMsg("considering branch test-branch")

	if triggeredWS != nil {
		t.Fatal("expected no triggered workspaces")
	}
}
func TestTFCEvents_MultiWorkspaceApplyError(t *testing.T) {

	ws := &tfc_trigger.ProjectConfig{
		Workspaces: []*tfc_trigger.TFCWorkspace{{
			Name:         "service-tfbuddy",
			Organization: "zapier-test",
			Mode:         "apply-before-merge",
		}, {
			Name:         "service-tfbuddy-staging",
			Organization: "zapier-test",
			Mode:         "apply-before-merge",
			Dir:          "staging/",
		}}}

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{ProjectConfig: ws}, t)
	testSuite.MockGitClient.EXPECT().GetMergeRequestModifiedFiles(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS).Return([]string{"main.tf", "staging/terraform.tf"}, nil)

	testSuite.MockGitClient.EXPECT().CreateMergeRequestDiscussion(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS, "Starting TFC apply for Workspace: `zapier-test/service-tfbuddy`.").Return(testSuite.MockGitDisc, nil)
	testSuite.MockGitClient.EXPECT().CreateMergeRequestDiscussion(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS, "Starting TFC apply for Workspace: `zapier-test/service-tfbuddy-staging`.").Return(testSuite.MockGitDisc, nil)

	testSuite.MockApiClient.EXPECT().GetWorkspaceByName(gomock.Any(), "zapier-test", gomock.Any()).DoAndReturn(func(a interface{}, b, c string) (*tfe.Workspace, error) {
		return &tfe.Workspace{ID: c}, nil
	}).AnyTimes()

	testSuite.MockApiClient.EXPECT().CreateRunFromSource(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, opts *tfc_api.ApiRunOptions) (*tfe.Run, error) {
		if opts.Workspace == "service-tfbuddy" {
			return nil, fmt.Errorf("api error with terraform cloud")
		}
		return &tfe.Run{
			ID: "101",
			Workspace: &tfe.Workspace{Name: opts.Workspace,
				Organization: &tfe.Organization{Name: "zapier-test"},
			},
			ConfigurationVersion: &tfe.ConfigurationVersion{Speculative: false}}, nil
	}).Times(2)

	testSuite.InitTestSuite()

	tCfg, _ := tfc_trigger.NewTFCTriggerConfig(&tfc_trigger.TFCTriggerOptions{
		Action:                   tfc_trigger.ApplyAction,
		Branch:                   "test-branch",
		CommitSHA:                "abcd12233",
		ProjectNameWithNamespace: testSuite.MetaData.ProjectNameNS,
		MergeRequestIID:          testSuite.MetaData.MRIID,
		TriggerSource:            tfc_trigger.CommentTrigger,
	})
	trigger := tfc_trigger.NewTFCTrigger(testSuite.MockGitClient, testSuite.MockApiClient, testSuite.MockStreamClient, tCfg)
	ctx, _ := otel.Tracer("FAKE").Start(context.Background(), "TEST")
	triggeredWS, err := trigger.TriggerTFCEvents(ctx)
	if err != nil {
		t.Fatal(err)
		return
	}

	if len(triggeredWS.Errored) == 0 {
		t.Fatal("expected failed workspaces")
	}
	if len(triggeredWS.Executed) != 1 {
		t.Fatal("unexpected successful triggers", triggeredWS.Executed)
	}
	if triggeredWS.Executed[0] != "service-tfbuddy-staging" {
		t.Fatal("unexpected workspace", triggeredWS.Executed[0])
	}
	if triggeredWS.Errored[0].Name != "service-tfbuddy" {
		t.Fatal("unexpected workspace", triggeredWS.Errored[0].Name)
	}
	if triggeredWS.Errored[0].Error != "could not trigger Run for Workspace. could not create TFC run. api error with terraform cloud" {
		t.Fatal("unexpected error", triggeredWS.Errored[0].Error)
	}

}
func TestTFCEvents_WorkspaceApplyModifiedBothSrcDstBranches(t *testing.T) {

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	testSuite.MockGitRepo.EXPECT().GetModifiedFileNamesBetweenCommits(testSuite.MetaData.CommonSHA, "main").Return([]string{"terraform.tf"}, nil)
	testSuite.MockGitClient.EXPECT().GetMergeRequestModifiedFiles(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS).Return([]string{"main.tf"}, nil)

	mockStreamClient := mocks.NewMockStreamClient(mockCtrl)

	testSuite.InitTestSuite()

	testLogger := zltest.New(t)
	log.Logger = log.Logger.Output(testLogger)

	tCfg, _ := tfc_trigger.NewTFCTriggerConfig(&tfc_trigger.TFCTriggerOptions{
		Action:                   tfc_trigger.ApplyAction,
		Branch:                   testSuite.MetaData.SourceBranch,
		CommitSHA:                "abcd12233",
		ProjectNameWithNamespace: testSuite.MetaData.ProjectNameNS,
		MergeRequestIID:          testSuite.MetaData.MRIID,
		TriggerSource:            tfc_trigger.CommentTrigger,
	})
	trigger := tfc_trigger.NewTFCTrigger(testSuite.MockGitClient, testSuite.MockApiClient, mockStreamClient, tCfg)
	ctx, _ := otel.Tracer("FAKE").Start(context.Background(), "TEST")
	triggeredWS, err := trigger.TriggerTFCEvents(ctx)
	if err != nil {
		t.Fatal(err)
		return
	}
	lastEntry := testLogger.LastEntry()
	if lastEntry == nil {
		t.Fatal("expected log message not nil")
		return
	}
	lastEntry.ExpMsg("Ignoring workspace, because it is modified in the target branch.")

	if len(triggeredWS.Errored) == 0 {
		t.Fatal("expected  failed workspaces")
	}
	if len(triggeredWS.Executed) != 0 {
		t.Fatal("unexpected successful triggers")
	}
	if triggeredWS.Errored[0].Name != mocks.TF_WORKSPACE_NAME {
		t.Fatal("unexpected workspace", triggeredWS.Errored[0].Name)
	}
	if triggeredWS.Errored[0].Error != "Ignoring workspace, because it is modified in the target branch. Please rebase/merge target branch to resolve this." {
		t.Fatal("unexpected error", triggeredWS.Errored[0].Error)
	}
}

func TestTFCEvents_MultiWorkspaceApplyModifiedBothSrcDstBranches(t *testing.T) {

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{
		ProjectConfig: &tfc_trigger.ProjectConfig{
			Workspaces: []*tfc_trigger.TFCWorkspace{{
				Name:         "service-tfbuddy",
				Organization: "zapier-test",
				Mode:         "apply-before-merge",
				Dir:          "production",
			}}},
	}, t)
	testSuite.MockGitClient.EXPECT().GetMergeRequestModifiedFiles(gomock.Any(), testSuite.MetaData.MRIID, testSuite.MetaData.ProjectNameNS).Return([]string{"/production/main.tf"}, nil).AnyTimes()
	testSuite.MockGitRepo.EXPECT().GetModifiedFileNamesBetweenCommits(testSuite.MetaData.CommonSHA, testSuite.MetaData.TargetBranch).Return([]string{"/staging/terraform.tf"}, nil).AnyTimes()

	testSuite.MockApiClient.EXPECT().CreateRunFromSource(gomock.Any(), gomock.Any()).Return(&tfe.Run{
		ID: "101",
		Workspace: &tfe.Workspace{Name: "service-tfbuddy",
			Organization: &tfe.Organization{Name: "zapier-test"},
		},
		ConfigurationVersion: &tfe.ConfigurationVersion{Speculative: true}}, nil)

	mockRunPollingTask := mocks.NewMockRunPollingTask(mockCtrl)
	mockRunPollingTask.EXPECT().Schedule(gomock.Any())
	testSuite.MockStreamClient.EXPECT().NewTFRunPollingTask(gomock.Any(), time.Second*1).Return(mockRunPollingTask)

	testSuite.InitTestSuite()

	testLogger := zltest.New(t)
	log.Logger = log.Logger.Output(testLogger)

	tCfg, _ := tfc_trigger.NewTFCTriggerConfig(&tfc_trigger.TFCTriggerOptions{
		Action:                   tfc_trigger.ApplyAction,
		MergeRequestIID:          testSuite.MetaData.MRIID,
		ProjectNameWithNamespace: testSuite.MetaData.ProjectNameNS,
		Branch:                   testSuite.MetaData.SourceBranch,
		Workspace:                "",
		CommitSHA:                "abcd12233",
	})
	trigger := tfc_trigger.NewTFCTrigger(testSuite.MockGitClient, testSuite.MockApiClient, testSuite.MockStreamClient, tCfg)
	ctx, _ := otel.Tracer("FAKE").Start(context.Background(), "TEST")
	triggeredWS, err := trigger.TriggerTFCEvents(ctx)
	if err != nil {
		t.Fatal(err)
		return
	}
	lastEntry := testLogger.LastEntry()
	if lastEntry == nil {
		t.Fatal("expected log message not nil")
		return
	}
	lastEntry.ExpMsg("created TFC run")

	if len(triggeredWS.Errored) != 0 {
		t.Fatal("expected  no failed workspaces")
	}
	if len(triggeredWS.Executed) == 0 {
		t.Fatal("expected successful triggers")
	}
	if triggeredWS.Executed[0] != "service-tfbuddy" {
		t.Fatal("expected workspace", triggeredWS.Errored[0].Name)
	}

}
