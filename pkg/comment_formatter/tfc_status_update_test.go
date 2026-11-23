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

// estimateRead is what GetCostEstimate returns. nil means it must not be called.
type estimateRead struct {
	ce  *tfe.CostEstimate
	err error
}

func formatWithCostEstimate(t *testing.T, minDelta int, run *tfe.Run, read *estimateRead) string {
	t.Helper()
	ctrl := gomock.NewController(t)
	tfc := mocks.NewMockApiClient(ctrl)
	tfc.EXPECT().GetPlanOutput("plan-abc123").Return([]byte(testPlanJSON), nil)
	if read != nil {
		tfc.EXPECT().GetCostEstimate(gomock.Any(), "ce-abc123").Return(read.ce, read.err)
	}

	rmd := &runstream.TFRunMetadata{Action: runstream.PlanAction}
	main, _, _ := FormatRunStatusCommentBody(config.Config{CostEstimateMinDelta: minDelta}, tfc, run, rmd)
	return main
}

func TestFormatRunStatusCommentBody_CostEstimate(t *testing.T) {
	finished := func(delta string) *estimateRead {
		return &estimateRead{ce: &tfe.CostEstimate{ID: "ce-abc123", Status: tfe.CostEstimateFinished, DeltaMonthlyCost: delta}}
	}
	withStatus := func(status tfe.CostEstimateStatus) *estimateRead {
		return &estimateRead{ce: &tfe.CostEstimate{ID: "ce-abc123", Status: status}}
	}
	tests := []struct {
		name       string
		minDelta   int
		noEstimate bool // the run has no cost estimate, as in orgs without cost estimation
		emptyID    bool
		read       *estimateRead
		want       string // empty: no cost line
		wantLog    string
	}{
		{name: "increase", minDelta: 10, read: finished("1582.56"), want: "**Cost estimate:** +$1,582.56/mo at list price"},
		{name: "decrease", minDelta: 10, read: finished("-4410.72"), want: "**Cost estimate:** -$4,410.72/mo at list price"},
		{name: "exactly the minimum", minDelta: 10, read: finished("10.0"), want: "**Cost estimate:** +$10.00/mo"},
		{name: "below the minimum", minDelta: 10, read: finished("-9.99")},
		{name: "no cost change", minDelta: 10, read: finished("0.0")},
		{name: "delta isn't a number", minDelta: 10, read: finished("")},
		{name: "skipped for a targeted plan", minDelta: 10, read: withStatus(tfe.CostEstimateSkippedDueToTargeting)},
		{name: "still pending", minDelta: 10, read: withStatus(tfe.CostEstimatePending)},
		{name: "errored estimate", minDelta: 10, read: withStatus(tfe.CostEstimateErrored)},
		{name: "estimate can't be read", minDelta: 10, read: &estimateRead{err: errors.New("resource not found")}, wantLog: "could not get cost estimate"},
		{name: "empty estimate", minDelta: 10, read: &estimateRead{}},
		{name: "org without cost estimation", minDelta: 10, noEstimate: true},
		{name: "estimate without an ID", minDelta: 10, emptyID: true},
		{name: "turned off", minDelta: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			run := plannedAndFinishedRun()
			switch {
			case tt.noEstimate:
				run.CostEstimate = nil
			case tt.emptyID:
				run.CostEstimate = &tfe.CostEstimate{}
			default:
				run.CostEstimate = &tfe.CostEstimate{ID: "ce-abc123"}
			}
			main := formatWithCostEstimate(t, tt.minDelta, run, tt.read)

			// The rest of the comment is unchanged whatever happens to the estimate.
			if !strings.Contains(main, "<code>terraform_data.example</code>") || !strings.Contains(main, "tfc apply") {
				t.Errorf("comment is missing the plan or the apply instructions:\n%s", main)
			}
			if tt.wantLog != "" && !strings.Contains(logs.String(), tt.wantLog) {
				t.Errorf("logs are missing %q:\n%s", tt.wantLog, logs.String())
			}
			if tt.want == "" {
				if strings.Contains(main, "Cost estimate") {
					t.Errorf("comment has a cost line, want none:\n%s", main)
				}
				return
			}
			if !strings.Contains(main, tt.want) {
				t.Errorf("comment is missing %q:\n%s", tt.want, main)
			}
			if strings.Index(main, "Cost estimate") > strings.Index(main, "tfc apply") {
				t.Errorf("cost line should come before the apply instructions:\n%s", main)
			}
		})
	}
}

func TestFormatRunStatusCommentBody_NoCostEstimateWithoutChanges(t *testing.T) {
	run := plannedAndFinishedRun()
	run.Plan.HasChanges = false
	run.Plan.ResourceAdditions = 0
	run.CostEstimate = &tfe.CostEstimate{ID: "ce-abc123"}

	// The mock fails the test if the estimate is read.
	main := formatWithCostEstimate(t, 10, run, nil)
	if strings.Contains(main, "Cost estimate") {
		t.Errorf("comment has a cost line for a plan without changes:\n%s", main)
	}
}

func TestFormatUSD(t *testing.T) {
	tests := map[float64]string{
		0.004:       "+$0.00",
		12.5:        "+$12.50",
		-354.96:     "-$354.96",
		999.999:     "+$1,000.00",
		1582.56:     "+$1,582.56",
		-1234567.89: "-$1,234,567.89",
	}
	for in, want := range tests {
		if got := formatUSD(in); got != want {
			t.Errorf("formatUSD(%v) = %q, want %q", in, got, want)
		}
	}
}
