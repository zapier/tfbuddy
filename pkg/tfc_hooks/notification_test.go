package tfc_hooks

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/go-tfe"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"go.uber.org/mock/gomock"
)

func TestNewNotificationHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockAPI := mocks.NewMockApiClient(ctrl)
	mockStream := mocks.NewMockStreamClient(ctrl)

	mockStream.EXPECT().SubscribeTFRunPollingTasks(gomock.Any()).Return(func() {}, nil)

	handler := NewNotificationHandler(mockAPI, mockStream)
	require.NotNil(t, handler)
	assert.Equal(t, mockAPI, handler.api)
	assert.Equal(t, mockStream, handler.stream)
}

func TestNewNotificationHandler_SubscriptionError(t *testing.T) {
	// This test would cause the program to exit due to log.Fatal()
	// In a real scenario, we'd need to refactor to return errors instead of calling log.Fatal
	t.Skip("Test skipped because log.Fatal() calls os.Exit()")
}

func TestNotificationHandler_Handler(t *testing.T) {
	tests := []struct {
		name           string
		payload        string
		setupMocks     func(*mocks.MockApiClient, *mocks.MockStreamClient)
		expectedStatus int
	}{
		{
			name:    "Valid notification",
			payload: NotificationRunCreatedUIPayload,
			setupMocks: func(mockAPI *mocks.MockApiClient, mockStream *mocks.MockStreamClient) {
				mockStream.EXPECT().SubscribeTFRunPollingTasks(gomock.Any()).Return(func() {}, nil)
				mockAPI.EXPECT().GetRun(gomock.Any(), "run-Vy7sSoyhizTafW8f").Return(&tfe.Run{ID: "run-Vy7sSoyhizTafW8f"}, nil)
				mockStream.EXPECT().PublishTFRunEvent(gomock.Any(), gomock.Any()).Return(nil)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:    "Invalid JSON payload",
			payload: "invalid json",
			setupMocks: func(mockAPI *mocks.MockApiClient, mockStream *mocks.MockStreamClient) {
				mockStream.EXPECT().SubscribeTFRunPollingTasks(gomock.Any()).Return(func() {}, nil)
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:    "Verification payload",
			payload: NotificationVerificationPayload,
			setupMocks: func(mockAPI *mocks.MockApiClient, mockStream *mocks.MockStreamClient) {
				mockStream.EXPECT().SubscribeTFRunPollingTasks(gomock.Any()).Return(func() {}, nil)
			},
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockAPI := mocks.NewMockApiClient(ctrl)
			mockStream := mocks.NewMockStreamClient(ctrl)

			tt.setupMocks(mockAPI, mockStream)

			handler := NewNotificationHandler(mockAPI, mockStream)

			e := echo.New()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.payload))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := handler.Handler()(c)

			if tt.expectedStatus == http.StatusOK {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedStatus, rec.Code)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestNotificationHandler_processNotification(t *testing.T) {
	tests := []struct {
		name       string
		payload    *NotificationPayload
		setupMocks func(*mocks.MockApiClient, *mocks.MockStreamClient)
	}{
		{
			name:    "Empty RunId",
			payload: &NotificationPayload{RunId: ""},
			setupMocks: func(mockAPI *mocks.MockApiClient, mockStream *mocks.MockStreamClient) {
				mockStream.EXPECT().SubscribeTFRunPollingTasks(gomock.Any()).Return(func() {}, nil)
			},
		},
		{
			name: "Successful processing",
			payload: &NotificationPayload{
				RunId:            "run-test",
				OrganizationName: "test-org",
				WorkspaceName:    "test-workspace",
				Notifications: []struct {
					Message      string        `json:"message"`
					Trigger      string        `json:"trigger"`
					RunStatus    tfe.RunStatus `json:"run_status"`
					RunUpdatedAt time.Time     `json:"run_updated_at"`
					RunUpdatedBy string        `json:"run_updated_by"`
				}{{RunStatus: tfe.RunPlanning}},
			},
			setupMocks: func(mockAPI *mocks.MockApiClient, mockStream *mocks.MockStreamClient) {
				mockStream.EXPECT().SubscribeTFRunPollingTasks(gomock.Any()).Return(func() {}, nil)
				mockAPI.EXPECT().GetRun(gomock.Any(), "run-test").Return(&tfe.Run{ID: "run-test"}, nil)
				mockStream.EXPECT().PublishTFRunEvent(gomock.Any(), &runstream.TFRunEvent{
					Organization: "test-org",
					Workspace:    "test-workspace",
					RunID:        "run-test",
					NewStatus:    string(tfe.RunPlanning),
				}).Return(nil)
			},
		},
		{
			name: "Empty notifications array",
			payload: &NotificationPayload{
				RunId:            "run-test",
				OrganizationName: "test-org",
				WorkspaceName:    "test-workspace",
				Notifications: []struct {
					Message      string        `json:"message"`
					Trigger      string        `json:"trigger"`
					RunStatus    tfe.RunStatus `json:"run_status"`
					RunUpdatedAt time.Time     `json:"run_updated_at"`
					RunUpdatedBy string        `json:"run_updated_by"`
				}{},
			},
			setupMocks: func(mockAPI *mocks.MockApiClient, mockStream *mocks.MockStreamClient) {
				mockStream.EXPECT().SubscribeTFRunPollingTasks(gomock.Any()).Return(func() {}, nil)
				// No other expectations as processNotification should return early
			},
		},
		{
			name: "API error",
			payload: &NotificationPayload{
				RunId:            "run-test",
				OrganizationName: "test-org",
				WorkspaceName:    "test-workspace",
				Notifications: []struct {
					Message      string        `json:"message"`
					Trigger      string        `json:"trigger"`
					RunStatus    tfe.RunStatus `json:"run_status"`
					RunUpdatedAt time.Time     `json:"run_updated_at"`
					RunUpdatedBy string        `json:"run_updated_by"`
				}{{RunStatus: tfe.RunErrored}},
			},
			setupMocks: func(mockAPI *mocks.MockApiClient, mockStream *mocks.MockStreamClient) {
				mockStream.EXPECT().SubscribeTFRunPollingTasks(gomock.Any()).Return(func() {}, nil)
				mockAPI.EXPECT().GetRun(gomock.Any(), "run-test").Return(nil, errors.New("API error"))
				mockStream.EXPECT().PublishTFRunEvent(gomock.Any(), gomock.Any()).Return(nil)
			},
		},
		{
			name: "Stream publish error",
			payload: &NotificationPayload{
				RunId:            "run-test",
				OrganizationName: "test-org",
				WorkspaceName:    "test-workspace",
				Notifications: []struct {
					Message      string        `json:"message"`
					Trigger      string        `json:"trigger"`
					RunStatus    tfe.RunStatus `json:"run_status"`
					RunUpdatedAt time.Time     `json:"run_updated_at"`
					RunUpdatedBy string        `json:"run_updated_by"`
				}{{RunStatus: tfe.RunApplied}},
			},
			setupMocks: func(mockAPI *mocks.MockApiClient, mockStream *mocks.MockStreamClient) {
				mockStream.EXPECT().SubscribeTFRunPollingTasks(gomock.Any()).Return(func() {}, nil)
				mockAPI.EXPECT().GetRun(gomock.Any(), "run-test").Return(&tfe.Run{ID: "run-test"}, nil)
				mockStream.EXPECT().PublishTFRunEvent(gomock.Any(), gomock.Any()).Return(errors.New("stream error"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockAPI := mocks.NewMockApiClient(ctrl)
			mockStream := mocks.NewMockStreamClient(ctrl)

			tt.setupMocks(mockAPI, mockStream)

			handler := NewNotificationHandler(mockAPI, mockStream)

			handler.processNotification(context.Background(), tt.payload)
		})
	}
}

func TestNotificationPayload_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantErr bool
	}{
		{
			name:    "Valid verification payload",
			payload: NotificationVerificationPayload,
			wantErr: false,
		},
		{
			name:    "Valid run created payload",
			payload: NotificationRunCreatedUIPayload,
			wantErr: false,
		},
		{
			name:    "Valid run planning payload",
			payload: NotificationRunPlanningPayload,
			wantErr: false,
		},
		{
			name:    "Valid run errored payload",
			payload: NotificationRunErroredPayload,
			wantErr: false,
		},
		{
			name:    "Valid run planned payload",
			payload: NotificationRunPlannedPayload,
			wantErr: false,
		},
		{
			name:    "Invalid JSON",
			payload: "invalid json",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var payload NotificationPayload
			err := json.Unmarshal([]byte(tt.payload), &payload)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, 1, payload.PayloadVersion)
			}
		})
	}
}

const NotificationVerificationPayload = `{
    "payload_version": 1,
    "notification_configuration_id": "nc-YfWgs5thoiEteRJj",
    "run_url": null,
    "run_id": null,
    "run_message": null,
    "run_created_at": null,
    "run_created_by": null,
    "workspace_id": null,
    "workspace_name": null,
    "organization_name": null,
    "notifications": [
        {
            "message": "Verification of tfbuddy",
            "trigger": "verification",
            "run_status": null,
            "run_updated_at": null,
            "run_updated_by": null
        }
    ]
}`

const NotificationRunCreatedUIPayload = `{
    "payload_version": 1,
    "notification_configuration_id": "nc-YfWgs5thoiEteRJj",
    "run_url": "https://app.terraform.io/app/zapier/service-tfbuddy-dev/runs/run-Vy7sSoyhizTafW8f",
    "run_id": "run-Vy7sSoyhizTafW8f",
    "run_message": "testing notifications",
    "run_created_at": "2022-01-23T21:22:00.000Z",
    "run_created_by": "matt_morrison",
    "workspace_id": "ws-WyuJ7TFeifRBdPQ9",
    "workspace_name": "service-tfbuddy-dev",
    "organization_name": "zapier",
    "notifications": [
        {
            "message": "Run Created",
            "trigger": "run:created",
            "run_status": "pending",
            "run_updated_at": "2022-01-23T21:22:00.000Z",
            "run_updated_by": "matt_morrison"
        }
    ]
}`

const NotificationRunPlanningPayload = `{
    "payload_version": 1,
    "notification_configuration_id": "nc-YfWgs5thoiEteRJj",
    "run_url": "https://app.terraform.io/app/zapier/service-tfbuddy-dev/runs/run-Vy7sSoyhizTafW8f",
    "run_id": "run-Vy7sSoyhizTafW8f",
    "run_message": "testing notifications",
    "run_created_at": "2022-01-23T21:22:00.000Z",
    "run_created_by": "matt_morrison",
    "workspace_id": "ws-WyuJ7TFeifRBdPQ9",
    "workspace_name": "service-tfbuddy-dev",
    "organization_name": "zapier",
    "notifications": [
        {
            "message": "Run Planning",
            "trigger": "run:planning",
            "run_status": "planning",
            "run_updated_at": "2022-01-23T21:22:02.000Z",
            "run_updated_by": null
        }
    ]
}`

const NotificationRunPlannedPayload = `{
    "payload_version": 1,
    "notification_configuration_id": "nc-YfWgs5thoiEteRJj",
    "run_url": "https://app.terraform.io/app/zapier/service-tfbuddy-dev/runs/run-Vy7sSoyhizTafW8f",
    "run_id": "run-Vy7sSoyhizTafW8f",
    "run_message": "testing notifications",
    "run_created_at": "2022-01-23T21:22:00.000Z",
    "run_created_by": "matt_morrison",
    "workspace_id": "ws-WyuJ7TFeifRBdPQ9",
    "workspace_name": "service-tfbuddy-dev",
    "organization_name": "zapier",
    "notifications": [
        {
            "message": "Run Planned and Finished",
            "trigger": "run:completed",
            "run_status": "planned_and_finished",
            "run_updated_at": "2022-01-23T21:22:23.000Z",
            "run_updated_by": null
        }
    ]
}`

const NotificationRunErroredPayload = `{
    "payload_version": 1,
    "notification_configuration_id": "nc-YfWgs5thoiEteRJj",
    "run_url": "https://app.terraform.io/app/zapier/service-tfbuddy-dev/runs/run-TCmduqLvvFw7WWmC",
    "run_id": "run-TCmduqLvvFw7WWmC",
    "run_message": "v0.3.10\n",
    "run_created_at": "2022-01-23T23:00:06.000Z",
    "run_created_by": "Matt Morrison",
    "workspace_id": "ws-WyuJ7TFeifRBdPQ9",
    "workspace_name": "service-tfbuddy-dev",
    "organization_name": "zapier",
    "notifications": [
        {
            "message": "Run Errored",
            "trigger": "run:errored",
            "run_status": "errored",
            "run_updated_at": "2022-01-23T23:00:10.000Z",
            "run_updated_by": null
        }
    ]
}`
