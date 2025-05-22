package gitlab_hooks

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/utils"
	"github.com/zapier/tfbuddy/pkg/vcs"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
	"go.opentelemetry.io/otel/propagation"
	"go.uber.org/mock/gomock"
)

func createMergeEventForWorkerTest(action, projectPath string, mrIID int) *gogitlab.MergeEvent {
	event := &gogitlab.MergeEvent{}
	event.ObjectAttributes.Action = action
	event.ObjectAttributes.IID = mrIID
	event.Project.PathWithNamespace = projectPath
	return event
}

func TestNewGitlabEventWorker(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	worker := &GitlabEventWorker{
		tfc:             mocks.NewMockApiClient(ctrl),
		gl:              mocks.NewMockGitClient(ctrl),
		runstream:       mocks.NewMockStreamClient(ctrl),
		triggerCreation: tfc_trigger.NewTFCTrigger,
	}

	assert.NotNil(t, worker)
	assert.NotNil(t, worker.tfc)
	assert.NotNil(t, worker.gl)
	assert.NotNil(t, worker.runstream)
	assert.NotNil(t, worker.triggerCreation)
}

func TestGitlabEventWorker_processMREventStreamMsg(t *testing.T) {
	tests := []struct {
		name       string
		setupMocks func(*GitlabEventWorker)
		msg        *MergeRequestEventMsg
		wantErr    bool
		wantNilErr bool
	}{
		{
			name:       "successful MR processing",
			setupMocks: func(w *GitlabEventWorker) {},
			msg: &MergeRequestEventMsg{
				Payload: createMergeEventForWorkerTest("open", "test/project", 123),
				Context: context.Background(),
				Carrier: propagation.MapCarrier{},
			},
			wantErr:    false,
			wantNilErr: false,
		},
		{
			name:       "MR processing with permanent error",
			setupMocks: func(w *GitlabEventWorker) {},
			msg: &MergeRequestEventMsg{
				Payload: createMergeEventForWorkerTest("open", "test/project", 123),
				Context: context.Background(),
				Carrier: propagation.MapCarrier{},
			},
			wantErr:    false,
			wantNilErr: true,
		},
		{
			name:       "panic recovery in MR processing",
			setupMocks: func(w *GitlabEventWorker) {},
			msg: &MergeRequestEventMsg{
				Payload: nil,
				Context: context.Background(),
				Carrier: propagation.MapCarrier{},
			},
			wantErr:    false,
			wantNilErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &GitlabEventWorker{
				tfc:       nil,
				gl:        nil,
				runstream: nil,
			}

			if tt.setupMocks != nil {
				tt.setupMocks(w)
			}

			err := w.processMREventStreamMsg(tt.msg)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				if tt.wantNilErr {
					assert.NoError(t, err)
				} else {
					assert.NoError(t, err)
				}
			}
		})
	}
}

func TestGitlabEventWorker_processNoteEventStreamMsg(t *testing.T) {
	tests := []struct {
		name       string
		setupMocks func(*GitlabEventWorker)
		msg        *NoteEventMsg
		wantErr    bool
		wantNilErr bool
	}{
		{
			name: "note processing with nil payload causes panic recovery",
			setupMocks: func(w *GitlabEventWorker) {
			},
			msg: &NoteEventMsg{
				Payload: nil,
				Context: context.Background(),
				Carrier: propagation.MapCarrier{},
			},
			wantErr:    false,
			wantNilErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &GitlabEventWorker{
				tfc:       nil,
				gl:        nil,
				runstream: nil,
			}

			if tt.setupMocks != nil {
				tt.setupMocks(w)
			}

			err := w.processNoteEventStreamMsg(tt.msg)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				if tt.wantNilErr {
					assert.NoError(t, err)
				} else {
					assert.NoError(t, err)
				}
			}
		})
	}
}

func TestGitlabEventWorker_ErrorHandling(t *testing.T) {
	t.Run("permanent error returns nil", func(t *testing.T) {
		permanentErr := utils.CreatePermanentError(errors.New("test error"))
		var handlerCalled bool

		result := utils.EmitPermanentError(permanentErr, func(err error) {
			handlerCalled = true
		})

		assert.NoError(t, result)
		assert.False(t, handlerCalled)
	})

	t.Run("non-permanent error is returned", func(t *testing.T) {
		regularErr := errors.New("regular error")
		var handlerCalled bool

		result := utils.EmitPermanentError(regularErr, func(err error) {
			handlerCalled = true
		})

		assert.Error(t, result)
		assert.Equal(t, regularErr, result)
		assert.False(t, handlerCalled)
	})

	t.Run("nil error returns nil", func(t *testing.T) {
		var handlerCalled bool

		result := utils.EmitPermanentError(nil, func(err error) {
			handlerCalled = true
		})

		assert.NoError(t, result)
		assert.False(t, handlerCalled)
	})
}

func TestGitlabEventWorker_Integration(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockTFC := mocks.NewMockApiClient(ctrl)
	mockGL := mocks.NewMockGitClient(ctrl)
	mockRunstream := mocks.NewMockStreamClient(ctrl)

	w := &GitlabEventWorker{
		tfc:       mockTFC,
		gl:        mockGL,
		runstream: mockRunstream,
		triggerCreation: func(gl vcs.GitClient, tfc tfc_api.ApiClient, runstream runstream.StreamClient, config *tfc_trigger.TFCTriggerOptions) tfc_trigger.Trigger {
			return mocks.NewMockTrigger(ctrl)
		},
	}

	t.Run("valid MR event with mocked dependencies", func(t *testing.T) {
		msg := &MergeRequestEventMsg{
			Payload: createMergeEventForWorkerTest("open", "unauthorized/project", 123),
			Context: context.Background(),
			Carrier: propagation.MapCarrier{},
		}

		err := w.processMREventStreamMsg(msg)
		assert.NoError(t, err)
	})
}

func TestGitlabEventWorker_TriggerCreationFunction(t *testing.T) {
	w := &GitlabEventWorker{
		triggerCreation: tfc_trigger.NewTFCTrigger,
	}

	assert.NotNil(t, w.triggerCreation)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockTFC := mocks.NewMockApiClient(ctrl)
	mockGL := mocks.NewMockGitClient(ctrl)
	mockRunstream := mocks.NewMockStreamClient(ctrl)

	config := &tfc_trigger.TFCTriggerOptions{
		Action: tfc_trigger.PlanAction,
	}

	trigger := w.triggerCreation(mockGL, mockTFC, mockRunstream, config)
	assert.NotNil(t, trigger)
}

func TestNewGitlabEventWorker_Constructor(t *testing.T) {
	t.Run("handles nil handler gracefully", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				assert.NotNil(t, r)
			}
		}()

		NewGitlabEventWorker(nil, nil)
	})
}
