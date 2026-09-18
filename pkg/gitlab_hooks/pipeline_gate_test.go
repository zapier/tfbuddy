package gitlab_hooks

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.uber.org/mock/gomock"
)

const (
	gateProject   = "zapier/service-tf-buddy"
	gateCommitSHA = "abvc12345"
	gateMRIID     = 101
)

// fakePipeline is a vcs.ProjectPipeline built from literals so tests describe
// GitLab's response without going through the gogitlab structs.
type fakePipeline struct {
	id     int
	source string
	status string
}

func (f fakePipeline) GetID() int        { return f.id }
func (f fakePipeline) GetSource() string { return f.source }
func (f fakePipeline) GetStatus() string { return f.status }

type fakeJobStatus struct {
	name         string
	status       string
	pipelineID   int
	allowFailure bool
}

func (f fakeJobStatus) GetName() string       { return f.name }
func (f fakeJobStatus) GetStatus() string     { return f.status }
func (f fakeJobStatus) GetPipelineID() int    { return f.pipelineID }
func (f fakeJobStatus) GetAllowFailure() bool { return f.allowFailure }

type fakeProjectSettings struct{ onlyAllowMergeIfPipelineSucceeds bool }

func (f fakeProjectSettings) OnlyAllowMergeIfPipelineSucceeds() bool {
	return f.onlyAllowMergeIfPipelineSucceeds
}

// gateFixture wires a GitlabEventWorker against mocks for the pipeline gate.
type gateFixture struct {
	worker *GitlabEventWorker
	event  vcs.MRCommentEvent
	client *mocks.MockGitClient
}

func newGateFixture(t *testing.T, ctrl *gomock.Controller, requirePipelineSuccess bool) *gateFixture {
	t.Helper()

	client := mocks.NewMockGitClient(ctrl)

	project := mocks.NewMockProject(ctrl)
	project.EXPECT().GetPathWithNamespace().Return(gateProject).AnyTimes()

	mr := mocks.NewMockMR(ctrl)
	mr.EXPECT().GetInternalID().Return(gateMRIID).AnyTimes()

	event := mocks.NewMockMRCommentEvent(ctrl)
	event.EXPECT().GetProject().Return(project).AnyTimes()
	event.EXPECT().GetMR().Return(mr).AnyTimes()

	cfg := config.C
	cfg.RequirePipelineSuccess = requirePipelineSuccess

	return &gateFixture{
		worker: &GitlabEventWorker{cfg: cfg, gl: client},
		event:  event,
		client: client,
	}
}

func TestCheckPipelineStatus(t *testing.T) {
	mrPipeline := fakePipeline{id: 900, source: vcs.PipelineSourceMergeRequestEvent, status: "running"}

	tests := []struct {
		name string
		// requirePipelineSuccess is the TFBuddy config flag under test.
		requirePipelineSuccess bool
		// onlyAllowMergeIfPipelineSucceeds is the GitLab project setting that must
		// also be on for the gate to apply.
		onlyAllowMergeIfPipelineSucceeds bool
		projectSettingsErr               error
		pipelines                        []vcs.ProjectPipeline
		pipelinesErr                     error
		statuses                         []vcs.CommitJobStatus
		statusesErr                      error
		want                             bool
		// wantComment is the message the gate must post to the MR, or "" when it
		// must stay silent.
		wantComment string
	}{
		{
			name:                   "flag disabled allows apply without calling the API",
			requirePipelineSuccess: false,
			want:                   true,
		},
		{
			name:                             "all jobs succeeded",
			requirePipelineSuccess:           true,
			onlyAllowMergeIfPipelineSucceeds: true,
			pipelines:                        []vcs.ProjectPipeline{mrPipeline},
			statuses: []vcs.CommitJobStatus{
				fakeJobStatus{name: "build", status: "success", pipelineID: 900},
				fakeJobStatus{name: "lint", status: "success", pipelineID: 900},
			},
			want: true,
		},
		{
			name:                             "failed job blocks apply",
			requirePipelineSuccess:           true,
			onlyAllowMergeIfPipelineSucceeds: true,
			pipelines:                        []vcs.ProjectPipeline{mrPipeline},
			statuses: []vcs.CommitJobStatus{
				fakeJobStatus{name: "build", status: "success", pipelineID: 900},
				fakeJobStatus{name: "lint", status: "failed", pipelineID: 900},
			},
			want:        false,
			wantComment: ":no_entry: Apply failed. Pipeline 900 (running) has not succeeded (lint: failed).",
		},
		{
			name:                             "running job blocks apply",
			requirePipelineSuccess:           true,
			onlyAllowMergeIfPipelineSucceeds: true,
			pipelines:                        []vcs.ProjectPipeline{mrPipeline},
			statuses: []vcs.CommitJobStatus{
				fakeJobStatus{name: "build", status: "running", pipelineID: 900},
			},
			want:        false,
			wantComment: ":no_entry: Apply failed. Pipeline 900 (running) has not succeeded (build: running).",
		},
		{
			name:                             "pending job blocks apply",
			requirePipelineSuccess:           true,
			onlyAllowMergeIfPipelineSucceeds: true,
			pipelines:                        []vcs.ProjectPipeline{mrPipeline},
			statuses: []vcs.CommitJobStatus{
				fakeJobStatus{name: "build", status: "pending", pipelineID: 900},
			},
			want:        false,
			wantComment: ":no_entry: Apply failed. Pipeline 900 (running) has not succeeded (build: pending).",
		},
		{
			name:                             "canceled job blocks apply",
			requirePipelineSuccess:           true,
			onlyAllowMergeIfPipelineSucceeds: true,
			pipelines:                        []vcs.ProjectPipeline{mrPipeline},
			statuses: []vcs.CommitJobStatus{
				fakeJobStatus{name: "build", status: "canceled", pipelineID: 900},
			},
			want:        false,
			wantComment: ":no_entry: Apply failed. Pipeline 900 (running) has not succeeded (build: canceled).",
		},
		{
			// The deadlock regression: TFBuddy's own pending apply status lives in
			// the merge request pipeline, so it must never block the apply that
			// would clear it.
			name:                             "only tfbuddy statuses pending allows apply",
			requirePipelineSuccess:           true,
			onlyAllowMergeIfPipelineSucceeds: true,
			pipelines:                        []vcs.ProjectPipeline{mrPipeline},
			statuses: []vcs.CommitJobStatus{
				fakeJobStatus{name: "TFC/plan/service-tfbuddy", status: "success", pipelineID: 900},
				fakeJobStatus{name: "TFC/apply/service-tfbuddy", status: "pending", pipelineID: 900},
			},
			want: true,
		},
		{
			name:                             "allow_failure job does not block apply",
			requirePipelineSuccess:           true,
			onlyAllowMergeIfPipelineSucceeds: true,
			pipelines:                        []vcs.ProjectPipeline{mrPipeline},
			statuses: []vcs.CommitJobStatus{
				fakeJobStatus{name: "flaky", status: "failed", pipelineID: 900, allowFailure: true},
			},
			want: true,
		},
		{
			name:                             "skipped and manual jobs do not block apply",
			requirePipelineSuccess:           true,
			onlyAllowMergeIfPipelineSucceeds: true,
			pipelines:                        []vcs.ProjectPipeline{mrPipeline},
			statuses: []vcs.CommitJobStatus{
				fakeJobStatus{name: "optional-deploy", status: "manual", pipelineID: 900},
				fakeJobStatus{name: "skipped-job", status: "skipped", pipelineID: 900},
			},
			want: true,
		},
		{
			name:                             "failed job in another pipeline does not block apply",
			requirePipelineSuccess:           true,
			onlyAllowMergeIfPipelineSucceeds: true,
			pipelines:                        []vcs.ProjectPipeline{mrPipeline},
			statuses: []vcs.CommitJobStatus{
				fakeJobStatus{name: "build", status: "success", pipelineID: 900},
				fakeJobStatus{name: "stale", status: "failed", pipelineID: 42},
			},
			want: true,
		},
		{
			// A commit with no CI has only the external pipeline TFBuddy created
			// for its own statuses. That must not gate the apply.
			name:                             "only external tfbuddy pipeline allows apply",
			requirePipelineSuccess:           true,
			onlyAllowMergeIfPipelineSucceeds: true,
			pipelines: []vcs.ProjectPipeline{
				fakePipeline{id: 901, source: vcs.PipelineSourceExternal, status: "pending"},
			},
			want: true,
		},
		{
			name:                             "no pipelines allows apply",
			requirePipelineSuccess:           true,
			onlyAllowMergeIfPipelineSucceeds: true,
			pipelines:                        []vcs.ProjectPipeline{},
			want:                             true,
		},
		{
			name:                             "non merge request pipeline is used when there is no MR pipeline",
			requirePipelineSuccess:           true,
			onlyAllowMergeIfPipelineSucceeds: true,
			pipelines: []vcs.ProjectPipeline{
				fakePipeline{id: 800, source: "push", status: "failed"},
			},
			statuses: []vcs.CommitJobStatus{
				fakeJobStatus{name: "build", status: "failed", pipelineID: 800},
			},
			want:        false,
			wantComment: ":no_entry: Apply failed. Pipeline 800 (failed) has not succeeded (build: failed).",
		},
		{
			// GitLab already knows whether a red pipeline should stop a merge.
			// TFBuddy defers to that rather than second-guessing it.
			name:                             "project does not require pipeline success allows apply",
			requirePipelineSuccess:           true,
			onlyAllowMergeIfPipelineSucceeds: false,
			pipelines:                        []vcs.ProjectPipeline{mrPipeline},
			statuses: []vcs.CommitJobStatus{
				fakeJobStatus{name: "lint", status: "failed", pipelineID: 900},
			},
			want: true,
		},
		{
			name:                             "project settings lookup error blocks apply",
			requirePipelineSuccess:           true,
			onlyAllowMergeIfPipelineSucceeds: true,
			projectSettingsErr:               fmt.Errorf("gitlab exploded"),
			want:                             false,
			wantComment:                      ":fire: <br> Error: could not get project settings from GitlabAPI: gitlab exploded",
		},
		{
			name:                             "pipeline lookup error blocks apply",
			requirePipelineSuccess:           true,
			onlyAllowMergeIfPipelineSucceeds: true,
			pipelinesErr:                     fmt.Errorf("gitlab exploded"),
			want:                             false,
			wantComment:                      ":fire: <br> Error: could not get pipelines for commit from GitlabAPI: gitlab exploded",
		},
		{
			name:                             "commit status lookup error blocks apply",
			requirePipelineSuccess:           true,
			onlyAllowMergeIfPipelineSucceeds: true,
			pipelines:                        []vcs.ProjectPipeline{mrPipeline},
			statusesErr:                      fmt.Errorf("gitlab exploded"),
			want:                             false,
			wantComment:                      ":fire: <br> Error: could not get commit statuses from GitlabAPI: gitlab exploded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			f := newGateFixture(t, ctrl, tt.requirePipelineSuccess)

			if tt.requirePipelineSuccess {
				f.client.EXPECT().
					GetProjectSettings(gomock.Any(), gateProject).
					Return(fakeProjectSettings{tt.onlyAllowMergeIfPipelineSucceeds}, tt.projectSettingsErr).
					AnyTimes()
				f.client.EXPECT().
					GetPipelinesForCommit(gomock.Any(), gateProject, gateCommitSHA).
					Return(tt.pipelines, tt.pipelinesErr).
					AnyTimes()
				f.client.EXPECT().
					GetCommitJobStatuses(gomock.Any(), gateProject, gateCommitSHA).
					Return(tt.statuses, tt.statusesErr).
					AnyTimes()
			}
			if tt.wantComment != "" {
				f.client.EXPECT().
					CreateMergeRequestComment(gomock.Any(), gateMRIID, gateProject, tt.wantComment).
					Return(nil)
			}

			got := f.worker.checkPipelineStatus(context.Background(), f.event, gateCommitSHA)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestProcessNoteEventApplyBlockedByPipeline checks the gate is actually wired
// into the apply command path, and that a blocked apply never reaches TFC.
func TestProcessNoteEventApplyBlockedByPipeline(t *testing.T) {
	t.Setenv("TFBUDDY_GITLAB_PROJECT_ALLOW_LIST", "zapier/")
	t.Setenv("TFBUDDY_REQUIRE_PIPELINE_SUCCESS", "true")
	config.Reload()
	defer config.Reload()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockGitClient := mocks.NewMockGitClient(ctrl)

	approvals := mocks.NewMockMRApproved(ctrl)
	approvals.EXPECT().IsApproved().Return(true)
	mockGitClient.EXPECT().GetMergeRequestApprovals(gomock.Any(), gateMRIID, gateProject).Return(approvals, nil)

	detailedMR := mocks.NewMockDetailedMR(ctrl)
	detailedMR.EXPECT().HasConflicts().Return(false)
	mockGitClient.EXPECT().GetMergeRequest(gomock.Any(), gateMRIID, gateProject).Return(detailedMR, nil)

	mockGitClient.EXPECT().
		GetProjectSettings(gomock.Any(), gateProject).
		Return(fakeProjectSettings{onlyAllowMergeIfPipelineSucceeds: true}, nil)
	mockGitClient.EXPECT().
		GetPipelinesForCommit(gomock.Any(), gateProject, gateCommitSHA).
		Return([]vcs.ProjectPipeline{
			fakePipeline{id: 900, source: vcs.PipelineSourceMergeRequestEvent, status: "failed"},
		}, nil)
	mockGitClient.EXPECT().
		GetCommitJobStatuses(gomock.Any(), gateProject, gateCommitSHA).
		Return([]vcs.CommitJobStatus{
			fakeJobStatus{name: "lint", status: "failed", pipelineID: 900},
		}, nil)
	mockGitClient.EXPECT().
		CreateMergeRequestComment(gomock.Any(), gateMRIID, gateProject,
			":no_entry: Apply failed. Pipeline 900 (failed) has not succeeded (lint: failed).").
		Return(nil)

	project := mocks.NewMockProject(ctrl)
	project.EXPECT().GetPathWithNamespace().Return(gateProject).AnyTimes()

	lastCommit := mocks.NewMockCommit(ctrl)
	lastCommit.EXPECT().GetSHA().Return(gateCommitSHA)

	attributes := mocks.NewMockMRAttributes(ctrl)
	attributes.EXPECT().GetNote().Return("tfc apply -w service-tf-buddy")
	attributes.EXPECT().GetType().Return("SomeNote")

	mr := mocks.NewMockMR(ctrl)
	mr.EXPECT().GetSourceBranch().Return("DTA-2009")
	mr.EXPECT().GetInternalID().Return(gateMRIID).AnyTimes()

	event := mocks.NewMockMRCommentEvent(ctrl)
	event.EXPECT().GetProject().Return(project).AnyTimes()
	event.EXPECT().GetAttributes().Return(attributes).Times(2)
	event.EXPECT().GetLastCommit().Return(lastCommit)
	event.EXPECT().GetMR().Return(mr).AnyTimes()

	// No TriggerTFCEvents expectation: a blocked apply must not reach TFC.
	trigger := mocks.NewMockTrigger(ctrl)

	worker := &GitlabEventWorker{
		cfg:       config.C,
		gl:        mockGitClient,
		tfc:       mocks.NewMockApiClient(ctrl),
		runstream: mocks.NewMockStreamClient(ctrl),
		triggerCreation: func(appCfg config.Config, gl vcs.GitClient, tfc tfc_api.ApiClient, rs runstream.StreamClient, cfg *tfc_trigger.TFCTriggerOptions) tfc_trigger.Trigger {
			return trigger
		},
	}

	proj, err := worker.processNoteEvent(context.Background(), event)
	assert.NoError(t, err)
	assert.Equal(t, gateProject, proj)
}
