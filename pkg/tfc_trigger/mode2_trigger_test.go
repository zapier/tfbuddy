package tfc_trigger_test

import (
	"context"
	"errors"
	"slices"
	"strings"
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

// mode2TargetConfig is the target branch's .tfbuddy.yaml in these tests.
const mode2TargetConfig = `
workspaces:
  - name: service-tfbuddy
    organization: zapier-test
mode2:
  authors:
    - username: tags-bot
      commitEmails: [tags-bot@example.com]
  classes:
    - name: tags
      authors: [tags-bot]
      paths: ["**/tags.auto.tfvars.json"]
`

const mode2TagsFile = "services/api/prod/main-us-east-1/tags.auto.tfvars.json"

func mode2AppConfig() config.Config {
	cfg := config.C
	cfg.Mode2Enabled = true
	return cfg
}

// mode2Workspaces is the source branch's config: one workspace that every
// change triggers, opted into auto-merge.
func mode2Workspaces() *tfc_trigger.ProjectConfig {
	return &tfc_trigger.ProjectConfig{Workspaces: []*tfc_trigger.TFCWorkspace{{
		Name:         "service-tfbuddy",
		Organization: "zapier-test",
		Mode:         "apply-before-merge",
		AutoMerge:    true,
		TriggerDirs:  []string{"**"},
	}}}
}

// expectMR makes the suite's MR one by author that changes files in commits,
// with targetConfig as the target branch's .tfbuddy.yaml. It must run before
// InitTestSuite, whose catch-all expectations would otherwise match first.
func expectMR(ctrl *gomock.Controller, ts *mocks.TestSuite, author, targetConfig string, files []string, commits []vcs.MRCommit) {
	a := mocks.NewMockMRAuthor(ctrl)
	a.EXPECT().GetUsername().Return(author).AnyTimes()
	ts.MockGitMR.EXPECT().GetAuthor().Return(a).AnyTimes()
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

func newMode2Trigger(appCfg config.Config, ts *mocks.TestSuite, action tfc_trigger.TriggerAction) tfc_trigger.Trigger {
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

func TestLoadMode2Config_ReadsTargetBranchOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	gl := mocks.NewMockGitClient(ctrl)
	// Only the target branch is expected; a read of any other branch fails.
	gl.EXPECT().GetRepoFile(gomock.Any(), "zapier/tfbuddy", ".tfbuddy.yaml", "main").Return([]byte(mode2TargetConfig), nil)

	cfg, err := tfc_trigger.LoadMode2Config(context.Background(), gl, "zapier/tfbuddy", "main")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Author("tags-bot") == nil {
		t.Fatal("expected tags-bot to be a Mode 2 author")
	}
}

func TestLoadMode2Config_MissingFileIsNotMode2(t *testing.T) {
	ctrl := gomock.NewController(t)
	gl := mocks.NewMockGitClient(ctrl)
	gl.EXPECT().GetRepoFile(gomock.Any(), "zapier/tfbuddy", ".tfbuddy.yaml", "main").
		Return(nil, utils.CreatePermanentError(errors.New("404 File Not Found")))

	cfg, err := tfc_trigger.LoadMode2Config(context.Background(), gl, "zapier/tfbuddy", "main")
	if err != nil || cfg != nil {
		t.Fatalf("a branch without the file has no Mode 2 config, got %+v, %v", cfg, err)
	}
}

func TestLoadMode2Config_TransientErrorFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	gl := mocks.NewMockGitClient(ctrl)
	gl.EXPECT().GetRepoFile(gomock.Any(), "zapier/tfbuddy", ".tfbuddy.yaml", "main").Return(nil, errors.New("502 Bad Gateway"))

	if _, err := tfc_trigger.LoadMode2Config(context.Background(), gl, "zapier/tfbuddy", "main"); err == nil {
		t.Fatal("a read GitLab did not answer must not decide that the MR is not in Mode 2")
	}
}

func TestTFCEvents_Mode2PlanCarriesClass(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: mode2Workspaces()}, t)
	expectMR(ctrl, ts, "tags-bot", mode2TargetConfig, []string{mode2TagsFile}, tagsBotCommits)
	opts, rmd := capturePlanRun(ctrl, ts)
	var classStatuses []vcs.ClassStatus
	ts.MockGitClient.EXPECT().SetClassStatus(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s vcs.ClassStatus) error {
			classStatuses = append(classStatuses, s)
			return nil
		}).AnyTimes()
	ts.InitTestSuite()

	triggered, err := newMode2Trigger(mode2AppConfig(), ts, tfc_trigger.PlanAction).TriggerTFCEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(triggered.Errored) != 0 {
		t.Fatalf("unexpected failed workspaces: %v", triggered.Errored)
	}
	if opts.Message != "MR [!101] [tfbuddy-class=tags] sha=abcd12233 actor=tags-bot" {
		t.Errorf("run message = %q, want the tags class marker and no title", opts.Message)
	}
	if rmd.ChangeClass != "tags" || !slices.Equal(rmd.ClassWorkspaces, []string{"service-tfbuddy"}) {
		t.Errorf("run metadata class = %q over %v, want tags over [service-tfbuddy]", rmd.ChangeClass, rmd.ClassWorkspaces)
	}
	if len(classStatuses) != 1 || classStatuses[0].State != vcs.CommitStatePending || classStatuses[0].CommitSHA != "abcd12233" {
		t.Errorf("expected one pending TFC/class status on the head commit, got %+v", classStatuses)
	}
}

func TestTFCEvents_PersonTitleIsKept(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: mode2Workspaces()}, t)
	ts.MockGitMR.EXPECT().GetTitle().Return("Add bucket [tfbuddy-class=tags]").AnyTimes()
	// A person is not on the list, so their commits are never read.
	expectMR(ctrl, ts, "a-person", mode2TargetConfig, []string{mode2TagsFile}, nil)
	opts, rmd := capturePlanRun(ctrl, ts)
	ts.InitTestSuite()

	if _, err := newMode2Trigger(mode2AppConfig(), ts, tfc_trigger.PlanAction).TriggerTFCEvents(context.Background()); err != nil {
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

// The source branch is the MR's own: a mode2 section there must not put its
// author in Mode 2.
func TestTFCEvents_Mode2ConfigOnSourceBranchIsIgnored(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: mode2Workspaces()}, t)
	sourceConfig := `
workspaces:
  - name: service-tfbuddy
    organization: zapier-test
    triggerDirs: ["**"]
mode2:
  authors: [{username: a-person, commitEmails: [person@example.com]}]
  classes: [{name: tags, authors: [a-person], paths: ["**"]}]
`
	ts.MockGitClient.EXPECT().GetRepoFile(gomock.Any(), ts.MetaData.ProjectNameNS, ".tfbuddy.yaml", ts.MetaData.SourceBranch).
		Return([]byte(sourceConfig), nil).AnyTimes()
	expectMR(ctrl, ts, "a-person", "workspaces:\n  - name: service-tfbuddy\n", []string{mode2TagsFile}, nil)
	opts, _ := capturePlanRun(ctrl, ts)
	ts.InitTestSuite()

	if _, err := newMode2Trigger(mode2AppConfig(), ts, tfc_trigger.PlanAction).TriggerTFCEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(opts.Message, "tfbuddy-class") {
		t.Errorf("run message = %q, want no class from the source branch's config", opts.Message)
	}
}

func TestTFCEvents_Mode2DisabledIsMode1(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: mode2Workspaces()}, t)
	// No GetAuthor or target-branch expectations: with the flag off, TFBuddy
	// must not even look.
	ts.MockGitClient.EXPECT().GetMergeRequestModifiedFiles(gomock.Any(), ts.MetaData.MRIID, ts.MetaData.ProjectNameNS).
		Return([]string{mode2TagsFile}, nil).AnyTimes()
	opts, _ := capturePlanRun(ctrl, ts)
	ts.InitTestSuite()

	if _, err := newMode2Trigger(config.C, ts, tfc_trigger.PlanAction).TriggerTFCEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if opts.Message != "MR [!101]: MR Title" {
		t.Errorf("run message = %q, want today's message", opts.Message)
	}
}

func TestTFCEvents_Mode2DivergenceCheckFailsClosed(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: mode2Workspaces()}, t)
	expectMR(ctrl, ts, "tags-bot", mode2TargetConfig, []string{mode2TagsFile}, tagsBotCommits)
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

	_, err := newMode2Trigger(mode2AppConfig(), ts, tfc_trigger.ApplyAction).TriggerTFCEvents(context.Background())
	if !errors.Is(err, utils.ErrPermanent) {
		t.Fatalf("expected a permanent error, got %v", err)
	}
	if len(statuses) != 1 || statuses[0].State != vcs.CommitStateFailed || !strings.Contains(statuses[0].Description, "could not check the target branch") {
		t.Errorf("expected the workspace to be blocked, got %+v", statuses)
	}
}

func TestTFCEvents_Mode2ApplyRequiresAutoMerge(t *testing.T) {
	ctrl := gomock.NewController(t)
	workspaces := mode2Workspaces()
	workspaces.Workspaces[0].AutoMerge = false
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: workspaces}, t)
	expectMR(ctrl, ts, "tags-bot", mode2TargetConfig, []string{mode2TagsFile}, tagsBotCommits)
	ts.InitTestSuite()

	_, err := newMode2Trigger(mode2AppConfig(), ts, tfc_trigger.ApplyAction).TriggerTFCEvents(context.Background())
	if err == nil || !strings.Contains(err.Error(), "auto-merge") {
		t.Fatalf("a Mode 2 apply that cannot auto-merge must be refused, got %v", err)
	}
}
