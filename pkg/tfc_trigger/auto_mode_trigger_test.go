package tfc_trigger_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/go-tfe"
	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/utils"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.uber.org/mock/gomock"
)

// autoModeTargetConfig is the target branch's .tfbuddy.yaml in these tests.
const autoModeTargetConfig = `
workspaces:
  - name: service-tfbuddy
    organization: zapier-test
autoMode:
  authors:
    - username: tags-bot
      commitEmails: [tags-bot@example.com]
  classes:
    - name: tags
      authors: [tags-bot]
      paths: ["**/tags.auto.tfvars.json"]
      workspaces: ["service-*"]
`

const autoModeTagsFile = "services/api/prod/main-us-east-1/tags.auto.tfvars.json"

func autoModeAppConfig() config.Config {
	cfg := config.C
	cfg.AutoModeEnabled = true
	return cfg
}

// autoModeWorkspaces is the source branch's config: one workspace that every
// change triggers, opted into auto-merge.
func autoModeWorkspaces() *tfc_trigger.ProjectConfig {
	return &tfc_trigger.ProjectConfig{Workspaces: []*tfc_trigger.TFCWorkspace{{
		Name:         "service-tfbuddy",
		Organization: "zapier-test",
		Mode:         "apply-before-merge",
		AutoMerge:    true,
		TriggerDirs:  []string{"**"},
	}}}
}

// defaultBranch makes branch the default branch of every project gl reads.
func defaultBranch(ctrl *gomock.Controller, gl *mocks.MockGitClient, branch string) {
	settings := mocks.NewMockProjectSettings(ctrl)
	settings.EXPECT().GetDefaultBranch().Return(branch).AnyTimes()
	gl.EXPECT().GetProjectSettings(gomock.Any(), gomock.Any()).Return(settings, nil).AnyTimes()
}

// expectMR makes the suite's MR one by author that changes files in commits,
// with targetConfig as the target branch's .tfbuddy.yaml and the target branch
// as the project's default branch. It must run before InitTestSuite, whose
// catch-all expectations would otherwise match first.
func expectMR(ctrl *gomock.Controller, ts *mocks.TestSuite, author, targetConfig string, files []string, commits []vcs.MRCommit) {
	a := mocks.NewMockMRAuthor(ctrl)
	a.EXPECT().GetUsername().Return(author).AnyTimes()
	ts.MockGitMR.EXPECT().GetAuthor().Return(a).AnyTimes()
	ts.MockGitMR.EXPECT().GetHeadSHA().Return("abcd12233").AnyTimes()
	defaultBranch(ctrl, ts.MockGitClient, ts.MetaData.TargetBranch)
	// The MR has not fallen back to Manual Mode, and a fallback finds it
	// already marked, unless a test registered other answers first.
	ts.MockStreamClient.EXPECT().IsAutoModeManual(gomock.Any()).Return(false, "", nil).AnyTimes()
	ts.MockStreamClient.EXPECT().MarkAutoModeManual(gomock.Any(), gomock.Any()).Return(false, nil).AnyTimes()
	// An Auto Mode run checks out the commit it is for and diverges from it;
	// a Manual Mode run never checks out a commit.
	ts.MockGitRepo.EXPECT().CheckoutCommit("abcd12233").Return(nil).AnyTimes()
	ts.MockGitRepo.EXPECT().GetMergeBase("abcd12233", ts.MetaData.TargetBranch).Return(ts.MetaData.CommonSHA, nil).AnyTimes()
	ts.MockStreamClient.EXPECT().BeginClassPlans(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	ts.MockStreamClient.EXPECT().MarkClassPlansReported(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	ts.MockStreamClient.EXPECT().RegisterClassPlanRun(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	ts.MockStreamClient.EXPECT().GetClassPlans(gomock.Any()).Return(nil, runstream.ErrClassPlansNotFound).AnyTimes()
	ts.MockStreamClient.EXPECT().RecordClassPlanResult(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, false, nil).AnyTimes()
	ts.MockGitClient.EXPECT().GetRepoFile(gomock.Any(), ts.MetaData.ProjectNameNS, ".tfbuddy.yaml", ts.MetaData.TargetBranch).
		Return([]byte(targetConfig), nil).AnyTimes()
	ts.MockGitClient.EXPECT().GetMergeRequestModifiedFiles(gomock.Any(), ts.MetaData.MRIID, ts.MetaData.ProjectNameNS).
		Return(files, nil).AnyTimes()
	if commits != nil {
		ts.MockGitClient.EXPECT().GetMergeRequestCommits(gomock.Any(), ts.MetaData.MRIID, ts.MetaData.ProjectNameNS).
			Return(commits, nil).AnyTimes()
	}
}

var tagsBotCommits = []vcs.MRCommit{{SHA: "c1", AuthorEmail: "tags-bot@example.com", CommitterEmail: "tags-bot@example.com"}}

// expectManualModeClass expects TFC/class to be skipped on the head commit,
// with a description that contains reason.
func expectManualModeClass(t *testing.T, ts *mocks.TestSuite, reason string) {
	t.Helper()
	ts.MockGitClient.EXPECT().SetClassStatus(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, s vcs.ClassStatus) error {
		if s.State != vcs.CommitStateSkipped || s.CommitSHA != "abcd12233" || !strings.HasPrefix(s.Description, "Manual Mode: ") ||
			!strings.Contains(s.Description, reason) {
			t.Errorf("TFC/class = %+v, want skipped for Manual Mode because %s", s, reason)
		}
		return nil
	})
}

// capturePlanRun expects one speculative plan run and returns where its
// options and metadata land.
func capturePlanRun(ctrl *gomock.Controller, ts *mocks.TestSuite) (*tfc_api.ApiRunOptions, *runstream.TFRunMetadata) {
	opts := &tfc_api.ApiRunOptions{}
	rmd := &runstream.TFRunMetadata{}
	ts.MockGitClient.EXPECT().CreateMergeRequestDiscussion(gomock.Any(), ts.MetaData.MRIID, ts.MetaData.ProjectNameNS,
		"Starting TFC plan for Workspace: `zapier-test/service-tfbuddy`.\n<!-- tfbuddy:ws=service-tfbuddy:action=plan -->").
		Return(ts.MockGitDisc, nil)
	ts.MockApiClient.EXPECT().CreateRunFromSource(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, o *tfc_api.ApiRunOptions) (*tfe.Run, error) {
			*opts = *o
			return &tfe.Run{
				ID:                   "run-1",
				Workspace:            &tfe.Workspace{Name: "service-tfbuddy", Organization: &tfe.Organization{Name: "zapier-test"}},
				ConfigurationVersion: &tfe.ConfigurationVersion{Speculative: true},
			}, nil
		})
	ts.MockStreamClient.EXPECT().AddRunMeta(gomock.Any()).DoAndReturn(func(r runstream.RunMetadata) error {
		*rmd = *r.(*runstream.TFRunMetadata)
		return nil
	})
	task := mocks.NewMockRunPollingTask(ctrl)
	task.EXPECT().Schedule(gomock.Any())
	ts.MockStreamClient.EXPECT().NewTFRunPollingTask(gomock.Any(), time.Second).Return(task)
	return opts, rmd
}

func newAutoModeTrigger(appCfg config.Config, ts *mocks.TestSuite, action tfc_trigger.TriggerAction) tfc_trigger.Trigger {
	tCfg, _ := tfc_trigger.NewTFCTriggerConfig(&tfc_trigger.TFCTriggerOptions{
		Action:                   action,
		Branch:                   ts.MetaData.SourceBranch,
		CommitSHA:                "abcd12233",
		ProjectNameWithNamespace: ts.MetaData.ProjectNameNS,
		MergeRequestIID:          ts.MetaData.MRIID,
		TriggerSource:            tfc_trigger.MergeRequestEventTrigger,
		VcsProvider:              "gitlab",
		DeliveryID:               "delivery-1",
	})
	return tfc_trigger.NewTFCTrigger(appCfg, ts.MockGitClient, ts.MockApiClient, ts.MockStreamClient, tCfg)
}

func TestLoadAutoModeConfig_ReadsDefaultBranchOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	gl := mocks.NewMockGitClient(ctrl)
	defaultBranch(ctrl, gl, "main")
	// Only the default branch is expected; a read of any other branch fails.
	gl.EXPECT().GetRepoFile(gomock.Any(), "zapier/tfbuddy", ".tfbuddy.yaml", "main").Return([]byte(autoModeTargetConfig), nil)

	cfg, err := tfc_trigger.LoadAutoModeConfig(context.Background(), gl, "zapier/tfbuddy", "main")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Author("tags-bot") == nil {
		t.Fatal("expected tags-bot to be an Auto Mode author")
	}
}

// An MR into any branch but the default one has no Auto Mode, whatever that
// branch's .tfbuddy.yaml says: TFBuddy does not even read it.
func TestLoadAutoModeConfig_NonDefaultTargetIsNotAutoMode(t *testing.T) {
	ctrl := gomock.NewController(t)
	gl := mocks.NewMockGitClient(ctrl)
	defaultBranch(ctrl, gl, "main")

	cfg, err := tfc_trigger.LoadAutoModeConfig(context.Background(), gl, "zapier/tfbuddy", "release-1")
	if err != nil || cfg != nil {
		t.Fatalf("an MR into a non-default branch has no Auto Mode config, got %+v, %v", cfg, err)
	}
}

func TestLoadAutoModeConfig_UnreadableDefaultBranchFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	gl := mocks.NewMockGitClient(ctrl)
	gl.EXPECT().GetProjectSettings(gomock.Any(), "zapier/tfbuddy").Return(nil, errors.New("502 Bad Gateway"))

	if _, err := tfc_trigger.LoadAutoModeConfig(context.Background(), gl, "zapier/tfbuddy", "main"); err == nil {
		t.Fatal("a default branch GitLab did not answer must not decide that the MR is not in Auto Mode")
	}
}

func TestLoadAutoModeConfig_MissingFileIsNotAutoMode(t *testing.T) {
	ctrl := gomock.NewController(t)
	gl := mocks.NewMockGitClient(ctrl)
	defaultBranch(ctrl, gl, "main")
	gl.EXPECT().GetRepoFile(gomock.Any(), "zapier/tfbuddy", ".tfbuddy.yaml", "main").
		Return(nil, utils.CreatePermanentError(errors.New("404 File Not Found")))

	cfg, err := tfc_trigger.LoadAutoModeConfig(context.Background(), gl, "zapier/tfbuddy", "main")
	if err != nil || cfg != nil {
		t.Fatalf("a branch without the file has no Auto Mode config, got %+v, %v", cfg, err)
	}
}

func TestLoadAutoModeConfig_TransientErrorFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	gl := mocks.NewMockGitClient(ctrl)
	defaultBranch(ctrl, gl, "main")
	gl.EXPECT().GetRepoFile(gomock.Any(), "zapier/tfbuddy", ".tfbuddy.yaml", "main").Return(nil, errors.New("502 Bad Gateway"))

	if _, err := tfc_trigger.LoadAutoModeConfig(context.Background(), gl, "zapier/tfbuddy", "main"); err == nil {
		t.Fatal("a read GitLab did not answer must not decide that the MR is not in Auto Mode")
	}
}

func TestTFCEvents_AutoModePlanCarriesClass(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: autoModeWorkspaces()}, t)
	ref := runstream.ClassPlanRef{VcsProvider: "gitlab", Project: ts.MetaData.ProjectNameNS, MergeRequest: 101, CommitSHA: "abcd12233"}
	// The class plans begin before any run, and each run is registered.
	gomock.InOrder(
		ts.MockStreamClient.EXPECT().BeginClassPlans(ref, "tags", []string{"service-tfbuddy"}, "delivery:delivery-1").Return(true, nil),
		ts.MockStreamClient.EXPECT().RegisterClassPlanRun(ref, "service-tfbuddy", "run-1", "delivery:delivery-1").Return(nil),
	)
	// Every workspace planned, so none is recorded as failed.
	ts.MockStreamClient.EXPECT().RecordClassPlanResult(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
	expectMR(ctrl, ts, "tags-bot", autoModeTargetConfig, []string{autoModeTagsFile}, tagsBotCommits)
	opts, rmd := capturePlanRun(ctrl, ts)
	var classStatuses []vcs.ClassStatus
	ts.MockGitClient.EXPECT().SetClassStatus(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s vcs.ClassStatus) error {
			classStatuses = append(classStatuses, s)
			return nil
		}).AnyTimes()
	ts.InitTestSuite()

	triggered, err := newAutoModeTrigger(autoModeAppConfig(), ts, tfc_trigger.PlanAction).TriggerTFCEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(triggered.Errored) != 0 {
		t.Fatalf("unexpected failed workspaces: %v", triggered.Errored)
	}
	if opts.Message != "MR [!101] [tfbuddy-class=tags] sha=abcd12233 author=tags-bot" {
		t.Errorf("run message = %q, want the tags class marker and no title", opts.Message)
	}
	if rmd.ChangeClass != "tags" || !slices.Equal(rmd.ClassWorkspaces, []string{"service-tfbuddy"}) {
		t.Errorf("run metadata class = %q over %v, want tags over [service-tfbuddy]", rmd.ChangeClass, rmd.ClassWorkspaces)
	}
	if len(classStatuses) != 1 || classStatuses[0].State != vcs.CommitStatePending || classStatuses[0].CommitSHA != "abcd12233" {
		t.Errorf("expected one pending TFC/class status on the head commit, got %+v", classStatuses)
	}
}

func TestTFCEvents_AutoModeAuthorWithNoClassFallsBackToManualMode(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: autoModeWorkspaces()}, t)
	// tags-bot is listed, but main.tf is outside every class it is on.
	expectMR(ctrl, ts, "tags-bot", autoModeTargetConfig, []string{"services/api/prod/main-us-east-1/main.tf"}, tagsBotCommits)
	opts, rmd := capturePlanRun(ctrl, ts)
	expectManualModeClass(t, ts, "fit none of tags-bot's Auto Mode classes")
	ts.InitTestSuite()

	if _, err := newAutoModeTrigger(autoModeAppConfig(), ts, tfc_trigger.PlanAction).TriggerTFCEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if opts.Message != "MR [!101]: MR Title" {
		t.Errorf("run message = %q, want the Manual Mode message with the title and no class", opts.Message)
	}
	if rmd.ChangeClass != "" {
		t.Errorf("a Manual Mode run has no class, got %q", rmd.ChangeClass)
	}
}

func TestTFCEvents_PersonTitleIsKept(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: autoModeWorkspaces()}, t)
	ts.MockGitMR.EXPECT().GetTitle().Return("Add bucket [tfbuddy-class=tags]").AnyTimes()
	// A person is not on the list, so their commits are never read.
	expectMR(ctrl, ts, "a-person", autoModeTargetConfig, []string{autoModeTagsFile}, nil)
	opts, rmd := capturePlanRun(ctrl, ts)
	ts.InitTestSuite()

	if _, err := newAutoModeTrigger(autoModeAppConfig(), ts, tfc_trigger.PlanAction).TriggerTFCEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A marker in a person's own title can only restrict their own run.
	if opts.Message != "MR [!101]: Add bucket [tfbuddy-class=tags]" {
		t.Errorf("run message = %q, want the title unchanged", opts.Message)
	}
	if rmd.ChangeClass != "" {
		t.Errorf("a person's run has no class, got %q", rmd.ChangeClass)
	}
}

// An Auto Mode author's MR into a branch other than the default one is a
// Manual Mode MR.
func TestTFCEvents_AutoModeNeedsTheDefaultBranch(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: autoModeWorkspaces()}, t)
	a := mocks.NewMockMRAuthor(ctrl)
	a.EXPECT().GetUsername().Return("tags-bot").AnyTimes()
	ts.MockGitMR.EXPECT().GetAuthor().Return(a).AnyTimes()
	// The suite's MR targets main; the default branch is another one. Its
	// config is read only to find a fallback recorded before a retarget.
	defaultBranch(ctrl, ts.MockGitClient, "trunk")
	ts.MockGitClient.EXPECT().GetRepoFile(gomock.Any(), ts.MetaData.ProjectNameNS, ".tfbuddy.yaml", "trunk").
		Return([]byte(autoModeTargetConfig), nil).AnyTimes()
	ts.MockStreamClient.EXPECT().IsAutoModeManual(gomock.Any()).Return(false, "", nil).AnyTimes()
	ts.MockGitClient.EXPECT().GetMergeRequestModifiedFiles(gomock.Any(), ts.MetaData.MRIID, ts.MetaData.ProjectNameNS).
		Return([]string{autoModeTagsFile}, nil).AnyTimes()
	opts, rmd := capturePlanRun(ctrl, ts)
	ts.InitTestSuite()

	if _, err := newAutoModeTrigger(autoModeAppConfig(), ts, tfc_trigger.PlanAction).TriggerTFCEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if opts.Message != "MR [!101]: MR Title" || rmd.ChangeClass != "" {
		t.Errorf("run message = %q, class = %q, want a Manual Mode run", opts.Message, rmd.ChangeClass)
	}
}

// The source branch is the MR's own: an Auto Mode section there must not put its
// author in Auto Mode.
func TestTFCEvents_AutoModeConfigOnSourceBranchIsIgnored(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: autoModeWorkspaces()}, t)
	sourceConfig := `
workspaces:
  - name: service-tfbuddy
    organization: zapier-test
    triggerDirs: ["**"]
autoMode:
  authors: [{username: a-person, commitEmails: [person@example.com]}]
  classes: [{name: tags, authors: [a-person], paths: ["**"]}]
`
	ts.MockGitClient.EXPECT().GetRepoFile(gomock.Any(), ts.MetaData.ProjectNameNS, ".tfbuddy.yaml", ts.MetaData.SourceBranch).
		Return([]byte(sourceConfig), nil).AnyTimes()
	expectMR(ctrl, ts, "a-person", "workspaces:\n  - name: service-tfbuddy\n", []string{autoModeTagsFile}, nil)
	opts, _ := capturePlanRun(ctrl, ts)
	ts.InitTestSuite()

	if _, err := newAutoModeTrigger(autoModeAppConfig(), ts, tfc_trigger.PlanAction).TriggerTFCEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(opts.Message, "tfbuddy-class") {
		t.Errorf("run message = %q, want no class from the source branch's config", opts.Message)
	}
}

func TestTFCEvents_AutoModeDisabledIsMode1(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: autoModeWorkspaces()}, t)
	// No GetAuthor or target-branch expectations: with the flag off, TFBuddy
	// must not even look.
	ts.MockGitClient.EXPECT().GetMergeRequestModifiedFiles(gomock.Any(), ts.MetaData.MRIID, ts.MetaData.ProjectNameNS).
		Return([]string{autoModeTagsFile}, nil).AnyTimes()
	opts, _ := capturePlanRun(ctrl, ts)
	ts.InitTestSuite()

	if _, err := newAutoModeTrigger(config.C, ts, tfc_trigger.PlanAction).TriggerTFCEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if opts.Message != "MR [!101]: MR Title" {
		t.Errorf("run message = %q, want today's message", opts.Message)
	}
}

func TestTFCEvents_AutoModeDivergenceCheckFailsClosed(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: autoModeWorkspaces()}, t)
	expectMR(ctrl, ts, "tags-bot", autoModeTargetConfig, []string{autoModeTagsFile}, tagsBotCommits)
	ts.MockGitRepo.EXPECT().GetModifiedFileNamesBetweenCommits(ts.MetaData.CommonSHA, ts.MetaData.TargetBranch).
		Return(nil, errors.New("could not diff"))
	var statuses []vcs.WorkspaceStatus
	ts.MockGitClient.EXPECT().SetWorkspaceStatus(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s vcs.WorkspaceStatus) error {
			statuses = append(statuses, s)
			return nil
		}).AnyTimes()
	// No CreateRunFromSource or AddTags expectation: nothing may run or lock.
	ts.InitTestSuite()

	_, err := newAutoModeTrigger(autoModeAppConfig(), ts, tfc_trigger.ApplyAction).TriggerTFCEvents(context.Background())
	if !errors.Is(err, utils.ErrPermanent) {
		t.Fatalf("expected a permanent error, got %v", err)
	}
	if len(statuses) != 1 || statuses[0].State != vcs.CommitStateFailed || !strings.Contains(statuses[0].Description, "could not check the target branch") {
		t.Errorf("expected the workspace to be blocked, got %+v", statuses)
	}
}

func TestTFCEvents_AutoModeApplyRequiresAutoMerge(t *testing.T) {
	ctrl := gomock.NewController(t)
	workspaces := autoModeWorkspaces()
	workspaces.Workspaces[0].AutoMerge = false
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: workspaces}, t)
	expectMR(ctrl, ts, "tags-bot", autoModeTargetConfig, []string{autoModeTagsFile}, tagsBotCommits)
	ts.InitTestSuite()

	_, err := newAutoModeTrigger(autoModeAppConfig(), ts, tfc_trigger.ApplyAction).TriggerTFCEvents(context.Background())
	if err == nil || !strings.Contains(err.Error(), "auto-merge") {
		t.Fatalf("an Auto Mode apply that cannot auto-merge must be refused, got %v", err)
	}
}

// An apply an approval started runs in Auto Mode or not at all: when the MR no
// longer fits a class by the time the apply decides, nothing runs.
func TestTFCEvents_AutoModeApprovalRefusesManualModeMR(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: autoModeWorkspaces()}, t)
	expectMR(ctrl, ts, "tags-bot", autoModeTargetConfig, []string{"services/api/prod/main-us-east-1/main.tf"}, tagsBotCommits)
	// No CreateRunFromSource or AddTags expectation: nothing may run or lock.
	expectManualModeClass(t, ts, "fit none of tags-bot's Auto Mode classes")
	ts.InitTestSuite()

	tCfg, _ := tfc_trigger.NewTFCTriggerConfig(&tfc_trigger.TFCTriggerOptions{
		Action:                   tfc_trigger.ApplyAction,
		Branch:                   ts.MetaData.SourceBranch,
		CommitSHA:                "abcd12233",
		ProjectNameWithNamespace: ts.MetaData.ProjectNameNS,
		MergeRequestIID:          ts.MetaData.MRIID,
		TriggerSource:            tfc_trigger.MergeRequestEventTrigger,
		VcsProvider:              "gitlab",
		DeliveryID:               "delivery-1",
		AutoModeApproval:         true,
	})
	trigger := tfc_trigger.NewTFCTrigger(autoModeAppConfig(), ts.MockGitClient, ts.MockApiClient, ts.MockStreamClient, tCfg)
	_, err := trigger.TriggerTFCEvents(context.Background())
	if !errors.Is(err, utils.ErrPermanent) || !strings.Contains(err.Error(), "does not run in Auto Mode") {
		t.Fatalf("expected the approval's apply to be refused, got %v", err)
	}
}

func TestAutoModeClassForMR(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: autoModeWorkspaces()}, t)
	expectMR(ctrl, ts, "tags-bot", autoModeTargetConfig, []string{autoModeTagsFile}, tagsBotCommits)
	ts.InitTestSuite()

	class, _, err := tfc_trigger.AutoModeClassForMR(context.Background(), autoModeAppConfig(), ts.MockGitClient, ts.MockStreamClient, nil, ts.MetaData.ProjectNameNS, ts.MockGitMR)
	if err != nil || class != "tags" {
		t.Fatalf("AutoModeClassForMR() = %q, %v, want tags", class, err)
	}
	class, _, err = tfc_trigger.AutoModeClassForMR(context.Background(), config.C, ts.MockGitClient, ts.MockStreamClient, nil, ts.MetaData.ProjectNameNS, ts.MockGitMR)
	if err != nil || class != tfc_trigger.ClassNone {
		t.Fatalf("with Auto Mode off, AutoModeClassForMR() = %q, %v, want no class", class, err)
	}
}

// An MR whose files fit a class, but which affects a workspace the class does
// not list, is a Manual Mode MR.
func TestTFCEvents_AutoModeWorkspaceOutsideClassFallsBackToManualMode(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: autoModeWorkspaces()}, t)
	narrow := strings.Replace(autoModeTargetConfig, `workspaces: ["service-*"]`, `workspaces: ["services-*"]`, 1)
	expectMR(ctrl, ts, "tags-bot", narrow, []string{autoModeTagsFile}, tagsBotCommits)
	opts, rmd := capturePlanRun(ctrl, ts)
	expectManualModeClass(t, ts, "fit none of tags-bot's Auto Mode classes")
	ts.InitTestSuite()

	if _, err := newAutoModeTrigger(autoModeAppConfig(), ts, tfc_trigger.PlanAction).TriggerTFCEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if opts.Message != "MR [!101]: MR Title" || rmd.ChangeClass != "" {
		t.Errorf("run message = %q, class = %q, want a Manual Mode run", opts.Message, rmd.ChangeClass)
	}
}

// Once an MR has fallen back to Manual Mode, it stays there: a plan that would
// fit a class carries none.
func TestTFCEvents_ManualModeIsSticky(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: autoModeWorkspaces()}, t)
	ts.MockStreamClient.EXPECT().IsAutoModeManual(runstream.AutoModeMRRef{VcsProvider: "gitlab", Project: ts.MetaData.ProjectNameNS, MergeRequest: 101}).
		Return(true, "a plan failed", nil)
	ts.MockStreamClient.EXPECT().MarkAutoModeManual(gomock.Any(), gomock.Any()).Times(0)
	expectMR(ctrl, ts, "tags-bot", autoModeTargetConfig, []string{autoModeTagsFile}, tagsBotCommits)
	opts, rmd := capturePlanRun(ctrl, ts)
	// A later commit of a fallen-back MR is skipped too, with the first reason.
	expectManualModeClass(t, ts, "a plan failed")
	ts.InitTestSuite()

	if _, err := newAutoModeTrigger(autoModeAppConfig(), ts, tfc_trigger.PlanAction).TriggerTFCEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if opts.Message != "MR [!101]: MR Title" || rmd.ChangeClass != "" {
		t.Errorf("run message = %q, class = %q, want a Manual Mode run", opts.Message, rmd.ChangeClass)
	}
}

// An Auto Mode author's MR that fits no class, here because of a commit by
// someone else, is marked as Manual Mode, so a force-push that drops the
// commit does not bring Auto Mode back. The first fallback says why.
func TestTFCEvents_FallbackMarksTheMRManual(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: autoModeWorkspaces()}, t)
	ts.MockStreamClient.EXPECT().MarkAutoModeManual(runstream.AutoModeMRRef{VcsProvider: "gitlab", Project: ts.MetaData.ProjectNameNS, MergeRequest: 101}, gomock.Any()).
		DoAndReturn(func(_ runstream.AutoModeMRRef, reason string) (bool, error) {
			if !strings.Contains(reason, "commit c2 was not authored and committed by tags-bot") {
				t.Errorf("unexpected reason %q", reason)
			}
			return true, nil
		})
	ts.MockGitClient.EXPECT().CreateMergeRequestComment(gomock.Any(), 101, ts.MetaData.ProjectNameNS, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ int, _, msg string) error {
			if !strings.Contains(msg, "now runs in Manual Mode because commit c2") || strings.Contains(msg, "planning it again") {
				t.Errorf("unexpected comment %q", msg)
			}
			return nil
		})
	commits := append(slices.Clone(tagsBotCommits), vcs.MRCommit{SHA: "c2", AuthorEmail: "someone@example.com", CommitterEmail: "someone@example.com"})
	expectMR(ctrl, ts, "tags-bot", autoModeTargetConfig, []string{autoModeTagsFile}, commits)
	opts, _ := capturePlanRun(ctrl, ts)
	// This plan already has no class, so nothing is planned again; TFC/class
	// says why, without blocking the merge.
	expectManualModeClass(t, ts, "commit c2 was not authored")
	ts.InitTestSuite()

	if _, err := newAutoModeTrigger(autoModeAppConfig(), ts, tfc_trigger.PlanAction).TriggerTFCEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if opts.Message != "MR [!101]: MR Title" {
		t.Errorf("run message = %q, want a Manual Mode run", opts.Message)
	}
}

// fakeReplanner records the Manual Mode re-plans it is asked for.
type fakeReplanner struct {
	mu       sync.Mutex
	requests []string
	err      error
}

func (f *fakeReplanner) RequestManualModeReplan(_ context.Context, project string, mrIID int, commitSHA string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, fmt.Sprintf("%s!%d@%s", project, mrIID, commitSHA))
	return f.err
}

// A class plan that fails puts the MR in Manual Mode, fails TFC/class, and
// asks for a plan of the head commit without the class. The re-plan is only
// requested here; it never runs inside this trigger.
func TestTFCEvents_ClassFailureAsksForAReplan(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: autoModeWorkspaces()}, t)
	ref := runstream.AutoModeMRRef{VcsProvider: "gitlab", Project: ts.MetaData.ProjectNameNS, MergeRequest: 101}
	ts.MockStreamClient.EXPECT().MarkAutoModeManual(ref, "service-tfbuddy did not plan").Return(true, nil)
	// The workspace that did not plan fails its class plan.
	ts.MockStreamClient.EXPECT().RecordClassPlanResult(
		runstream.ClassPlanRef{VcsProvider: "gitlab", Project: ts.MetaData.ProjectNameNS, MergeRequest: 101, CommitSHA: "abcd12233"},
		"service-tfbuddy", "", false, gomock.Any()).
		DoAndReturn(func(_ runstream.ClassPlanRef, _, _ string, _ bool, detail string) (*runstream.ClassPlanState, bool, error) {
			if !strings.Contains(detail, "did not plan") || !strings.Contains(detail, "TFC is down") {
				t.Errorf("unexpected detail %q", detail)
			}
			return nil, false, nil
		})
	expectMR(ctrl, ts, "tags-bot", autoModeTargetConfig, []string{autoModeTagsFile}, tagsBotCommits)
	ts.MockGitClient.EXPECT().CreateMergeRequestDiscussion(gomock.Any(), ts.MetaData.MRIID, ts.MetaData.ProjectNameNS, gomock.Any()).
		Return(ts.MockGitDisc, nil)
	ts.MockApiClient.EXPECT().CreateRunFromSource(gomock.Any(), gomock.Any()).Return(nil, errors.New("TFC is down"))
	var classStatuses []vcs.ClassStatus
	ts.MockGitClient.EXPECT().SetClassStatus(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s vcs.ClassStatus) error {
			classStatuses = append(classStatuses, s)
			return nil
		}).AnyTimes()
	var comments []string
	ts.MockGitClient.EXPECT().CreateMergeRequestComment(gomock.Any(), 101, ts.MetaData.ProjectNameNS, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ int, _, msg string) error {
			comments = append(comments, msg)
			return nil
		}).AnyTimes()
	ts.InitTestSuite()

	replanner := &fakeReplanner{}
	trigger := newAutoModeTrigger(autoModeAppConfig(), ts, tfc_trigger.PlanAction)
	trigger.SetManualModeReplanner(replanner)
	triggered, err := trigger.TriggerTFCEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(triggered.Errored) != 1 {
		t.Fatalf("expected the Auto Mode plan to fail, got %+v", triggered)
	}
	if !slices.Equal(replanner.requests, []string{"zapier/tfbuddy!101@abcd12233"}) {
		t.Errorf("re-plan requests = %q, want one for the head commit", replanner.requests)
	}
	last := classStatuses[len(classStatuses)-1]
	if last.State != vcs.CommitStateSkipped || last.Description != "Manual Mode: service-tfbuddy did not plan" {
		t.Errorf("TFC/class = %+v, want skipped with the Manual Mode reason", last)
	}
	if len(comments) != 1 || !strings.Contains(comments[0], "planning it again without a change class") {
		t.Errorf("expected one Manual Mode comment, got %q", comments)
	}
}

// Without a re-planner, the MR is told to plan it again by hand.
func TestFallBackToManualMode_NoReplanner(t *testing.T) {
	ctrl := gomock.NewController(t)
	gl := mocks.NewMockGitClient(ctrl)
	rs := mocks.NewMockStreamClient(ctrl)
	rs.EXPECT().MarkAutoModeManual(gomock.Any(), gomock.Any()).Return(true, nil)
	gl.EXPECT().SetClassStatus(gomock.Any(), gomock.Any()).Return(nil)
	var comments []string
	gl.EXPECT().CreateMergeRequestComment(gomock.Any(), 7, "zapier/x", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ int, _, msg string) error {
			comments = append(comments, msg)
			return nil
		}).Times(2)

	err := tfc_trigger.FallBackToManualMode(context.Background(), tfc_trigger.AutoModeDeps{GL: gl, RS: rs}, tfc_trigger.ManualModeFallback{
		Project: "zapier/x", MergeRequestIID: 7, CommitSHA: "abc", Class: "tags", Reason: "a plan failed", Replan: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(comments[1], "Comment `tfc plan`") {
		t.Errorf("expected the MR to be told to plan again, got %q", comments)
	}
}

// A merged MR's Auto Mode state is deleted; a closed one keeps it, so that
// reopening it does not bring Auto Mode back.
func TestTriggerCleanupEvent_ForgetsAutoModeStateOnMerge(t *testing.T) {
	for _, state := range []string{"merged", "closed"} {
		t.Run(state, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{}, t)
			ts.MockGitMR.EXPECT().GetState().Return(state).AnyTimes()
			if state == "merged" {
				ts.MockStreamClient.EXPECT().DeleteAutoModeMRState(runstream.AutoModeMRRef{VcsProvider: "gitlab", Project: ts.MetaData.ProjectNameNS, MergeRequest: 101})
			}
			ts.MockApiClient.EXPECT().GetTagsByQuery(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			ts.InitTestSuite()

			if err := newAutoModeTrigger(autoModeAppConfig(), ts, tfc_trigger.PlanAction).TriggerCleanupEvent(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A class plan whose only workspace never starts finishes the class at once:
// TFC/class fails, and the summary comment names the workspace.
func TestTFCEvents_UnplannedWorkspaceFailsTheClass(t *testing.T) {
	ctrl := gomock.NewController(t)
	cfg := autoModeWorkspaces()
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: cfg}, t)
	ref := runstream.ClassPlanRef{VcsProvider: "gitlab", Project: ts.MetaData.ProjectNameNS, MergeRequest: 101, CommitSHA: "abcd12233"}
	mrRef := ref.MR()
	finished := &runstream.ClassPlanState{
		VcsProvider: "gitlab", Project: ref.Project, MergeRequest: 101, CommitSHA: "abcd12233", Class: "tags", Finished: true,
		Workspaces: map[string]*runstream.ClassPlanWorkspace{"service-tfbuddy": {Outcome: runstream.ClassPlanFailed, Detail: "did not plan: blocked"}},
	}
	ts.MockStreamClient.EXPECT().RecordClassPlanResult(ref, "service-tfbuddy", "", false, gomock.Any()).Return(finished, true, nil)
	// Already marked, so no comment and no re-plan: just the report.
	ts.MockStreamClient.EXPECT().IsAutoModeManual(mrRef).Return(false, "", nil).Times(1)
	ts.MockStreamClient.EXPECT().IsAutoModeManual(mrRef).Return(true, "a plan failed", nil).AnyTimes()
	ts.MockStreamClient.EXPECT().GetAutoModeSummaryNote(mrRef).Return(int64(0), nil)
	ts.MockGitClient.EXPECT().CreateMergeRequestCommentWithID(gomock.Any(), 101, ts.MetaData.ProjectNameNS, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ int, _, body string) (int64, error) {
			if !strings.Contains(body, "| `service-tfbuddy` | none | failed: did not plan: blocked |") {
				t.Errorf("unexpected summary %q", body)
			}
			return 9, nil
		})
	ts.MockStreamClient.EXPECT().SetAutoModeSummaryNote(mrRef, int64(9)).Return(nil)
	expectMR(ctrl, ts, "tags-bot", autoModeTargetConfig, []string{autoModeTagsFile}, tagsBotCommits)
	// The target branch changed the workspace's paths, so it is blocked.
	ts.MockGitRepo.EXPECT().GetModifiedFileNamesBetweenCommits(ts.MetaData.CommonSHA, ts.MetaData.TargetBranch).
		Return([]string{"services/api/prod/main-us-east-1/main.tf"}, nil)
	var classStatuses []vcs.ClassStatus
	ts.MockGitClient.EXPECT().SetClassStatus(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s vcs.ClassStatus) error {
			classStatuses = append(classStatuses, s)
			return nil
		}).AnyTimes()
	ts.InitTestSuite()

	triggered, err := newAutoModeTrigger(autoModeAppConfig(), ts, tfc_trigger.PlanAction).TriggerTFCEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(triggered.Errored) != 1 {
		t.Fatalf("expected the workspace to be blocked, got %+v", triggered)
	}
	last := classStatuses[len(classStatuses)-1]
	if last.State != vcs.CommitStateSkipped || last.Description != "Manual Mode: a plan failed" {
		t.Errorf("TFC/class = %+v, want skipped for the Manual Mode MR", last)
	}
}

// An Auto Mode run is created from the commit its run message names, not from
// whatever the source branch's tip is when TFBuddy clones it.
func TestTFCEvents_AutoModeRunsTheExactCommit(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: autoModeWorkspaces()}, t)
	checkedOut := false
	ts.MockGitRepo.EXPECT().CheckoutCommit("abcd12233").DoAndReturn(func(string) error {
		checkedOut = true
		return nil
	})
	expectMR(ctrl, ts, "tags-bot", autoModeTargetConfig, []string{autoModeTagsFile}, tagsBotCommits)
	ts.MockGitClient.EXPECT().CreateMergeRequestDiscussion(gomock.Any(), ts.MetaData.MRIID, ts.MetaData.ProjectNameNS, gomock.Any()).
		Return(ts.MockGitDisc, nil)
	ts.MockApiClient.EXPECT().CreateRunFromSource(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, o *tfc_api.ApiRunOptions) (*tfe.Run, error) {
			if !checkedOut {
				t.Error("the run was created before the commit was checked out")
			}
			return &tfe.Run{
				ID:                   "run-1",
				Workspace:            &tfe.Workspace{Name: "service-tfbuddy", Organization: &tfe.Organization{Name: "zapier-test"}},
				ConfigurationVersion: &tfe.ConfigurationVersion{Speculative: true},
			}, nil
		})
	task := mocks.NewMockRunPollingTask(ctrl)
	task.EXPECT().Schedule(gomock.Any())
	ts.MockStreamClient.EXPECT().NewTFRunPollingTask(gomock.Any(), time.Second).Return(task)
	ts.MockGitClient.EXPECT().SetClassStatus(gomock.Any(), gomock.Any()).AnyTimes()
	ts.InitTestSuite()

	if _, err := newAutoModeTrigger(autoModeAppConfig(), ts, tfc_trigger.PlanAction).TriggerTFCEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// When the clone does not contain the commit, for example because the branch
// was force-pushed past it, nothing runs and every workspace fails.
func TestTFCEvents_AutoModeCommitMissingFailsClosed(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: autoModeWorkspaces()}, t)
	ts.MockGitRepo.EXPECT().CheckoutCommit("abcd12233").Return(errors.New("object not found"))
	expectMR(ctrl, ts, "tags-bot", autoModeTargetConfig, []string{autoModeTagsFile}, tagsBotCommits)
	var statuses []vcs.WorkspaceStatus
	ts.MockGitClient.EXPECT().SetWorkspaceStatus(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s vcs.WorkspaceStatus) error {
			statuses = append(statuses, s)
			return nil
		}).AnyTimes()
	// No CreateRunFromSource, AddTags or SetClassStatus expectation: nothing
	// may run or lock, and the class is not decided for a commit never planned.
	ts.InitTestSuite()

	_, err := newAutoModeTrigger(autoModeAppConfig(), ts, tfc_trigger.ApplyAction).TriggerTFCEvents(context.Background())
	if !errors.Is(err, utils.ErrPermanent) || !strings.Contains(err.Error(), "could not check out commit abcd12233") {
		t.Fatalf("expected a permanent refusal, got %v", err)
	}
	if len(statuses) != 1 || statuses[0].State != vcs.CommitStateFailed || statuses[0].Action != "apply" {
		t.Errorf("expected a failed apply status, got %+v", statuses)
	}
}

// An MR that stops fitting a class while TFBuddy decides an apply asks for a
// Manual Mode plan: the commit's plans so far carried the class marker.
func TestTFCEvents_FallbackDuringApplyAsksForAReplan(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: autoModeWorkspaces()}, t)
	ts.MockStreamClient.EXPECT().MarkAutoModeManual(gomock.Any(), gomock.Any()).Return(true, nil)
	ts.MockGitClient.EXPECT().CreateMergeRequestComment(gomock.Any(), 101, ts.MetaData.ProjectNameNS, gomock.Any()).Return(nil)
	expectMR(ctrl, ts, "tags-bot", autoModeTargetConfig, []string{"services/api/prod/main-us-east-1/main.tf"}, tagsBotCommits)
	expectManualModeClass(t, ts, "fit none of tags-bot's Auto Mode classes")
	ts.InitTestSuite()

	replanner := &fakeReplanner{}
	tCfg, _ := tfc_trigger.NewTFCTriggerConfig(&tfc_trigger.TFCTriggerOptions{
		Action: tfc_trigger.ApplyAction, Branch: ts.MetaData.SourceBranch, CommitSHA: "abcd12233",
		ProjectNameWithNamespace: ts.MetaData.ProjectNameNS, MergeRequestIID: 101,
		TriggerSource: tfc_trigger.MergeRequestEventTrigger, VcsProvider: "gitlab", AutoModeApproval: true,
	})
	trigger := tfc_trigger.NewTFCTrigger(autoModeAppConfig(), ts.MockGitClient, ts.MockApiClient, ts.MockStreamClient, tCfg)
	trigger.SetManualModeReplanner(replanner)
	if _, err := trigger.TriggerTFCEvents(context.Background()); err == nil {
		t.Fatal("an approval's apply of a Manual Mode MR must be refused")
	}
	if !slices.Equal(replanner.requests, []string{"zapier/tfbuddy!101@abcd12233"}) {
		t.Errorf("re-plan requests = %q, want one for the commit", replanner.requests)
	}
}

// approvedBy makes the suite's MR approved by usernames.
func approvedBy(ctrl *gomock.Controller, ts *mocks.TestSuite, usernames ...string) {
	approvals := mocks.NewMockMRApproved(ctrl)
	approvals.EXPECT().IsApproved().Return(true).AnyTimes()
	approvals.EXPECT().GetApprovers().Return(usernames).AnyTimes()
	ts.MockGitClient.EXPECT().GetMergeRequestApprovals(gomock.Any(), 101, ts.MetaData.ProjectNameNS).Return(approvals, nil).AnyTimes()
}

// An approval bot may have approved an MR before it fell back to Manual Mode, and
// as a code owner of the class files its approval alone can satisfy GitLab.
// A `tfc apply` of a fallen-back MR needs a person's approval.
func TestTFCEvents_FallenBackApplyNeedsAPerson(t *testing.T) {
	withApprovers := strings.Replace(autoModeTargetConfig, "  classes:", "  automaticApprovers: [approver-bot]\n  classes:", 1)
	for _, tt := range []struct {
		name      string
		approvers []string
		wantRun   bool
	}{
		{name: "only the automatic approver", approvers: []string{"approver-bot"}},
		{name: "the automatic approver and the Auto Mode author", approvers: []string{"Approver-Bot", "tags-bot"}},
		{name: "no approvals", approvers: nil},
		{name: "a person too", approvers: []string{"approver-bot", "alice"}, wantRun: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: autoModeWorkspaces()}, t)
			ts.MockStreamClient.EXPECT().IsAutoModeManual(gomock.Any()).Return(true, "a plan failed", nil)
			expectMR(ctrl, ts, "tags-bot", withApprovers, []string{autoModeTagsFile}, tagsBotCommits)
			approvedBy(ctrl, ts, tt.approvers...)
			var statuses []vcs.WorkspaceStatus
			ts.MockGitClient.EXPECT().SetWorkspaceStatus(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, s vcs.WorkspaceStatus) error {
					statuses = append(statuses, s)
					return nil
				}).AnyTimes()
			var message string
			if tt.wantRun {
				ts.MockApiClient.EXPECT().CreateRunFromSource(gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, o *tfc_api.ApiRunOptions) (*tfe.Run, error) {
						message = o.Message
						return &tfe.Run{
							ID:                   "run-1",
							Workspace:            &tfe.Workspace{Name: "service-tfbuddy", Organization: &tfe.Organization{Name: "zapier-test"}},
							ConfigurationVersion: &tfe.ConfigurationVersion{},
						}, nil
					})
			}
			ts.InitTestSuite()

			tCfg, _ := tfc_trigger.NewTFCTriggerConfig(&tfc_trigger.TFCTriggerOptions{
				Action: tfc_trigger.ApplyAction, Branch: ts.MetaData.SourceBranch, CommitSHA: "abcd12233",
				ProjectNameWithNamespace: ts.MetaData.ProjectNameNS, MergeRequestIID: 101,
				TriggerSource: tfc_trigger.CommentTrigger, VcsProvider: "gitlab", AutoMergeSequence: 5,
			})
			triggered, err := tfc_trigger.NewTFCTrigger(autoModeAppConfig(), ts.MockGitClient, ts.MockApiClient, ts.MockStreamClient, tCfg).
				TriggerTFCEvents(context.Background())
			if !tt.wantRun {
				if !errors.Is(err, utils.ErrPermanent) || !strings.Contains(err.Error(), "none of its approvals is from a person") {
					t.Fatalf("expected the apply to be refused, got %v", err)
				}
				if len(statuses) != 1 || statuses[0].State != vcs.CommitStateFailed {
					t.Errorf("expected a failed apply status, got %+v", statuses)
				}
				return
			}
			if err != nil || len(triggered.Errored) != 0 {
				t.Fatalf("expected the apply to run, got %v, %+v", err, triggered)
			}
			if message != "MR [!101]: MR Title" {
				t.Errorf("run message = %q, want a Manual Mode apply", message)
			}
		})
	}
}

// A redelivered MR event dispatches no run again: its workspace dispatch claims
// are taken. It keeps the class plans its first delivery began, and leaves
// TFC/class alone, so the runs that delivery started still decide the class.
func TestTFCEvents_RedeliveredClassPlanKeepsThePlans(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: autoModeWorkspaces()}, t)
	ref := runstream.ClassPlanRef{VcsProvider: "gitlab", Project: ts.MetaData.ProjectNameNS, MergeRequest: 101, CommitSHA: "abcd12233"}
	ts.MockStreamClient.EXPECT().BeginClassPlans(ref, "tags", []string{"service-tfbuddy"}, "delivery:delivery-1").Return(false, nil)
	ts.MockStreamClient.EXPECT().ClaimWorkspaceDispatch(gomock.Any()).Return(false, nil)
	// The workspace was dispatched by the first delivery, so nothing is
	// recorded for it, and no status is posted.
	ts.MockStreamClient.EXPECT().RecordClassPlanResult(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
	ts.MockGitClient.EXPECT().SetClassStatus(gomock.Any(), gomock.Any()).Times(0)
	expectMR(ctrl, ts, "tags-bot", autoModeTargetConfig, []string{autoModeTagsFile}, tagsBotCommits)
	ts.InitTestSuite()

	triggered, err := newAutoModeTrigger(autoModeAppConfig(), ts, tfc_trigger.PlanAction).TriggerTFCEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(triggered.Errored) != 0 || !slices.Equal(triggered.Executed, []string{"service-tfbuddy"}) {
		t.Fatalf("expected the workspace to count as dispatched, got %+v", triggered)
	}
}

// An approval checked for one commit never applies another: when the MR's head
// has moved on by the time the apply reads it, nothing runs.
func TestTFCEvents_AutoModeApprovalRefusesAStaleCommit(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: autoModeWorkspaces()}, t)
	ts.MockGitMR.EXPECT().GetHeadSHA().Return("ef567890a").AnyTimes()
	expectMR(ctrl, ts, "tags-bot", autoModeTargetConfig, []string{autoModeTagsFile}, tagsBotCommits)
	// No CreateRunFromSource, AddTags or status expectation: nothing may run,
	// lock or report.
	ts.MockGitClient.EXPECT().SetWorkspaceStatus(gomock.Any(), gomock.Any()).Times(0)
	ts.InitTestSuite()

	tCfg, _ := tfc_trigger.NewTFCTriggerConfig(&tfc_trigger.TFCTriggerOptions{
		Action: tfc_trigger.ApplyAction, Branch: ts.MetaData.SourceBranch, CommitSHA: "abcd12233",
		ProjectNameWithNamespace: ts.MetaData.ProjectNameNS, MergeRequestIID: 101,
		TriggerSource: tfc_trigger.MergeRequestEventTrigger, VcsProvider: "gitlab", DeliveryID: "delivery-1", AutoModeApproval: true,
	})
	_, err := tfc_trigger.NewTFCTrigger(autoModeAppConfig(), ts.MockGitClient, ts.MockApiClient, ts.MockStreamClient, tCfg).
		TriggerTFCEvents(context.Background())
	if !errors.Is(err, utils.ErrPermanent) || !strings.Contains(err.Error(), "the MR's head is now ef567890") {
		t.Fatalf("expected the stale approval's apply to be refused, got %v", err)
	}
}

// An Auto Mode apply that TFBuddy cannot start for a workspace sends no run
// event, so the trigger itself falls back: a retry must be a Manual Mode
// apply, which needs a person's approval.
func TestTFCEvents_AutoModeApplyThatCannotStartFallsBack(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: autoModeWorkspaces()}, t)
	mrRef := runstream.AutoModeMRRef{VcsProvider: "gitlab", Project: ts.MetaData.ProjectNameNS, MergeRequest: 101}
	ts.MockStreamClient.EXPECT().MarkAutoModeManual(mrRef, "its apply of `service-tfbuddy` could not be started").Return(true, nil)
	expectMR(ctrl, ts, "tags-bot", autoModeTargetConfig, []string{autoModeTagsFile}, tagsBotCommits)
	ts.MockGitClient.EXPECT().CreateMergeRequestDiscussion(gomock.Any(), ts.MetaData.MRIID, ts.MetaData.ProjectNameNS, gomock.Any()).
		Return(ts.MockGitDisc, nil)
	ts.MockApiClient.EXPECT().CreateRunFromSource(gomock.Any(), gomock.Any()).Return(nil, errors.New("TFC is down"))
	expectManualModeClass(t, ts, "its apply of `service-tfbuddy` could not be started")
	var comments []string
	ts.MockGitClient.EXPECT().CreateMergeRequestComment(gomock.Any(), 101, ts.MetaData.ProjectNameNS, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ int, _, msg string) error {
			comments = append(comments, msg)
			return nil
		}).AnyTimes()
	ts.InitTestSuite()

	replanner := &fakeReplanner{}
	trigger := newAutoModeTrigger(autoModeAppConfig(), ts, tfc_trigger.ApplyAction)
	trigger.SetManualModeReplanner(replanner)
	triggered, err := trigger.TriggerTFCEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(triggered.Errored) != 1 {
		t.Fatalf("expected the apply to fail to start, got %+v", triggered)
	}
	if !slices.Equal(replanner.requests, []string{"zapier/tfbuddy!101@abcd12233"}) {
		t.Errorf("re-plan requests = %q, want one for the commit", replanner.requests)
	}
	if len(comments) != 1 || !strings.Contains(comments[0], "now runs in Manual Mode because its apply of `service-tfbuddy` could not be started") {
		t.Errorf("expected one Manual Mode comment, got %q", comments)
	}
}

// An MR that fell back to Manual Mode stays there when it is retargeted to a
// branch that has no Auto Mode: `tfc apply` still needs a person's approval.
func TestTFCEvents_RetargetedFallenBackMRNeedsAPerson(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: autoModeWorkspaces()}, t)
	a := mocks.NewMockMRAuthor(ctrl)
	a.EXPECT().GetUsername().Return("tags-bot").AnyTimes()
	ts.MockGitMR.EXPECT().GetAuthor().Return(a).AnyTimes()
	defaultBranch(ctrl, ts.MockGitClient, "trunk")
	withApprovers := strings.Replace(autoModeTargetConfig, "  classes:", "  automaticApprovers: [approver-bot]\n  classes:", 1)
	ts.MockGitClient.EXPECT().GetRepoFile(gomock.Any(), ts.MetaData.ProjectNameNS, ".tfbuddy.yaml", "trunk").Return([]byte(withApprovers), nil).AnyTimes()
	ts.MockStreamClient.EXPECT().IsAutoModeManual(gomock.Any()).Return(true, "a plan failed", nil)
	approvedBy(ctrl, ts, "approver-bot")
	ts.MockGitClient.EXPECT().SetWorkspaceStatus(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	// No CreateRunFromSource expectation: nothing may run.
	ts.InitTestSuite()

	tCfg, _ := tfc_trigger.NewTFCTriggerConfig(&tfc_trigger.TFCTriggerOptions{
		Action: tfc_trigger.ApplyAction, Branch: ts.MetaData.SourceBranch, CommitSHA: "abcd12233",
		ProjectNameWithNamespace: ts.MetaData.ProjectNameNS, MergeRequestIID: 101,
		TriggerSource: tfc_trigger.CommentTrigger, VcsProvider: "gitlab", AutoMergeSequence: 5,
	})
	_, err := tfc_trigger.NewTFCTrigger(autoModeAppConfig(), ts.MockGitClient, ts.MockApiClient, ts.MockStreamClient, tCfg).
		TriggerTFCEvents(context.Background())
	if !errors.Is(err, utils.ErrPermanent) || !strings.Contains(err.Error(), "none of its approvals is from a person") {
		t.Fatalf("expected the retargeted MR's apply to be refused, got %v", err)
	}
}

// The class is decided from the MR's current changes, which are its head's.
// A trigger for an older commit, such as a delayed push event, must not run
// that commit with the head's class.
func TestTFCEvents_AutoModeRefusesAnOldCommit(t *testing.T) {
	for _, action := range []tfc_trigger.TriggerAction{tfc_trigger.PlanAction, tfc_trigger.ApplyAction} {
		t.Run(action.String(), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: autoModeWorkspaces()}, t)
			ts.MockGitMR.EXPECT().GetHeadSHA().Return("ef567890a").AnyTimes()
			expectMR(ctrl, ts, "tags-bot", autoModeTargetConfig, []string{autoModeTagsFile}, tagsBotCommits)
			ts.MockGitClient.EXPECT().SetWorkspaceStatus(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			// No CreateRunFromSource, AddTags or class expectation: nothing may
			// run, lock or begin class plans.
			ts.MockStreamClient.EXPECT().BeginClassPlans(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			ts.InitTestSuite()

			_, err := newAutoModeTrigger(autoModeAppConfig(), ts, action).TriggerTFCEvents(context.Background())
			if !errors.Is(err, utils.ErrPermanent) || !strings.Contains(err.Error(), "the MR's head is now ef567890") {
				t.Fatalf("expected the old commit to be refused, got %v", err)
			}
		})
	}
}

// A redelivered MR event whose first delivery finished the class without
// reporting it, because no workspace started, reports it: no run event will.
// A report that fails again is retried. (Had the first delivery recorded its
// fallback, the redelivery would plan in Manual Mode, which sets TFC/class
// to skipped by itself.)
func TestTFCEvents_RedeliveryReportsAnUnreportedClass(t *testing.T) {
	for _, statusFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("status write fails %v", statusFails), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: autoModeWorkspaces()}, t)
			ref := runstream.ClassPlanRef{VcsProvider: "gitlab", Project: ts.MetaData.ProjectNameNS, MergeRequest: 101, CommitSHA: "abcd12233"}
			finished := &runstream.ClassPlanState{
				VcsProvider: "gitlab", Project: ref.Project, MergeRequest: 101, CommitSHA: "abcd12233", Class: "tags",
				Finished: true, Trigger: "delivery:delivery-1",
				Workspaces: map[string]*runstream.ClassPlanWorkspace{"service-tfbuddy": {Outcome: runstream.ClassPlanFailed, Detail: "did not plan: TFC is down"}},
			}
			ts.MockStreamClient.EXPECT().BeginClassPlans(ref, "tags", []string{"service-tfbuddy"}, "delivery:delivery-1").Return(false, nil)
			ts.MockStreamClient.EXPECT().ClaimWorkspaceDispatch(gomock.Any()).Return(false, nil)
			// Once to find the unreported class, once by the report itself.
			ts.MockStreamClient.EXPECT().GetClassPlans(ref).Return(finished, nil).Times(2)
			ts.MockStreamClient.EXPECT().IsAutoModeManual(gomock.Any()).Return(false, "", nil).AnyTimes()
			var statusErr error
			if statusFails {
				statusErr = errors.New("502 Bad Gateway")
			} else {
				ts.MockStreamClient.EXPECT().GetAutoModeSummaryNote(ref.MR()).Return(int64(9), nil)
				ts.MockGitClient.EXPECT().UpdateMergeRequestComment(gomock.Any(), 101, int64(9), ts.MetaData.ProjectNameNS, gomock.Any()).Return(nil)
				ts.MockStreamClient.EXPECT().MarkClassPlansReported(ref, "delivery:delivery-1").Return(nil)
			}
			ts.MockGitClient.EXPECT().SetClassStatus(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, s vcs.ClassStatus) error {
				if s.State != vcs.CommitStateFailed || !strings.Contains(s.Description, "service-tfbuddy did not pass") {
					t.Errorf("TFC/class = %+v, want failed for the workspace that did not plan", s)
				}
				return statusErr
			})
			expectMR(ctrl, ts, "tags-bot", autoModeTargetConfig, []string{autoModeTagsFile}, tagsBotCommits)
			ts.InitTestSuite()

			_, err := newAutoModeTrigger(autoModeAppConfig(), ts, tfc_trigger.PlanAction).TriggerTFCEvents(context.Background())
			if statusFails {
				if err == nil || errors.Is(err, utils.ErrPermanent) {
					t.Fatalf("a report that failed must be retried, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A Manual Mode mark NATS does not record at once is retried before the
// fallback gives up: some callers have no later event to retry it.
func TestFallBackToManualMode_RetriesTheMark(t *testing.T) {
	ctrl := gomock.NewController(t)
	gl := mocks.NewMockGitClient(ctrl)
	rs := mocks.NewMockStreamClient(ctrl)
	gomock.InOrder(
		rs.EXPECT().MarkAutoModeManual(gomock.Any(), "a plan failed").Return(false, errors.New("nats: timeout")),
		rs.EXPECT().MarkAutoModeManual(gomock.Any(), "a plan failed").Return(true, nil),
	)
	gl.EXPECT().SetClassStatus(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, s vcs.ClassStatus) error {
		if s.State != vcs.CommitStateSkipped {
			t.Errorf("TFC/class = %+v, want skipped", s)
		}
		return nil
	})
	gl.EXPECT().CreateMergeRequestComment(gomock.Any(), 7, "zapier/x", gomock.Any()).Return(nil)

	err := tfc_trigger.FallBackToManualMode(context.Background(), tfc_trigger.AutoModeDeps{GL: gl, RS: rs}, tfc_trigger.ManualModeFallback{
		Project: "zapier/x", MergeRequestIID: 7, CommitSHA: "abc", Class: "tags", Reason: "a plan failed",
	})
	if err != nil {
		t.Fatal(err)
	}
}
