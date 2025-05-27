package gitlab

import (
	"context"
	"errors"
	"testing"

	"github.com/hashicorp/go-tfe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"go.uber.org/mock/gomock"
)

func TestNewRunStatusProcessor(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockClient := &GitlabClient{}
	mockRS := mocks.NewMockStreamClient(mockCtrl)
	mockTFC := mocks.NewMockApiClient(mockCtrl)

	// Set up expectation for subscription
	closerFunc := func() {}
	mockRS.EXPECT().
		SubscribeTFRunEvents("gitlab", gomock.Any()).
		Return(closerFunc, nil)

	processor := NewRunStatusProcessor(mockClient, mockRS, mockTFC)

	assert.NotNil(t, processor)
	assert.Equal(t, mockClient, processor.client)
	assert.Equal(t, mockRS, processor.rs)
	assert.Equal(t, mockTFC, processor.tfc)
	assert.NotNil(t, processor.eventQCloser)
}

func TestRunStatusProcessorClose(t *testing.T) {
	closerCalled := false
	processor := &RunStatusUpdater{
		eventQCloser: func() {
			closerCalled = true
		},
	}

	processor.Close()
	assert.True(t, closerCalled)
}

func TestEventStreamCallback(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	tests := []struct {
		name           string
		runEvent       runstream.RunEvent
		run            *tfe.Run
		runError       error
		expectedResult bool
	}{
		{
			name: "error getting run",
			runEvent: func() runstream.RunEvent {
				event := &runstream.TFRunEvent{
					RunID:     "run-456",
					NewStatus: string(tfe.RunErrored),
					Metadata: &runstream.TFRunMetadata{
						RunID: "run-456",
					},
				}
				event.SetContext(context.Background())
				return event
			}(),
			run:            nil,
			runError:       errors.New("API error"),
			expectedResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := mocks.NewMockGitClient(mockCtrl)
			mockTFC := mocks.NewMockApiClient(mockCtrl)

			mockTFC.EXPECT().
				GetRun(gomock.Any(), tt.runEvent.GetRunID()).
				Return(tt.run, tt.runError)

			processor := &RunStatusUpdater{
				client: mockClient,
				tfc:    mockTFC,
			}

			result := processor.eventStreamCallback(tt.runEvent)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestTFRunEventInterface(t *testing.T) {
	ctx := context.Background()
	event := &runstream.TFRunEvent{
		RunID:     "run-123",
		NewStatus: "applied",
		Metadata: &runstream.TFRunMetadata{
			RunID: "run-123",
		},
	}
	event.SetContext(ctx)

	assert.Equal(t, "run-123", event.GetRunID())
	assert.Equal(t, "applied", event.GetNewStatus())
	assert.Equal(t, ctx, event.GetContext())
	assert.NotNil(t, event.GetMetadata())
}

func TestRunStatusUpdaterIntegration(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockRS := mocks.NewMockStreamClient(mockCtrl)
	mockTFC := mocks.NewMockApiClient(mockCtrl)

	var callbackFunc func(runstream.RunEvent) bool
	mockRS.EXPECT().
		SubscribeTFRunEvents("gitlab", gomock.Any()).
		DoAndReturn(func(provider string, callback func(runstream.RunEvent) bool) (func(), error) {
			callbackFunc = callback
			return func() {}, nil
		})

	processor := NewRunStatusProcessor(&GitlabClient{}, mockRS, mockTFC)
	require.NotNil(t, processor)

	event := &runstream.TFRunEvent{
		RunID:     "run-test",
		NewStatus: string(tfe.RunPlanned),
		Metadata: &runstream.TFRunMetadata{
			RunID:                                "run-test",
			MergeRequestProjectNameWithNamespace: "test/project",
			MergeRequestIID:                      42,
		},
	}
	event.SetContext(context.Background())

	mockTFC.EXPECT().
		GetRun(gomock.Any(), "run-test").
		Return(&tfe.Run{
			ID:     "run-test",
			Status: tfe.RunPlanning,
		}, nil)

	if callbackFunc != nil {
		result := callbackFunc(event)
		assert.True(t, result)
	}
}
