package gitlab_hooks

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

func TestGitlabHooksHandler_handler_Authentication(t *testing.T) {
	tests := []struct {
		name           string
		hookSecretKey  string
		tokenHeader    string
		eventHeader    string
		expectStatus   int
		expectResponse string
	}{
		{
			name:           "missing X-Gitlab-Event header",
			hookSecretKey:  "",
			tokenHeader:    "",
			eventHeader:    "",
			expectStatus:   http.StatusBadRequest,
			expectResponse: "Invalid X-Gitlab-Event",
		},
		{
			name:           "invalid token when secret is configured",
			hookSecretKey:  "correct-secret",
			tokenHeader:    "wrong-token",
			eventHeader:    string(gogitlab.EventTypeMergeRequest),
			expectStatus:   http.StatusUnauthorized,
			expectResponse: "Unauthorized",
		},
		{
			name:           "valid token when secret is configured",
			hookSecretKey:  "test-secret",
			tokenHeader:    "test-secret",
			eventHeader:    "Push Hook", // Unhandled event type, but auth passes
			expectStatus:   http.StatusOK,
			expectResponse: "OK",
		},
		{
			name:           "no secret configured allows any request",
			hookSecretKey:  "",
			tokenHeader:    "any-token",
			eventHeader:    "Push Hook", // Unhandled event type
			expectStatus:   http.StatusOK,
			expectResponse: "OK",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &GitlabHooksHandler{
				hookSecretKey: tt.hookSecretKey,
			}

			req := httptest.NewRequest(http.MethodPost, "/webhook", nil)
			if tt.tokenHeader != "" {
				req.Header.Set(GitlabTokenHeader, tt.tokenHeader)
			}
			if tt.eventHeader != "" {
				req.Header.Set("X-Gitlab-Event", tt.eventHeader)
			}

			rec := httptest.NewRecorder()
			e := echo.New()
			c := e.NewContext(req, rec)

			err := h.handler(c)

			assert.NoError(t, err)
			assert.Equal(t, tt.expectStatus, rec.Code)
			assert.Equal(t, tt.expectResponse, rec.Body.String())
		})
	}
}

func TestGitlabHooksHandler_handler_EventRouting(t *testing.T) {
	tests := []struct {
		name         string
		eventType    string
		body         string
		expectStatus int
		expectNil    bool // Whether we expect nil streams to cause no panic
	}{
		{
			name:         "unhandled event type",
			eventType:    "Push Hook",
			body:         "",
			expectStatus: http.StatusOK,
			expectNil:    true, // No streams needed
		},
		{
			name:         "merge request event with invalid JSON",
			eventType:    string(gogitlab.EventTypeMergeRequest),
			body:         "invalid json",
			expectStatus: http.StatusOK,
			expectNil:    false, // Will try to parse and fail gracefully
		},
		{
			name:         "note event with invalid JSON",
			eventType:    string(gogitlab.EventTypeNote),
			body:         "invalid json",
			expectStatus: http.StatusOK,
			expectNil:    false, // Will try to parse and fail gracefully
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &GitlabHooksHandler{
				hookSecretKey: "", // No auth required
				// Note: streams are nil, which tests error handling
			}

			req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Gitlab-Event", tt.eventType)

			rec := httptest.NewRecorder()
			e := echo.New()
			c := e.NewContext(req, rec)

			err := h.handler(c)

			assert.NoError(t, err)
			assert.Equal(t, tt.expectStatus, rec.Code)
			assert.Equal(t, "OK", rec.Body.String())
		})
	}
}

func Test_checkError(t *testing.T) {
	tests := []struct {
		name   string
		ctx    context.Context
		err    error
		detail string
		want   bool
	}{
		{
			name:   "no error returns false",
			ctx:    context.Background(),
			err:    nil,
			detail: "test detail",
			want:   false,
		},
		{
			name:   "error returns true",
			ctx:    context.Background(),
			err:    errors.New("test error"),
			detail: "test detail",
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkError(tt.ctx, tt.err, tt.detail)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_getGitlabEventBody(t *testing.T) {
	type TestStruct struct {
		Name string `json:"name"`
		ID   int    `json:"id"`
	}

	tests := []struct {
		name        string
		body        string
		contentType string
		want        *TestStruct
		wantErr     bool
	}{
		{
			name:        "valid JSON",
			body:        `{"name":"test","id":123}`,
			contentType: "application/json",
			want:        &TestStruct{Name: "test", ID: 123},
			wantErr:     false,
		},
		{
			name:        "invalid JSON",
			body:        `{"name":"test","id":}`,
			contentType: "application/json",
			want:        nil,
			wantErr:     true,
		},
		{
			name:        "empty body",
			body:        "",
			contentType: "application/json",
			want:        &TestStruct{},
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader([]byte(tt.body)))
			req.Header.Set("Content-Type", tt.contentType)
			e := echo.New()
			c := e.NewContext(req, httptest.NewRecorder())

			got, err := getGitlabEventBody[TestStruct](c)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, got)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func Test_getNoteEventBody(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		contentType string
		wantErr     bool
		wantProject string
	}{
		{
			name: "valid note event",
			body: `{
				"object_kind": "note",
				"project": {
					"path_with_namespace": "test/project"
				},
				"merge_request": {
					"iid": 123
				},
				"object_attributes": {
					"note": "test comment",
					"noteable_type": "MergeRequest"
				}
			}`,
			contentType: "application/json",
			wantErr:     false,
			wantProject: "test/project",
		},
		{
			name:        "invalid JSON",
			body:        `{"invalid": json}`,
			contentType: "application/json",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader([]byte(tt.body)))
			req.Header.Set("Content-Type", tt.contentType)
			e := echo.New()
			c := e.NewContext(req, httptest.NewRecorder())

			got, err := getNoteEventBody(c)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, got)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, got)
				assert.NotNil(t, got.Payload)
				if tt.wantProject != "" {
					assert.Equal(t, tt.wantProject, got.Payload.GetProject().GetPathWithNamespace())
				}
			}
		})
	}
}
