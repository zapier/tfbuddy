package gitlab_hooks

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/zapier/tfbuddy/pkg/allow_list"
	"github.com/zapier/tfbuddy/pkg/comment_actions"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/vcs"
)

func Test_parseCommentCommand(t *testing.T) {
	type args struct {
		noteBody string
	}
	tests := []struct {
		name    string
		args    args
		want    *comment_actions.CommentOpts
		wantErr bool
	}{
		{
			name: "tfc plan (all workspaces)",
			args: args{"tfc plan"},
			want: &comment_actions.CommentOpts{
				Args: comment_actions.CommentArgs{
					Agent:   "tfc",
					Command: "plan",
					Rest:    nil,
				},
				TriggerOpts: &tfc_trigger.TFCTriggerOptions{
					Workspace: "",
					Action:    tfc_trigger.PlanAction,
				},
			},
			wantErr: false,
		},
		{
			name: "tfc apply (all workspaces)",
			args: args{"tfc apply"},
			want: &comment_actions.CommentOpts{
				Args: comment_actions.CommentArgs{
					Agent:   "tfc",
					Command: "apply",
					Rest:    nil,
				},
				TriggerOpts: &tfc_trigger.TFCTriggerOptions{
					Workspace: "",
					Action:    tfc_trigger.ApplyAction,
				},
			},
			wantErr: false,
		},
		{
			name: "tfc plan (single workspaces)",
			args: args{"tfc plan -w service-foo"},
			want: &comment_actions.CommentOpts{
				Args: comment_actions.CommentArgs{
					Agent:   "tfc",
					Command: "plan",
					Rest:    nil,
				},
				TriggerOpts: &tfc_trigger.TFCTriggerOptions{
					Workspace: "service-foo",
					Action:    tfc_trigger.PlanAction,
				},
			},
			wantErr: false,
		},
		{
			name: "tfc apply (single workspaces)",
			args: args{"tfc apply -w service-foo"},
			want: &comment_actions.CommentOpts{
				Args: comment_actions.CommentArgs{
					Agent:   "tfc",
					Command: "apply",
					Rest:    nil,
				},
				TriggerOpts: &tfc_trigger.TFCTriggerOptions{
					Workspace: "service-foo",
					Action:    tfc_trigger.ApplyAction,
				},
			},
			wantErr: false,
		},
		{
			name:    "not tfc command",
			args:    args{"amazing gitlab review comment"},
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := comment_actions.ParseCommentCommand(tt.args.noteBody)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseCommentCommand() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			assert.Equal(t, got, tt.want)
		})
	}
}

func TestProcessNoteEventPlanError(t *testing.T) {
	os.Setenv(allow_list.GitlabProjectAllowListEnv, "zapier/")
	defer os.Unsetenv(allow_list.GitlabProjectAllowListEnv)
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockGitClient := mocks.NewMockGitClient(mockCtrl)
	mockGitClient.EXPECT().CreateMergeRequestComment(gomock.Any(), 101, "zapier/service-tf-buddy", ":no_entry: could not be run because: something went wrong")
	mockApiClient := mocks.NewMockApiClient(mockCtrl)
	mockStreamClient := mocks.NewMockStreamClient(mockCtrl)
	mockProject := mocks.NewMockProject(mockCtrl)
	mockProject.EXPECT().GetPathWithNamespace().Return("zapier/service-tf-buddy").Times(2)

	mockLastCommit := mocks.NewMockCommit(mockCtrl)
	mockLastCommit.EXPECT().GetSHA().Return("abvc12345")

	mockAttributes := mocks.NewMockMRAttributes(mockCtrl)

	mockAttributes.EXPECT().GetNote().Return("tfc plan -w service-tf-buddy")
	mockAttributes.EXPECT().GetType().Return("SomeNote")

	mockMREvent := mocks.NewMockMRCommentEvent(mockCtrl)
	mockMREvent.EXPECT().GetProject().Return(mockProject).Times(2)
	mockMREvent.EXPECT().GetAttributes().Return(mockAttributes).Times(2)
	mockMREvent.EXPECT().GetLastCommit().Return(mockLastCommit)

	mockSimpleMR := mocks.NewMockMR(mockCtrl)
	mockSimpleMR.EXPECT().GetSourceBranch().Return("DTA-2009")

	mockSimpleMR.EXPECT().GetInternalID().Return(101).Times(2)
	mockMREvent.EXPECT().GetMR().Return(mockSimpleMR).Times(3)

	mockTFCTrigger := mocks.NewMockTrigger(mockCtrl)
	mockTFCTrigger.EXPECT().TriggerTFCEvents(gomock.Any()).Return(nil, fmt.Errorf("something went wrong"))

	client := &GitlabEventWorker{
		gl:        mockGitClient,
		tfc:       mockApiClient,
		runstream: mockStreamClient,
		triggerCreation: func(gl vcs.GitClient, tfc tfc_api.ApiClient, runstream runstream.StreamClient, cfg *tfc_trigger.TFCTriggerOptions) tfc_trigger.Trigger {
			return mockTFCTrigger
		},
	}

	proj, err := client.processNoteEvent(context.Background(), mockMREvent)
	if err == nil {
		t.Error("expected error")
		return
	}
	if proj != "zapier/service-tf-buddy" {
		t.Error("unexpected project")
		return
	}
}

func TestProcessNoteEventPanicHandling(t *testing.T) {
	os.Setenv(allow_list.GitlabProjectAllowListEnv, "zapier/")
	defer os.Unsetenv(allow_list.GitlabProjectAllowListEnv)
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	testSuite.MockProject.EXPECT().GetPathWithNamespace().DoAndReturn(func() string {
		var a []string
		return a[0]
	}).AnyTimes()

	testSuite.InitTestSuite()
	client := &GitlabEventWorker{
		gl:        testSuite.MockGitClient,
		tfc:       testSuite.MockApiClient,
		runstream: testSuite.MockStreamClient,
		triggerCreation: func(gl vcs.GitClient, tfc tfc_api.ApiClient, runstream runstream.StreamClient, cfg *tfc_trigger.TFCTriggerOptions) tfc_trigger.Trigger {
			return nil
		},
	}
	err := client.processNoteEventStreamMsg(&NoteEventMsg{
		Context: context.Background(),
	})
	if err != nil {
		t.Error("unexpected error", err)
		return
	}
}
func TestProcessNoteEventPlan(t *testing.T) {
	os.Setenv(allow_list.GitlabProjectAllowListEnv, "zapier/")
	defer os.Unsetenv(allow_list.GitlabProjectAllowListEnv)
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockGitClient := mocks.NewMockGitClient(mockCtrl)

	mockApiClient := mocks.NewMockApiClient(mockCtrl)
	mockStreamClient := mocks.NewMockStreamClient(mockCtrl)
	mockProject := mocks.NewMockProject(mockCtrl)
	mockProject.EXPECT().GetPathWithNamespace().Return("zapier/service-tf-buddy")

	mockLastCommit := mocks.NewMockCommit(mockCtrl)
	mockLastCommit.EXPECT().GetSHA().Return("abvc12345")

	mockAttributes := mocks.NewMockMRAttributes(mockCtrl)

	mockAttributes.EXPECT().GetNote().Return("tfc plan -w service-tf-buddy")
	mockAttributes.EXPECT().GetType().Return("SomeNote")

	mockMREvent := mocks.NewMockMRCommentEvent(mockCtrl)
	mockMREvent.EXPECT().GetProject().Return(mockProject)
	mockMREvent.EXPECT().GetAttributes().Return(mockAttributes).Times(2)
	mockMREvent.EXPECT().GetLastCommit().Return(mockLastCommit)

	mockSimpleMR := mocks.NewMockMR(mockCtrl)
	mockSimpleMR.EXPECT().GetSourceBranch().Return("DTA-2009")

	mockSimpleMR.EXPECT().GetInternalID().Return(101)
	mockMREvent.EXPECT().GetMR().Return(mockSimpleMR).Times(2)

	mockTFCTrigger := mocks.NewMockTrigger(mockCtrl)
	mockTFCTrigger.EXPECT().TriggerTFCEvents(gomock.Any()).Return(&tfc_trigger.TriggeredTFCWorkspaces{
		Executed: []string{"service-tf-buddy"},
	}, nil)

	client := &GitlabEventWorker{
		gl:        mockGitClient,
		tfc:       mockApiClient,
		runstream: mockStreamClient,
		triggerCreation: func(gl vcs.GitClient, tfc tfc_api.ApiClient, runstream runstream.StreamClient, cfg *tfc_trigger.TFCTriggerOptions) tfc_trigger.Trigger {
			return mockTFCTrigger
		},
	}

	proj, err := client.processNoteEvent(context.Background(), mockMREvent)
	if err != nil {
		t.Fatal(err)
	}
	if proj != "zapier/service-tf-buddy" {
		t.Fatal("expected a project name to be returned")
	}
}

func TestProcessNoteEventPlanFailedWorkspace(t *testing.T) {
	os.Setenv(allow_list.GitlabProjectAllowListEnv, "zapier/")
	defer os.Unsetenv(allow_list.GitlabProjectAllowListEnv)
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	testSuite.MockGitClient.EXPECT().CreateMergeRequestComment(gomock.Any(), 101, testSuite.MetaData.ProjectNameNS, ":no_entry: service-tf-buddy could not be run because: could not fetch upstream").Return(nil)

	mockLastCommit := mocks.NewMockCommit(mockCtrl)

	mockLastCommit.EXPECT().GetSHA().Return("abvc12345")

	mockAttributes := mocks.NewMockMRAttributes(mockCtrl)

	mockAttributes.EXPECT().GetNote().Return("tfc plan -w service-tf-buddy")
	mockAttributes.EXPECT().GetType().Return("SomeNote")

	mockMREvent := mocks.NewMockMRCommentEvent(mockCtrl)
	mockMREvent.EXPECT().GetProject().Return(testSuite.MockProject).Times(2)
	mockMREvent.EXPECT().GetAttributes().Return(mockAttributes).Times(2)
	mockMREvent.EXPECT().GetLastCommit().Return(mockLastCommit)

	mockMREvent.EXPECT().GetMR().Return(testSuite.MockGitMR).Times(3)

	mockTFCTrigger := mocks.NewMockTrigger(mockCtrl)

	// mockTFCTrigger.EXPECT().GetConfig().Return(testSuite.MockTriggerConfig).Times(2)
	mockTFCTrigger.EXPECT().TriggerTFCEvents(gomock.Any()).Return(&tfc_trigger.TriggeredTFCWorkspaces{
		Errored: []*tfc_trigger.ErroredWorkspace{{
			Name:  "service-tf-buddy",
			Error: "could not fetch upstream",
		},
		},
	}, nil)

	testSuite.InitTestSuite()

	worker := &GitlabEventWorker{
		tfc:       testSuite.MockApiClient,
		gl:        testSuite.MockGitClient,
		runstream: testSuite.MockStreamClient,
		triggerCreation: func(gl vcs.GitClient, tfc tfc_api.ApiClient, runstream runstream.StreamClient, cfg *tfc_trigger.TFCTriggerOptions) tfc_trigger.Trigger {
			return mockTFCTrigger
		},
	}

	proj, err := worker.processNoteEvent(context.Background(), mockMREvent)
	if err != nil {
		t.Fatal(err)
	}
	if proj != testSuite.MetaData.ProjectNameNS {
		t.Fatal("expected a project name to be returned")
	}
}

func TestProcessNoteEventPlanFailedMultipleWorkspaces(t *testing.T) {
	os.Setenv(allow_list.GitlabProjectAllowListEnv, "zapier/")
	defer os.Unsetenv(allow_list.GitlabProjectAllowListEnv)
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	gomock.InOrder(
		testSuite.MockGitClient.EXPECT().CreateMergeRequestComment(gomock.Any(), 101, testSuite.MetaData.ProjectNameNS, ":no_entry: service-tf-buddy could not be run because: could not fetch upstream").Return(nil),
		testSuite.MockGitClient.EXPECT().CreateMergeRequestComment(gomock.Any(), 101, testSuite.MetaData.ProjectNameNS, ":no_entry: service-tf-buddy-staging could not be run because: workspace has been modified on target branch").Return(nil),
	)
	mockLastCommit := mocks.NewMockCommit(mockCtrl)
	mockLastCommit.EXPECT().GetSHA().Return("abvc12345")

	mockAttributes := mocks.NewMockMRAttributes(mockCtrl)

	mockAttributes.EXPECT().GetNote().Return("tfc plan")
	mockAttributes.EXPECT().GetType().Return("SomeNote")

	mockMREvent := mocks.NewMockMRCommentEvent(mockCtrl)
	mockMREvent.EXPECT().GetProject().Return(testSuite.MockProject).AnyTimes()
	mockMREvent.EXPECT().GetAttributes().Return(mockAttributes).Times(2)
	mockMREvent.EXPECT().GetLastCommit().Return(mockLastCommit)
	mockMREvent.EXPECT().GetMR().Return(testSuite.MockGitMR).AnyTimes()

	mockTFCTrigger := mocks.NewMockTrigger(mockCtrl)
	mockTFCTrigger.EXPECT().TriggerTFCEvents(gomock.Any()).Return(&tfc_trigger.TriggeredTFCWorkspaces{
		Errored: []*tfc_trigger.ErroredWorkspace{{
			Name:  "service-tf-buddy",
			Error: "could not fetch upstream",
		},
			{
				Name:  "service-tf-buddy-staging",
				Error: "workspace has been modified on target branch",
			},
		},
	}, nil)

	testSuite.InitTestSuite()

	client := &GitlabEventWorker{
		gl:        testSuite.MockGitClient,
		tfc:       testSuite.MockApiClient,
		runstream: testSuite.MockStreamClient,
		triggerCreation: func(gl vcs.GitClient, tfc tfc_api.ApiClient, runstream runstream.StreamClient, cfg *tfc_trigger.TFCTriggerOptions) tfc_trigger.Trigger {
			return mockTFCTrigger
		},
	}

	proj, err := client.processNoteEvent(context.Background(), mockMREvent)
	if err != nil {
		t.Fatal(err)
	}
	if proj != testSuite.MetaData.ProjectNameNS {
		t.Fatal("expected a project name to be returned")
	}
}

func TestProcessNoteEventNoErrorNoRuns(t *testing.T) {
	os.Setenv(allow_list.GitlabProjectAllowListEnv, "zapier/")
	defer os.Unsetenv(allow_list.GitlabProjectAllowListEnv)
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockGitClient := mocks.NewMockGitClient(mockCtrl)

	mockApiClient := mocks.NewMockApiClient(mockCtrl)
	mockStreamClient := mocks.NewMockStreamClient(mockCtrl)
	mockProject := mocks.NewMockProject(mockCtrl)
	mockProject.EXPECT().GetPathWithNamespace().Return("zapier/service-tf-buddy")

	mockLastCommit := mocks.NewMockCommit(mockCtrl)
	mockLastCommit.EXPECT().GetSHA().Return("abvc12345")

	mockAttributes := mocks.NewMockMRAttributes(mockCtrl)

	mockAttributes.EXPECT().GetNote().Return("tfc plan -w service-tf-buddy")
	mockAttributes.EXPECT().GetType().Return("SomeNote")

	mockMREvent := mocks.NewMockMRCommentEvent(mockCtrl)
	mockMREvent.EXPECT().GetProject().Return(mockProject)
	mockMREvent.EXPECT().GetAttributes().Return(mockAttributes).Times(2)
	mockMREvent.EXPECT().GetLastCommit().Return(mockLastCommit)

	mockSimpleMR := mocks.NewMockMR(mockCtrl)
	mockSimpleMR.EXPECT().GetSourceBranch().Return("DTA-2009")

	mockSimpleMR.EXPECT().GetInternalID().Return(101)
	mockMREvent.EXPECT().GetMR().Return(mockSimpleMR).Times(2)

	mockTFCTrigger := mocks.NewMockTrigger(mockCtrl)
	mockTFCTrigger.EXPECT().TriggerTFCEvents(gomock.Any()).Return(nil, nil)

	client := &GitlabEventWorker{
		gl:        mockGitClient,
		tfc:       mockApiClient,
		runstream: mockStreamClient,
		triggerCreation: func(gl vcs.GitClient, tfc tfc_api.ApiClient, runstream runstream.StreamClient, cfg *tfc_trigger.TFCTriggerOptions) tfc_trigger.Trigger {
			return mockTFCTrigger
		},
	}

	proj, err := client.processNoteEvent(context.Background(), mockMREvent)
	if err != nil {
		t.Fatal(err)
	}
	if proj != "zapier/service-tf-buddy" {
		t.Fatal("expected a project name to be returned")
	}
}
