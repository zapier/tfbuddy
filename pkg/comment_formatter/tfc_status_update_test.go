package comment_formatter

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/hashicorp/go-tfe"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.uber.org/mock/gomock"

	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
)

// planValue stands in for a sensitive value inside a plan: a variable, a
// resource attribute or a signed URL. It must never reach the logs.
const planValue = "plan-value-that-must-not-be-logged"

const testPlanJSON = `{
  "format_version": "1.2",
  "terraform_version": "1.9.0",
  "variables": {"example_input": {"value": "` + planValue + `"}},
  "resource_changes": [
    {
      "address": "terraform_data.example",
      "mode": "managed",
      "type": "terraform_data",
      "name": "example",
      "provider_name": "terraform.io/builtin/terraform",
      "change": {
        "actions": ["create"],
        "before": null,
        "after": {"input": "` + planValue + `"},
        "after_unknown": {},
        "before_sensitive": false,
        "after_sensitive": {"input": true}
      }
    }
  ]
}`

// captureLogs sends every log line, at every level, to a buffer for the
// rest of the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	prevLevel := zerolog.GlobalLevel()
	prevLogger := log.Logger
	t.Cleanup(func() {
		zerolog.SetGlobalLevel(prevLevel)
		log.Logger = prevLogger
	})
	zerolog.SetGlobalLevel(zerolog.TraceLevel)
	buf := &bytes.Buffer{}
	log.Logger = zerolog.New(buf)
	return buf
}

func plannedAndFinishedRun() *tfe.Run {
	return &tfe.Run{
		ID:     "run-abc123",
		Status: tfe.RunPlannedAndFinished,
		Workspace: &tfe.Workspace{
			Name:         "example-workspace",
			Organization: &tfe.Organization{Name: "example-org"},
		},
		Plan: &tfe.Plan{
			ID:                "plan-abc123",
			HasChanges:        true,
			ResourceAdditions: 1,
			LogReadURL:        "https://archivist.example.com/v1/object/" + planValue,
		},
		Apply: &tfe.Apply{},
	}
}

func TestFormatRunStatusCommentBody_PlanIsNeverLogged(t *testing.T) {
	logs := captureLogs(t)

	ctrl := gomock.NewController(t)
	tfc := mocks.NewMockApiClient(ctrl)
	tfc.EXPECT().GetPlanOutput("plan-abc123").Return([]byte(testPlanJSON), nil)

	rmd := &runstream.TFRunMetadata{Action: runstream.PlanAction}
	main, _, _ := FormatRunStatusCommentBody(config.Config{}, tfc, plannedAndFinishedRun(), rmd)

	if !strings.Contains(main, "<code>terraform_data.example</code>") {
		t.Errorf("comment is missing the planned resource address:\n%s", main)
	}
	if !strings.Contains(main, "1 to add") {
		t.Errorf("comment is missing the plan summary:\n%s", main)
	}
	if strings.Contains(main, planValue) {
		t.Error("comment contains a value from the plan")
	}
	if strings.Contains(logs.String(), planValue) {
		t.Error("a value from the plan, or the plan itself, was written to the logs")
	}
}

func TestFormatRunStatusCommentBody_PlanFetchErrorIsLoggedWithoutPlan(t *testing.T) {
	logs := captureLogs(t)

	ctrl := gomock.NewController(t)
	tfc := mocks.NewMockApiClient(ctrl)
	tfc.EXPECT().GetPlanOutput("plan-abc123").Return(nil, errors.New("resource not found"))

	rmd := &runstream.TFRunMetadata{Action: runstream.PlanAction}
	FormatRunStatusCommentBody(config.Config{}, tfc, plannedAndFinishedRun(), rmd)

	out := logs.String()
	if !strings.Contains(out, "could not get plan JSON") || !strings.Contains(out, "plan-abc123") {
		t.Errorf("expected an error line naming the plan, got:\n%s", out)
	}
	if strings.Contains(out, planValue) {
		t.Error("the run's plan object was written to the logs")
	}
}
