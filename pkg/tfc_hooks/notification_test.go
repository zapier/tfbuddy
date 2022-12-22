package tfc_hooks

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/go-tfe"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
)

//
//func Test_processNotification(t *testing.T) {
//	testTFCApiClient := &TestApiClient{t: t}
//	testHandler := NewNotificationHandler(testTFCApiClient)
//
//	type args struct {
//		n *NotificationPayload
//	}
//	tests := []struct {
//		name    string
//		args    args
//		runData string
//		want    Action
//	}{
//		{
//			name:    "Verification",
//			args:    args{testParsePayload(t, NotificationVerificationPayload)},
//			runData: runData,
//			want: Action{
//				Type: NotificationActionNone,
//				Data: nil,
//			},
//		},
//		{
//			name:    "Created",
//			args:    args{testParsePayload(t, NotificationRunCreatedUIPayload)},
//			runData: runData,
//			want: Action{
//				Type: NotificationActionMergeRequestComment,
//				Data: map[string]string{
//					"CommentBody":           "asdfasdfdas",
//					"GitlabProjectID":       "",
//					"GitlabMergeRequestIID": "",
//				},
//			},
//		},
//		{
//			name:    "Planning",
//			args:    args{testParsePayload(t, NotificationRunPlanningPayload)},
//			runData: runData,
//			want: Action{
//				Type: NotificationActionMergeRequestComment,
//				Data: map[string]string{
//					"CommentBody":           "asdfasdfdas",
//					"GitlabProjectID":       "",
//					"GitlabMergeRequestIID": "",
//				},
//			},
//		},
//	}
//	for _, tt := range tests {
//		t.Run(tt.name, func(t *testing.T) {
//			testTFCApiClient.RunData = []byte(tt.runData)
//			if got := testHandler.processNotification(tt.args.n); !reflect.DeepEqual(got, tt.want) {
//				t.Errorf("processNotification() = %v, want %v", got, tt.want)
//			}
//		})
//	}
//}

type TestApiClient struct {
	RunData       []byte
	WorkspaceData []byte
	t             *testing.T
}

func (api *TestApiClient) GetWorkspaceByName(ctx context.Context, org, name string) (*tfe.Workspace, error) {
	//TODO implement me
	panic("implement me")
}

func (api *TestApiClient) CreateRunFromSource(opts tfc_api.ApiRunOptions) (*tfe.Run, error) {
	//TODO implement me
	panic("implement me")
}

func (api *TestApiClient) GetRun(id string) (*tfe.Run, error) {
	run := &tfe.Run{}
	err := json.Unmarshal(api.RunData, run)
	if err != nil {
		api.t.Errorf("Test setup failure - could not unmarshal TFE Run data: %v", err)
	}
	return run, nil
}

func (api *TestApiClient) GetWorkspaceById(ctx context.Context, id string) (*tfe.Workspace, error) {
	workspace := &tfe.Workspace{}
	err := json.Unmarshal(api.WorkspaceData, workspace)
	if err != nil {
		api.t.Errorf("Test setup failure - could not unmarshal TFE Workspace data: %v", err)
	}
	return workspace, nil
}

func testParsePayload(t *testing.T, payStr string) *NotificationPayload {
	pay := NotificationPayload{}
	err := json.Unmarshal([]byte(payStr), &pay)
	if err != nil {
		t.Fatalf("Test setup error, failed to parse test payload: %v", err)
	}

	return &pay
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

const notificationRunPlannedPayload = `{
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

const runData = `{
    "data": {
        "id": "run-CZcmD7eagjhyX0vN",
        "type": "runs",
        "attributes": {
            "actions": {
                "is-cancelable": true,
                "is-confirmable": false,
                "is-discardable": false,
                "is-force-cancelable": false
            },
            "canceled-at": null,
            "created-at": "2021-05-24T07:38:04.171Z",
            "has-changes": false,
            "auto-apply": false,
            "is-destroy": false,
            "message": "Custom message",
            "plan-only": false,
            "source": "tfe-api",
            "status-timestamps": {
                "plan-queueable-at": "2021-05-24T07:38:04+00:00"
            },
            "status": "pending",
            "trigger-reason": "manual",
            "target-addrs": null,
            "permissions": {
                "can-apply": true,
                "can-cancel": true,
                "can-comment": true,
                "can-discard": true,
                "can-force-execute": true,
                "can-force-cancel": true,
                "can-override-policy-check": true
            },
            "refresh": false,
            "refresh-only": false,
            "replace-addrs": null,
            "variables": []
        },
        "relationships": {
            "apply": {},
            "comments": {},
            "configuration-version": {},
            "cost-estimate": {},
            "created-by": {},
            "input-state-version": {},
            "plan": {},
            "run-events": {},
            "policy-checks": {},
            "workspace": {},
            "workspace-run-alerts": {}
        }
    },
    "links": {
        "self": "/api/v2/runs/run-bWSq4YeYpfrW4mx7"
    }
}`
