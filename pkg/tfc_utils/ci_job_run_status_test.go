package tfc_utils

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/go-tfe"
)

func Test_isRunning(t *testing.T) {
	tests := []struct {
		name string
		run  *tfe.Run
		want bool
	}{
		{
			name: "nil run",
			run:  nil,
			want: false,
		},
		{
			name: "pending status (running)",
			run:  &tfe.Run{Status: "pending"},
			want: true,
		},
		{
			name: "planning status (running)",
			run:  &tfe.Run{Status: "planning"},
			want: true,
		},
		{
			name: "applying status (running)",
			run:  &tfe.Run{Status: "applying"},
			want: true,
		},
		// Test key differences from isUnfinishedRun - these are NOT running but ARE unfinished
		{
			name: "planned status (not running but unfinished)",
			run:  &tfe.Run{Status: "planned"},
			want: false,
		},
		{
			name: "confirmed status (not running but unfinished)",
			run:  &tfe.Run{Status: "confirmed"},
			want: false,
		},
		{
			name: "policy_checked status (not running but unfinished)",
			run:  &tfe.Run{Status: "policy_checked"},
			want: false,
		},
		{
			name: "applied status (finished)",
			run:  &tfe.Run{Status: "applied"},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRunning(tt.run); got != tt.want {
				t.Errorf("isRunning() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_extractRunIDFromURL(t *testing.T) {
	tests := []struct {
		name      string
		targetURL string
		want      string
	}{
		{
			name:      "valid TFC run URL",
			targetURL: "https://app.terraform.io/app/zapier/workspaces/my-workspace/runs/run-abc123def456",
			want:      "run-abc123def456",
		},
		{
			name:      "another valid TFC run URL",
			targetURL: "https://app.terraform.io/app/org/workspaces/workspace/runs/run-xyz789",
			want:      "run-xyz789",
		},
		{
			name:      "URL with trailing slash",
			targetURL: "https://app.terraform.io/app/org/workspaces/workspace/runs/run-123/",
			want:      "",
		},
		{
			name:      "empty URL",
			targetURL: "",
			want:      "",
		},
		{
			name:      "malformed URL",
			targetURL: "not-a-valid-url",
			want:      "not-a-valid-url",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the URL parsing logic from MonitorRunStatus
			urlParts := strings.SplitAfter(tt.targetURL, "/")
			var got string
			if len(urlParts) > 0 {
				got = urlParts[len(urlParts)-1]
			}
			if got != tt.want {
				t.Errorf("extractRunIDFromURL() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_formatSuccessPlanSummary(t *testing.T) {
	tests := []struct {
		name         string
		additions    int
		changes      int
		destructions int
		want         string
	}{
		{
			name:         "basic plan summary",
			additions:    5,
			changes:      3,
			destructions: 1,
			want: `
  * Additions: 5
  * Changes: 3
  * Destructions: 1

*Click Terraform Cloud URL to see detailed plan output*

**Merge MR to apply changes**
`,
		},
		{
			name:         "no changes",
			additions:    0,
			changes:      0,
			destructions: 0,
			want: `
  * Additions: 0
  * Changes: 0
  * Destructions: 0

*Click Terraform Cloud URL to see detailed plan output*

**Merge MR to apply changes**
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fmt.Sprintf(successPlanSummaryFormat, tt.additions, tt.changes, tt.destructions)
			if got != tt.want {
				t.Errorf("formatSuccessPlanSummary() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_formatMRRunDetails(t *testing.T) {
	tests := []struct {
		name        string
		workspace   string
		status      string
		targetURL   string
		description string
		want        string
	}{
		{
			name:        "basic run details",
			workspace:   "my-workspace",
			status:      "planned",
			targetURL:   "https://app.terraform.io/app/org/workspaces/my-workspace/runs/run-123",
			description: "Some description",
			want: `
#### Workspace: my-workspace

**Status**: planned

[https://app.terraform.io/app/org/workspaces/my-workspace/runs/run-123](https://app.terraform.io/app/org/workspaces/my-workspace/runs/run-123)

Some description
---
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fmt.Sprintf(MR_RUN_DETAILS_FORMAT, tt.workspace, tt.status, tt.targetURL, tt.targetURL, tt.description)
			if got != tt.want {
				t.Errorf("formatMRRunDetails() = %v, want %v", got, tt.want)
			}
		})
	}
}
