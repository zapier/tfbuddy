package tfc_hooks

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
)

func TestRunTaskHandler(t *testing.T) {
	tests := []struct {
		name               string
		payload            string
		expectedStatusCode int
		expectError        bool
	}{
		{
			name: "Valid run task event",
			payload: `{
				"payload_version": 1,
				"access_token": "test-token",
				"task_result_id": "taskresult-123",
				"task_result_enforcement_level": "advisory",
				"task_result_callback_url": "https://app.terraform.io/api/v2/task-results/taskresult-123/callback",
				"run_app_url": "https://app.terraform.io/app/org/workspace/runs/run-123",
				"run_id": "run-123",
				"run_message": "Test run",
				"run_created_at": "2023-01-01T12:00:00Z",
				"run_created_by": "test-user",
				"workspace_id": "ws-123",
				"workspace_name": "test-workspace",
				"workspace_app_url": "https://app.terraform.io/app/org/test-workspace",
				"organization_name": "test-org",
				"plan_json_api_url": "https://app.terraform.io/api/v2/plans/plan-123/json-output",
				"vcs_repo_url": "https://github.com/org/repo",
				"vcs_branch": "main",
				"vcs_pull_request_url": null,
				"vcs_commit_url": "https://github.com/org/repo/commit/abc123"
			}`,
			expectedStatusCode: http.StatusOK,
			expectError:        false,
		},
		{
			name: "Valid run task event with pull request",
			payload: `{
				"payload_version": 1,
				"access_token": "test-token",
				"task_result_id": "taskresult-456",
				"task_result_enforcement_level": "mandatory",
				"task_result_callback_url": "https://app.terraform.io/api/v2/task-results/taskresult-456/callback",
				"run_app_url": "https://app.terraform.io/app/org/workspace/runs/run-456",
				"run_id": "run-456",
				"run_message": "PR test run",
				"run_created_at": "2023-01-02T12:00:00Z",
				"run_created_by": "pr-user",
				"workspace_id": "ws-456",
				"workspace_name": "pr-workspace",
				"workspace_app_url": "https://app.terraform.io/app/org/pr-workspace",
				"organization_name": "test-org",
				"plan_json_api_url": "https://app.terraform.io/api/v2/plans/plan-456/json-output",
				"vcs_repo_url": "https://github.com/org/repo",
				"vcs_branch": "feature-branch",
				"vcs_pull_request_url": "https://github.com/org/repo/pull/123",
				"vcs_commit_url": "https://github.com/org/repo/commit/def456"
			}`,
			expectedStatusCode: http.StatusOK,
			expectError:        false,
		},
		{
			name: "Minimal valid payload",
			payload: `{
				"payload_version": 1,
				"access_token": "minimal-token",
				"task_result_id": "taskresult-minimal",
				"run_id": "run-minimal"
			}`,
			expectedStatusCode: http.StatusOK,
			expectError:        false,
		},
		{
			name:               "Invalid JSON payload",
			payload:            `{"invalid": json}`,
			expectedStatusCode: http.StatusBadRequest,
			expectError:        true,
		},
		{
			name:               "Empty payload",
			payload:            "",
			expectedStatusCode: http.StatusOK,
			expectError:        false, // Empty payload creates empty struct, doesn't fail
		},
		{
			name:               "Non-JSON payload",
			payload:            "not json at all",
			expectedStatusCode: http.StatusBadRequest,
			expectError:        true,
		},
		{
			name:               "Empty JSON object",
			payload:            `{}`,
			expectedStatusCode: http.StatusOK,
			expectError:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.payload))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := RunTaskHandler(c)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedStatusCode, rec.Code)
				assert.Equal(t, "OK", rec.Body.String())
			}
		})
	}
}

func TestRunTaskEvent_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		payload  string
		wantErr  bool
		validate func(*testing.T, *RunTaskEvent)
	}{
		{
			name: "Complete payload with all fields",
			payload: `{
				"payload_version": 2,
				"access_token": "at-token123",
				"task_result_id": "taskresult-abc123",
				"task_result_enforcement_level": "mandatory",
				"task_result_callback_url": "https://app.terraform.io/api/v2/task-results/taskresult-abc123/callback",
				"run_app_url": "https://app.terraform.io/app/myorg/myworkspace/runs/run-def456",
				"run_id": "run-def456",
				"run_message": "Triggered via API",
				"run_created_at": "2023-06-15T14:30:00Z",
				"run_created_by": "api-user",
				"workspace_id": "ws-xyz789",
				"workspace_name": "production-workspace",
				"workspace_app_url": "https://app.terraform.io/app/myorg/production-workspace",
				"organization_name": "myorg",
				"plan_json_api_url": "https://app.terraform.io/api/v2/plans/plan-ghi789/json-output",
				"vcs_repo_url": "https://github.com/myorg/infrastructure",
				"vcs_branch": "main",
				"vcs_pull_request_url": "https://github.com/myorg/infrastructure/pull/42",
				"vcs_commit_url": "https://github.com/myorg/infrastructure/commit/abcdef123456"
			}`,
			wantErr: false,
			validate: func(t *testing.T, event *RunTaskEvent) {
				assert.Equal(t, 2, event.PayloadVersion)
				assert.Equal(t, "at-token123", event.AccessToken)
				assert.Equal(t, "taskresult-abc123", event.TaskResultId)
				assert.Equal(t, "mandatory", event.TaskResultEnforcementLevel)
				assert.Equal(t, "run-def456", event.RunId)
				assert.Equal(t, "Triggered via API", event.RunMessage)
				assert.Equal(t, "api-user", event.RunCreatedBy)
				assert.Equal(t, "ws-xyz789", event.WorkspaceId)
				assert.Equal(t, "production-workspace", event.WorkspaceName)
				assert.Equal(t, "myorg", event.OrganizationName)
				assert.Equal(t, "main", event.VcsBranch)
				assert.Equal(t, "https://github.com/myorg/infrastructure/pull/42", event.VcsPullRequestUrl)

				expectedTime, _ := time.Parse(time.RFC3339, "2023-06-15T14:30:00Z")
				assert.Equal(t, expectedTime, event.RunCreatedAt)
			},
		},
		{
			name: "Null vcs_pull_request_url",
			payload: `{
				"payload_version": 1,
				"run_id": "run-test",
				"vcs_pull_request_url": null
			}`,
			wantErr: false,
			validate: func(t *testing.T, event *RunTaskEvent) {
				assert.Equal(t, 1, event.PayloadVersion)
				assert.Equal(t, "run-test", event.RunId)
				assert.Nil(t, event.VcsPullRequestUrl)
			},
		},
		{
			name: "String vcs_pull_request_url",
			payload: `{
				"payload_version": 1,
				"run_id": "run-test",
				"vcs_pull_request_url": "https://github.com/org/repo/pull/123"
			}`,
			wantErr: false,
			validate: func(t *testing.T, event *RunTaskEvent) {
				assert.Equal(t, "https://github.com/org/repo/pull/123", event.VcsPullRequestUrl)
			},
		},
		{
			name: "Empty time field",
			payload: `{
				"payload_version": 1,
				"run_id": "run-test",
				"run_created_at": ""
			}`,
			wantErr:  true,
			validate: nil,
		},
		{
			name: "Invalid time format",
			payload: `{
				"payload_version": 1,
				"run_id": "run-test",
				"run_created_at": "not-a-date"
			}`,
			wantErr:  true,
			validate: nil,
		},
		{
			name:     "Invalid JSON",
			payload:  `{"invalid": json}`,
			wantErr:  true,
			validate: nil,
		},
		{
			name: "Zero values",
			payload: `{
				"payload_version": 0,
				"access_token": "",
				"task_result_id": "",
				"run_id": ""
			}`,
			wantErr: false,
			validate: func(t *testing.T, event *RunTaskEvent) {
				assert.Equal(t, 0, event.PayloadVersion)
				assert.Equal(t, "", event.AccessToken)
				assert.Equal(t, "", event.TaskResultId)
				assert.Equal(t, "", event.RunId)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var event RunTaskEvent
			err := json.Unmarshal([]byte(tt.payload), &event)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, &event)
				}
			}
		})
	}
}

func TestRunTaskCallback_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		payload  string
		wantErr  bool
		validate func(*testing.T, *RunTaskCallback)
	}{
		{
			name: "Valid callback with passed status",
			payload: `{
				"data": {
					"type": "task-results",
					"attributes": {
						"status": "passed",
						"message": "All checks passed successfully",
						"url": "https://example.com/task-result"
					}
				}
			}`,
			wantErr: false,
			validate: func(t *testing.T, callback *RunTaskCallback) {
				assert.Equal(t, "task-results", callback.Data.Type)
				assert.Equal(t, "passed", callback.Data.Attributes.Status)
				assert.Equal(t, "All checks passed successfully", callback.Data.Attributes.Message)
				assert.Equal(t, "https://example.com/task-result", callback.Data.Attributes.Url)
			},
		},
		{
			name: "Valid callback with failed status",
			payload: `{
				"data": {
					"type": "task-results",
					"attributes": {
						"status": "failed",
						"message": "Security scan found vulnerabilities",
						"url": "https://security-scanner.example.com/report/123"
					}
				}
			}`,
			wantErr: false,
			validate: func(t *testing.T, callback *RunTaskCallback) {
				assert.Equal(t, "task-results", callback.Data.Type)
				assert.Equal(t, "failed", callback.Data.Attributes.Status)
				assert.Equal(t, "Security scan found vulnerabilities", callback.Data.Attributes.Message)
				assert.Equal(t, "https://security-scanner.example.com/report/123", callback.Data.Attributes.Url)
			},
		},
		{
			name: "Valid callback with running status",
			payload: `{
				"data": {
					"type": "task-results",
					"attributes": {
						"status": "running",
						"message": "Task is currently executing",
						"url": ""
					}
				}
			}`,
			wantErr: false,
			validate: func(t *testing.T, callback *RunTaskCallback) {
				assert.Equal(t, "running", callback.Data.Attributes.Status)
				assert.Equal(t, "Task is currently executing", callback.Data.Attributes.Message)
				assert.Equal(t, "", callback.Data.Attributes.Url)
			},
		},
		{
			name: "Minimal valid callback",
			payload: `{
				"data": {
					"type": "",
					"attributes": {
						"status": "",
						"message": "",
						"url": ""
					}
				}
			}`,
			wantErr: false,
			validate: func(t *testing.T, callback *RunTaskCallback) {
				assert.Equal(t, "", callback.Data.Type)
				assert.Equal(t, "", callback.Data.Attributes.Status)
				assert.Equal(t, "", callback.Data.Attributes.Message)
				assert.Equal(t, "", callback.Data.Attributes.Url)
			},
		},
		{
			name:     "Invalid JSON",
			payload:  `{"data": {"invalid": json}}`,
			wantErr:  true,
			validate: nil,
		},
		{
			name:     "Empty payload",
			payload:  "",
			wantErr:  true,
			validate: nil,
		},
		{
			name: "Missing data field",
			payload: `{
				"type": "task-results",
				"attributes": {
					"status": "passed"
				}
			}`,
			wantErr: false,
			validate: func(t *testing.T, callback *RunTaskCallback) {
				// Should have zero values for missing data field
				assert.Equal(t, "", callback.Data.Type)
				assert.Equal(t, "", callback.Data.Attributes.Status)
			},
		},
		{
			name: "Missing attributes field",
			payload: `{
				"data": {
					"type": "task-results"
				}
			}`,
			wantErr: false,
			validate: func(t *testing.T, callback *RunTaskCallback) {
				assert.Equal(t, "task-results", callback.Data.Type)
				assert.Equal(t, "", callback.Data.Attributes.Status)
				assert.Equal(t, "", callback.Data.Attributes.Message)
				assert.Equal(t, "", callback.Data.Attributes.Url)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var callback RunTaskCallback
			err := json.Unmarshal([]byte(tt.payload), &callback)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, &callback)
				}
			}
		})
	}
}

func TestRunTaskHandler_ContentTypes(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		payload     string
		expectError bool
	}{
		{
			name:        "application/json content type",
			contentType: echo.MIMEApplicationJSON,
			payload:     `{"payload_version": 1, "run_id": "test"}`,
			expectError: false,
		},
		{
			name:        "application/json with charset",
			contentType: "application/json; charset=utf-8",
			payload:     `{"payload_version": 1, "run_id": "test"}`,
			expectError: false,
		},
		{
			name:        "text/plain content type with JSON payload",
			contentType: "text/plain",
			payload:     `{"payload_version": 1, "run_id": "test"}`,
			expectError: true, // Echo's default binder requires application/json
		},
		{
			name:        "missing content type",
			contentType: "",
			payload:     `{"payload_version": 1, "run_id": "test"}`,
			expectError: true, // Echo's default binder requires application/json
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.payload))
			if tt.contentType != "" {
				req.Header.Set(echo.HeaderContentType, tt.contentType)
			}
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := RunTaskHandler(c)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, http.StatusOK, rec.Code)
				assert.Equal(t, "OK", rec.Body.String())
			}
		})
	}
}

func TestRunTaskHandler_HTTPMethods(t *testing.T) {
	methods := []string{
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodGet,
		http.MethodDelete,
		http.MethodHead,
		http.MethodOptions,
	}

	payload := `{"payload_version": 1, "run_id": "test"}`

	for _, method := range methods {
		t.Run("method_"+method, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(method, "/", strings.NewReader(payload))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := RunTaskHandler(c)

			// Handler should work with any HTTP method since it doesn't check
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, "OK", rec.Body.String())
		})
	}
}
