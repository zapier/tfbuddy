package tfc_hooks

import (
	"context"
	"errors"
	"testing"

	"github.com/hashicorp/go-tfe"
	"github.com/stretchr/testify/assert"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"go.uber.org/mock/gomock"
)

func TestNotificationHandler_pollingStreamCallback(t *testing.T) {
	tests := []struct {
		name       string
		setupMocks func(*mocks.MockApiClient, *mocks.MockStreamClient, *mocks.MockRunPollingTask)
		wantResult bool
	}{
		{
			name: "Successful status change - running status",
			setupMocks: func(mockAPI *mocks.MockApiClient, mockStream *mocks.MockStreamClient, mockTask *mocks.MockRunPollingTask) {
				mockTask.EXPECT().GetContext().Return(context.Background())
				mockTask.EXPECT().GetRunID().Return("run-test").AnyTimes()
				mockAPI.EXPECT().GetRun(gomock.Any(), "run-test").Return(&tfe.Run{
					ID:     "run-test",
					Status: tfe.RunPlanning,
					Workspace: &tfe.Workspace{
						Name: "test-workspace",
						Organization: &tfe.Organization{
							Name: "test-org",
						},
					},
				}, nil)
				mockTask.EXPECT().GetLastStatus().Return("pending").AnyTimes()
				mockStream.EXPECT().PublishTFRunEvent(gomock.Any(), &runstream.TFRunEvent{
					Organization: "test-org",
					Workspace:    "test-workspace",
					RunID:        "run-test",
					NewStatus:    "planning",
				}).Return(nil)
				mockTask.EXPECT().SetLastStatus("planning")
				mockTask.EXPECT().Reschedule(gomock.Any()).Return(nil)
			},
			wantResult: true,
		},
		{
			name: "Successful status change - completed status",
			setupMocks: func(mockAPI *mocks.MockApiClient, mockStream *mocks.MockStreamClient, mockTask *mocks.MockRunPollingTask) {
				mockTask.EXPECT().GetContext().Return(context.Background())
				mockTask.EXPECT().GetRunID().Return("run-test").AnyTimes()
				mockAPI.EXPECT().GetRun(gomock.Any(), "run-test").Return(&tfe.Run{
					ID:     "run-test",
					Status: tfe.RunApplied,
					Workspace: &tfe.Workspace{
						Name: "test-workspace",
						Organization: &tfe.Organization{
							Name: "test-org",
						},
					},
				}, nil)
				mockTask.EXPECT().GetLastStatus().Return("planning").AnyTimes()
				mockStream.EXPECT().PublishTFRunEvent(gomock.Any(), gomock.Any()).Return(nil)
				mockTask.EXPECT().Completed().Return(nil)
			},
			wantResult: true,
		},
		{
			name: "API error when getting run",
			setupMocks: func(mockAPI *mocks.MockApiClient, mockStream *mocks.MockStreamClient, mockTask *mocks.MockRunPollingTask) {
				mockTask.EXPECT().GetContext().Return(context.Background())
				mockTask.EXPECT().GetRunID().Return("run-test").AnyTimes()
				mockAPI.EXPECT().GetRun(gomock.Any(), "run-test").Return(nil, errors.New("API error"))
			},
			wantResult: false,
		},
		{
			name: "No status change - still running",
			setupMocks: func(mockAPI *mocks.MockApiClient, mockStream *mocks.MockStreamClient, mockTask *mocks.MockRunPollingTask) {
				mockTask.EXPECT().GetContext().Return(context.Background())
				mockTask.EXPECT().GetRunID().Return("run-test").AnyTimes()
				mockAPI.EXPECT().GetRun(gomock.Any(), "run-test").Return(&tfe.Run{
					ID:     "run-test",
					Status: tfe.RunPlanning,
					Workspace: &tfe.Workspace{
						Name: "test-workspace",
						Organization: &tfe.Organization{
							Name: "test-org",
						},
					},
				}, nil)
				mockTask.EXPECT().GetLastStatus().Return("planning").AnyTimes()
				mockTask.EXPECT().SetLastStatus("planning")
				mockTask.EXPECT().Reschedule(gomock.Any()).Return(nil)
			},
			wantResult: true,
		},
		{
			name: "No status change - completed",
			setupMocks: func(mockAPI *mocks.MockApiClient, mockStream *mocks.MockStreamClient, mockTask *mocks.MockRunPollingTask) {
				mockTask.EXPECT().GetContext().Return(context.Background())
				mockTask.EXPECT().GetRunID().Return("run-test").AnyTimes()
				mockAPI.EXPECT().GetRun(gomock.Any(), "run-test").Return(&tfe.Run{
					ID:     "run-test",
					Status: tfe.RunApplied,
					Workspace: &tfe.Workspace{
						Name: "test-workspace",
						Organization: &tfe.Organization{
							Name: "test-org",
						},
					},
				}, nil)
				mockTask.EXPECT().GetLastStatus().Return("applied").AnyTimes()
				mockTask.EXPECT().Completed().Return(nil)
			},
			wantResult: true,
		},
		{
			name: "Publish event error",
			setupMocks: func(mockAPI *mocks.MockApiClient, mockStream *mocks.MockStreamClient, mockTask *mocks.MockRunPollingTask) {
				mockTask.EXPECT().GetContext().Return(context.Background())
				mockTask.EXPECT().GetRunID().Return("run-test").AnyTimes()
				mockAPI.EXPECT().GetRun(gomock.Any(), "run-test").Return(&tfe.Run{
					ID:     "run-test",
					Status: tfe.RunPlanning,
					Workspace: &tfe.Workspace{
						Name: "test-workspace",
						Organization: &tfe.Organization{
							Name: "test-org",
						},
					},
				}, nil)
				mockTask.EXPECT().GetLastStatus().Return("pending").AnyTimes()
				mockStream.EXPECT().PublishTFRunEvent(gomock.Any(), gomock.Any()).Return(errors.New("publish error"))
			},
			wantResult: false,
		},
		{
			name: "Reschedule error - still returns true",
			setupMocks: func(mockAPI *mocks.MockApiClient, mockStream *mocks.MockStreamClient, mockTask *mocks.MockRunPollingTask) {
				mockTask.EXPECT().GetContext().Return(context.Background())
				mockTask.EXPECT().GetRunID().Return("run-test").AnyTimes()
				mockAPI.EXPECT().GetRun(gomock.Any(), "run-test").Return(&tfe.Run{
					ID:     "run-test",
					Status: tfe.RunPlanning,
					Workspace: &tfe.Workspace{
						Name: "test-workspace",
						Organization: &tfe.Organization{
							Name: "test-org",
						},
					},
				}, nil)
				mockTask.EXPECT().GetLastStatus().Return("planning").AnyTimes()
				mockTask.EXPECT().SetLastStatus("planning")
				mockTask.EXPECT().Reschedule(gomock.Any()).Return(errors.New("reschedule error"))
			},
			wantResult: true,
		},
		{
			name: "Completed error - still returns true",
			setupMocks: func(mockAPI *mocks.MockApiClient, mockStream *mocks.MockStreamClient, mockTask *mocks.MockRunPollingTask) {
				mockTask.EXPECT().GetContext().Return(context.Background())
				mockTask.EXPECT().GetRunID().Return("run-test").AnyTimes()
				mockAPI.EXPECT().GetRun(gomock.Any(), "run-test").Return(&tfe.Run{
					ID:     "run-test",
					Status: tfe.RunApplied,
					Workspace: &tfe.Workspace{
						Name: "test-workspace",
						Organization: &tfe.Organization{
							Name: "test-org",
						},
					},
				}, nil)
				mockTask.EXPECT().GetLastStatus().Return("applied").AnyTimes()
				mockTask.EXPECT().Completed().Return(errors.New("completion error"))
			},
			wantResult: true,
		},
		{
			name: "Nil workspace - handled gracefully",
			setupMocks: func(mockAPI *mocks.MockApiClient, mockStream *mocks.MockStreamClient, mockTask *mocks.MockRunPollingTask) {
				mockTask.EXPECT().GetContext().Return(context.Background())
				mockTask.EXPECT().GetRunID().Return("run-test").AnyTimes()
				mockAPI.EXPECT().GetRun(gomock.Any(), "run-test").Return(&tfe.Run{
					ID:        "run-test",
					Status:    tfe.RunPlanning,
					Workspace: nil,
				}, nil)
				mockTask.EXPECT().GetLastStatus().Return("pending").AnyTimes()
			},
			wantResult: false, // Should return false due to nil workspace
		},
		{
			name: "Nil organization - handled gracefully",
			setupMocks: func(mockAPI *mocks.MockApiClient, mockStream *mocks.MockStreamClient, mockTask *mocks.MockRunPollingTask) {
				mockTask.EXPECT().GetContext().Return(context.Background())
				mockTask.EXPECT().GetRunID().Return("run-test").AnyTimes()
				mockAPI.EXPECT().GetRun(gomock.Any(), "run-test").Return(&tfe.Run{
					ID:     "run-test",
					Status: tfe.RunPlanning,
					Workspace: &tfe.Workspace{
						Name:         "test-workspace",
						Organization: nil,
					},
				}, nil)
				mockTask.EXPECT().GetLastStatus().Return("pending").AnyTimes()
			},
			wantResult: false, // Should return false due to nil organization
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockAPI := mocks.NewMockApiClient(ctrl)
			mockStream := mocks.NewMockStreamClient(ctrl)
			mockTask := mocks.NewMockRunPollingTask(ctrl)

			tt.setupMocks(mockAPI, mockStream, mockTask)

			handler := &NotificationHandler{
				api:    mockAPI,
				stream: mockStream,
			}

			result := handler.pollingStreamCallback(mockTask)
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func Test_isRunning(t *testing.T) {
	tests := []struct {
		name string
		run  *tfe.Run
		want bool
	}{
		{
			name: "Nil run",
			run:  nil,
			want: false,
		},
		{
			name: "Pending status",
			run:  &tfe.Run{Status: tfe.RunStatus("pending")},
			want: true,
		},
		{
			name: "Planning status",
			run:  &tfe.Run{Status: tfe.RunStatus("planning")},
			want: true,
		},
		{
			name: "Plan queued status",
			run:  &tfe.Run{Status: tfe.RunStatus("plan_queued")},
			want: true,
		},
		{
			name: "Applying status",
			run:  &tfe.Run{Status: tfe.RunStatus("applying")},
			want: true,
		},
		{
			name: "Apply queued status",
			run:  &tfe.Run{Status: tfe.RunStatus("apply_queued")},
			want: true,
		},
		{
			name: "Cost estimating status",
			run:  &tfe.Run{Status: tfe.RunStatus("cost_estimating")},
			want: true,
		},
		{
			name: "Policy checking status",
			run:  &tfe.Run{Status: tfe.RunStatus("policy_checking")},
			want: true,
		},
		{
			name: "Applied status",
			run:  &tfe.Run{Status: tfe.RunApplied},
			want: false,
		},
		{
			name: "Errored status",
			run:  &tfe.Run{Status: tfe.RunErrored},
			want: false,
		},
		{
			name: "Planned and finished status",
			run:  &tfe.Run{Status: tfe.RunPlannedAndFinished},
			want: false,
		},
		{
			name: "Canceled status",
			run:  &tfe.Run{Status: tfe.RunCanceled},
			want: false,
		},
		{
			name: "Discarded status",
			run:  &tfe.Run{Status: tfe.RunDiscarded},
			want: false,
		},
		{
			name: "Unknown status",
			run:  &tfe.Run{Status: tfe.RunStatus("unknown_status")},
			want: false,
		},
		{
			name: "Empty status",
			run:  &tfe.Run{Status: tfe.RunStatus("")},
			want: false,
		},
		{
			name: "Policy override status",
			run:  &tfe.Run{Status: tfe.RunPolicyOverride},
			want: false,
		},
		{
			name: "Policy checked status",
			run:  &tfe.Run{Status: tfe.RunPolicyChecked},
			want: false,
		},
		{
			name: "Policy soft failed status",
			run:  &tfe.Run{Status: tfe.RunPolicySoftFailed},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRunning(tt.run)
			assert.Equal(t, tt.want, result)
		})
	}
}
