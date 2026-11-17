package tfc_hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.uber.org/mock/gomock"

	"github.com/zapier/tfbuddy/pkg/mocks"
)

// payloadValue stands in for a value inside a webhook payload (a token, a
// commit message, a user name). It must never reach the logs or stdout.
const payloadValue = "payload-value-that-must-not-be-logged"

// captureOutput sends every log line, at every level, and everything
// written to stdout to one buffer until the returned function is called.
func captureOutput(t *testing.T) func() string {
	t.Helper()
	prevLevel := zerolog.GlobalLevel()
	prevLogger := log.Logger
	prevStdout := os.Stdout
	t.Cleanup(func() {
		zerolog.SetGlobalLevel(prevLevel)
		log.Logger = prevLogger
		os.Stdout = prevStdout
	})

	zerolog.SetGlobalLevel(zerolog.TraceLevel)
	logs := &bytes.Buffer{}
	log.Logger = zerolog.New(logs)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("could not create pipe: %v", err)
	}
	os.Stdout = w
	stdout := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		stdout <- string(b)
	}()

	return func() string {
		os.Stdout = prevStdout
		_ = w.Close()
		return logs.String() + <-stdout
	}
}

func TestProcessNotification_LogsIdentifiersOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	// No GetRun expectation: the run is not fetched, so it can't be printed.
	api := mocks.NewMockApiClient(ctrl)
	stream := mocks.NewMockStreamClient(ctrl)
	stream.EXPECT().PublishTFRunEvent(gomock.Any(), gomock.Any()).Return(nil)

	h := &NotificationHandler{api: api, stream: stream}
	n := &NotificationPayload{}
	payload := `{
		"run_id": "run-abc123",
		"run_message": "` + payloadValue + `",
		"run_created_by": "` + payloadValue + `",
		"workspace_name": "example-workspace",
		"organization_name": "example-org",
		"notifications": [{"message": "` + payloadValue + `", "run_status": "planned", "run_updated_by": "` + payloadValue + `"}]
	}`
	if err := json.Unmarshal([]byte(payload), n); err != nil {
		t.Fatalf("test setup: %v", err)
	}

	done := captureOutput(t)
	h.processNotification(context.Background(), n)
	out := done()

	if strings.Contains(out, payloadValue) {
		t.Error("the notification payload was written to the logs or stdout")
	}
	if !strings.Contains(out, "run-abc123") {
		t.Errorf("expected a debug line naming the run, got:\n%s", out)
	}
}

func TestRunTaskHandler_DoesNotLogPayload(t *testing.T) {
	body := `{"payload_version":1,"access_token":"` + payloadValue + `","run_id":"run-abc123","run_message":"` + payloadValue + `","workspace_name":"example-workspace"}`
	req := httptest.NewRequest(http.MethodPost, "/hooks/tfc/run_task", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)

	done := captureOutput(t)
	err := RunTaskHandler(c)
	out := done()

	if err != nil {
		t.Fatalf("RunTaskHandler() error = %v", err)
	}
	if strings.Contains(out, payloadValue) {
		t.Error("the run task payload was written to the logs or stdout")
	}
	if !strings.Contains(out, "run-abc123") {
		t.Errorf("expected a debug line naming the run, got:\n%s", out)
	}
}
