package gitlab

import (
	"context"
	"errors"
	"testing"

	"github.com/hashicorp/go-tfe"
	"github.com/stretchr/testify/assert"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/vcs"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
	"go.uber.org/mock/gomock"
)

func TestUpdateCommitStatusForRun(t *testing.T) {
	tests := []struct {
		name          string
		run           *tfe.Run
		metadata      *runstream.TFRunMetadata
		expectMerge   bool
		statusUpdates map[string]gogitlab.BuildStateValue
	}{
		{
			name: "pending run with plan action",
			run: &tfe.Run{
				Status: tfe.RunPending,
			},
			metadata: &runstream.TFRunMetadata{
				Action: runstream.PlanAction,
			},
			expectMerge: false,
			statusUpdates: map[string]gogitlab.BuildStateValue{
				"plan":  gogitlab.Pending,
				"apply": gogitlab.Failed,
			},
		},
		{
			name: "pending run with apply action",
			run: &tfe.Run{
				Status: tfe.RunPending,
			},
			metadata: &runstream.TFRunMetadata{
				Action: runstream.ApplyAction,
			},
			expectMerge: false,
			statusUpdates: map[string]gogitlab.BuildStateValue{
				"apply": gogitlab.Pending,
			},
		},
		{
			name: "apply queued",
			run: &tfe.Run{
				Status: tfe.RunApplyQueued,
			},
			metadata:    &runstream.TFRunMetadata{},
			expectMerge: false,
			statusUpdates: map[string]gogitlab.BuildStateValue{
				"apply": gogitlab.Pending,
			},
		},
		{
			name: "applying",
			run: &tfe.Run{
				Status: tfe.RunApplying,
			},
			metadata:    &runstream.TFRunMetadata{},
			expectMerge: false,
			statusUpdates: map[string]gogitlab.BuildStateValue{
				"apply": gogitlab.Running,
			},
		},
		{
			name: "applied with no target addresses and auto-merge",
			run: &tfe.Run{
				Status:      tfe.RunApplied,
				TargetAddrs: []string{},
			},
			metadata: &runstream.TFRunMetadata{
				AutoMerge: true,
			},
			expectMerge: true,
			statusUpdates: map[string]gogitlab.BuildStateValue{
				"apply": gogitlab.Success,
			},
		},
		{
			name: "applied with target addresses",
			run: &tfe.Run{
				Status:      tfe.RunApplied,
				TargetAddrs: []string{"module.foo"},
			},
			metadata: &runstream.TFRunMetadata{
				AutoMerge: true,
			},
			expectMerge: false,
			statusUpdates: map[string]gogitlab.BuildStateValue{
				"apply": gogitlab.Pending,
			},
		},
		{
			name: "canceled",
			run: &tfe.Run{
				Status: tfe.RunCanceled,
			},
			metadata: &runstream.TFRunMetadata{
				Action: runstream.PlanAction,
			},
			expectMerge: false,
			statusUpdates: map[string]gogitlab.BuildStateValue{
				"plan": gogitlab.Failed,
			},
		},
		{
			name: "discarded",
			run: &tfe.Run{
				Status: tfe.RunDiscarded,
			},
			metadata:    &runstream.TFRunMetadata{},
			expectMerge: false,
			statusUpdates: map[string]gogitlab.BuildStateValue{
				"plan":  gogitlab.Failed,
				"apply": gogitlab.Failed,
			},
		},
		{
			name: "errored",
			run: &tfe.Run{
				Status: tfe.RunErrored,
			},
			metadata: &runstream.TFRunMetadata{
				Action: runstream.ApplyAction,
			},
			expectMerge: false,
			statusUpdates: map[string]gogitlab.BuildStateValue{
				"apply": gogitlab.Failed,
			},
		},
		{
			name: "planning",
			run: &tfe.Run{
				Status: tfe.RunPlanning,
			},
			metadata: &runstream.TFRunMetadata{
				Action: runstream.PlanAction,
			},
			expectMerge: false,
			statusUpdates: map[string]gogitlab.BuildStateValue{
				"plan": gogitlab.Running,
			},
		},
		{
			name: "planned (should not update)",
			run: &tfe.Run{
				Status: tfe.RunPlanned,
			},
			metadata:      &runstream.TFRunMetadata{},
			expectMerge:   false,
			statusUpdates: map[string]gogitlab.BuildStateValue{},
		},
		{
			name: "planned and finished with changes",
			run: &tfe.Run{
				Status:     tfe.RunPlannedAndFinished,
				HasChanges: true,
			},
			metadata: &runstream.TFRunMetadata{
				Action: runstream.PlanAction,
			},
			expectMerge: false,
			statusUpdates: map[string]gogitlab.BuildStateValue{
				"plan":  gogitlab.Success,
				"apply": gogitlab.Pending,
			},
		},
		{
			name: "planned and finished no changes apply action with auto-merge",
			run: &tfe.Run{
				Status:      tfe.RunPlannedAndFinished,
				HasChanges:  false,
				TargetAddrs: []string{},
			},
			metadata: &runstream.TFRunMetadata{
				Action:    runstream.ApplyAction,
				AutoMerge: true,
			},
			expectMerge: true,
			statusUpdates: map[string]gogitlab.BuildStateValue{
				"apply": gogitlab.Success,
			},
		},
		{
			name: "policy soft failed",
			run: &tfe.Run{
				Status: tfe.RunPolicySoftFailed,
			},
			metadata: &runstream.TFRunMetadata{
				Action: runstream.PlanAction,
			},
			expectMerge: false,
			statusUpdates: map[string]gogitlab.BuildStateValue{
				"plan": gogitlab.Success,
			},
		},
		{
			name: "policy checked (no op)",
			run: &tfe.Run{
				Status: tfe.RunPolicyChecked,
			},
			metadata:      &runstream.TFRunMetadata{},
			expectMerge:   false,
			statusUpdates: map[string]gogitlab.BuildStateValue{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			mockClient := mocks.NewMockGitClient(mockCtrl)

			for action, state := range tt.statusUpdates {
				mockClient.EXPECT().
					GetPipelinesForCommit(gomock.Any(), gomock.Any(), gomock.Any()).
					Return([]vcs.ProjectPipeline{&GitlabPipeline{&gogitlab.PipelineInfo{ID: 1}}}, nil)

				mockClient.EXPECT().
					SetCommitStatus(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(ctx context.Context, projectWithNS string, commitSHA string, status vcs.CommitStatusOptions) (vcs.CommitStatus, error) {
						assert.Contains(t, status.GetName(), action)
						assert.Equal(t, string(state), status.GetState())
						return &GitlabCommitStatus{&gogitlab.CommitStatus{}}, nil
					})
			}

			if tt.expectMerge {
				mockClient.EXPECT().
					MergeMR(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(nil)
			}

			updater := &RunStatusUpdater{
				client: mockClient,
			}

			updater.updateCommitStatusForRun(context.Background(), tt.run, tt.metadata)
		})
	}
}

func TestUpdateStatus(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	tests := []struct {
		name               string
		state              gogitlab.BuildStateValue
		action             string
		metadata           *runstream.TFRunMetadata
		pipelineID         *int
		pipelineError      error
		expectPipelineCall bool
	}{
		{
			name:   "success with pipeline",
			state:  gogitlab.Success,
			action: "plan",
			metadata: &runstream.TFRunMetadata{
				Workspace:                            "workspace1",
				MergeRequestProjectNameWithNamespace: "group/project",
				CommitSHA:                            "abc123",
				Organization:                         "org1",
				RunID:                                "run-123",
			},
			pipelineID:         ptr(123),
			pipelineError:      nil,
			expectPipelineCall: true,
		},
		{
			name:   "failure without pipeline",
			state:  gogitlab.Failed,
			action: "apply",
			metadata: &runstream.TFRunMetadata{
				Workspace:                            "workspace2",
				MergeRequestProjectNameWithNamespace: "group/project",
				CommitSHA:                            "def456",
				Organization:                         "org2",
				RunID:                                "run-456",
			},
			pipelineID:         nil,
			pipelineError:      errors.New("pipeline not found"),
			expectPipelineCall: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := mocks.NewMockGitClient(mockCtrl)

			if tt.expectPipelineCall {
				if tt.pipelineError != nil {
					mockClient.EXPECT().
						GetPipelinesForCommit(gomock.Any(), tt.metadata.GetMRProjectNameWithNamespace(), tt.metadata.GetCommitSHA()).
						Return(nil, tt.pipelineError).AnyTimes()
				} else if tt.pipelineID != nil {
					mockClient.EXPECT().
						GetPipelinesForCommit(gomock.Any(), tt.metadata.GetMRProjectNameWithNamespace(), tt.metadata.GetCommitSHA()).
						Return([]vcs.ProjectPipeline{&GitlabPipeline{&gogitlab.PipelineInfo{ID: *tt.pipelineID, Source: "merge_request_event"}}}, nil).AnyTimes()
				}
			}

			mockClient.EXPECT().
				SetCommitStatus(gomock.Any(), tt.metadata.GetMRProjectNameWithNamespace(), tt.metadata.GetCommitSHA(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, projectWithNS string, commitSHA string, status vcs.CommitStatusOptions) (vcs.CommitStatus, error) {
					assert.Equal(t, "TFC/"+tt.action+"/"+tt.metadata.GetWorkspace(), status.GetName())
					assert.Equal(t, string(tt.state), status.GetState())
					if tt.pipelineID != nil && tt.pipelineError == nil {
						if gitlabStatus, ok := status.(*GitlabCommitStatusOptions); ok {
							if gitlabStatus.PipelineID != nil {
								assert.Equal(t, *tt.pipelineID, *gitlabStatus.PipelineID)
							}
						}
					}
					return &GitlabCommitStatus{&gogitlab.CommitStatus{}}, nil
				})

			updater := &RunStatusUpdater{
				client: mockClient,
			}

			updater.updateStatus(context.Background(), tt.state, tt.action, tt.metadata)
		})
	}
}

func TestGetLatestPipelineID(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	tests := []struct {
		name        string
		pipelines   []vcs.ProjectPipeline
		expectedID  *int
		expectError bool
	}{
		{
			name: "merge request pipeline found",
			pipelines: []vcs.ProjectPipeline{
				&GitlabPipeline{&gogitlab.PipelineInfo{ID: 1, Source: "push"}},
				&GitlabPipeline{&gogitlab.PipelineInfo{ID: 2, Source: "merge_request_event"}},
				&GitlabPipeline{&gogitlab.PipelineInfo{ID: 3, Source: "api"}},
			},
			expectedID: ptr(2),
		},
		{
			name: "no merge request pipeline - use latest",
			pipelines: []vcs.ProjectPipeline{
				&GitlabPipeline{&gogitlab.PipelineInfo{ID: 1, Source: "push"}},
				&GitlabPipeline{&gogitlab.PipelineInfo{ID: 2, Source: "api"}},
				&GitlabPipeline{&gogitlab.PipelineInfo{ID: 3, Source: "web"}},
			},
			expectedID: ptr(3),
		},
		{
			name:       "no pipelines",
			pipelines:  []vcs.ProjectPipeline{},
			expectedID: nil,
		},
		{
			name:        "error getting pipelines",
			pipelines:   nil,
			expectedID:  nil,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := mocks.NewMockGitClient(mockCtrl)

			metadata := &runstream.TFRunMetadata{
				MergeRequestProjectNameWithNamespace: "group/project",
				CommitSHA:                            "abc123",
			}

			if tt.expectError {
				mockClient.EXPECT().
					GetPipelinesForCommit(gomock.Any(), metadata.GetMRProjectNameWithNamespace(), metadata.GetCommitSHA()).
					Return(nil, errors.New("API error"))
			} else {
				mockClient.EXPECT().
					GetPipelinesForCommit(gomock.Any(), metadata.GetMRProjectNameWithNamespace(), metadata.GetCommitSHA()).
					Return(tt.pipelines, nil)
			}

			updater := &RunStatusUpdater{
				client: mockClient,
			}

			result := updater.getLatestPipelineID(context.Background(), metadata)

			if tt.expectedID == nil {
				assert.Nil(t, result)
			} else {
				assert.NotNil(t, result)
				assert.Equal(t, *tt.expectedID, *result)
			}
		})
	}
}

func TestMergeMRIfPossible(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	tests := []struct {
		name        string
		autoMerge   bool
		mergeError  error
		expectMerge bool
	}{
		{
			name:        "auto-merge enabled and successful",
			autoMerge:   true,
			mergeError:  nil,
			expectMerge: true,
		},
		{
			name:        "auto-merge enabled with error",
			autoMerge:   true,
			mergeError:  errors.New("merge conflict"),
			expectMerge: true,
		},
		{
			name:        "auto-merge disabled",
			autoMerge:   false,
			mergeError:  nil,
			expectMerge: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := mocks.NewMockGitClient(mockCtrl)

			metadata := &runstream.TFRunMetadata{
				AutoMerge:                            tt.autoMerge,
				MergeRequestIID:                      123,
				MergeRequestProjectNameWithNamespace: "group/project",
			}

			if tt.expectMerge {
				mockClient.EXPECT().
					MergeMR(gomock.Any(), metadata.GetMRInternalID(), metadata.GetMRProjectNameWithNamespace()).
					Return(tt.mergeError)
			}

			updater := &RunStatusUpdater{
				client: mockClient,
			}

			updater.mergeMRIfPossible(context.Background(), metadata)
		})
	}
}
