package utils

import (
	"fmt"
	"testing"
)

func TestFormatTFBuddyMarker(t *testing.T) {
	tests := []struct {
		workspace string
		action    string
		want      string
	}{
		{"brave-phoenix", "plan", "<!-- tfbuddy:ws=brave-phoenix:action=plan -->"},
		{"brave-phoenix", "apply", "<!-- tfbuddy:ws=brave-phoenix:action=apply -->"},
		{"service-tfbuddy", "plan", "<!-- tfbuddy:ws=service-tfbuddy:action=plan -->"},
		{"service-tfbuddy-staging", "apply", "<!-- tfbuddy:ws=service-tfbuddy-staging:action=apply -->"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s/%s", tt.workspace, tt.action), func(t *testing.T) {
			got := FormatTFBuddyMarker(tt.workspace, tt.action)
			if got != tt.want {
				t.Fatalf("FormatTFBuddyMarker(%q, %q) = %q, want %q", tt.workspace, tt.action, got, tt.want)
			}
		})
	}
}

func TestParseTFBuddyMarker(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantWS    string
		wantAct   string
		wantFound bool
	}{
		{
			name:      "marker at beginning of body",
			body:      "<!-- tfbuddy:ws=brave-phoenix:action=plan -->\n### Terraform Cloud\n**Workspace**: `brave-phoenix`",
			wantWS:    "brave-phoenix",
			wantAct:   "plan",
			wantFound: true,
		},
		{
			name:      "marker at end of body",
			body:      "### Terraform Cloud\n**Workspace**: `brave-phoenix`\n<!-- tfbuddy:ws=brave-phoenix:action=plan -->",
			wantWS:    "brave-phoenix",
			wantAct:   "plan",
			wantFound: true,
		},
		{
			name:      "apply action",
			body:      "Starting TFC apply for Workspace: `zapier-test/brave-phoenix`.\n<!-- tfbuddy:ws=brave-phoenix:action=apply -->",
			wantWS:    "brave-phoenix",
			wantAct:   "apply",
			wantFound: true,
		},
		{
			name:      "workspace with hyphens",
			body:      "<!-- tfbuddy:ws=service-tfbuddy-staging:action=apply -->",
			wantWS:    "service-tfbuddy-staging",
			wantAct:   "apply",
			wantFound: true,
		},
		{
			name:      "no marker in body",
			body:      "### Terraform Cloud\n**Workspace**: `brave-phoenix`",
			wantWS:    "",
			wantAct:   "",
			wantFound: false,
		},
		{
			name:      "empty body",
			body:      "",
			wantWS:    "",
			wantAct:   "",
			wantFound: false,
		},
		{
			name:      "malformed marker - missing action",
			body:      "<!-- tfbuddy:ws=brave-phoenix -->",
			wantWS:    "",
			wantAct:   "",
			wantFound: false,
		},
		{
			name:      "malformed marker - empty workspace",
			body:      "<!-- tfbuddy:ws=:action=plan -->",
			wantWS:    "",
			wantAct:   "",
			wantFound: false,
		},
		{
			name:      "malformed marker - empty action",
			body:      "<!-- tfbuddy:ws=brave-phoenix:action= -->",
			wantWS:    "",
			wantAct:   "",
			wantFound: false,
		},
		{
			name:      "marker embedded in large body with old run urls",
			body:      "<details><summary>\n\n### Previous TFC Urls:\n\n</summary>\n\n| Run ID | Status | Created at |\n| ------ | ------ | ---------- |\n|[run-abc](url)|📝 Planned|2026-01-01|\n</details>\n\n\n### Terraform Cloud\n**Workspace**: `brave-phoenix`<br>\n**Command**: plan <br>\n**Status**: `planned_and_finished`<br>\n**Run URL**: [run-xyz](url) <br>\n\n<!-- tfbuddy:ws=brave-phoenix:action=plan -->",
			wantWS:    "brave-phoenix",
			wantAct:   "plan",
			wantFound: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ws, action, found := ParseTFBuddyMarker(tt.body)
			if found != tt.wantFound {
				t.Fatalf("ParseTFBuddyMarker() found = %v, want %v", found, tt.wantFound)
			}
			if ws != tt.wantWS {
				t.Fatalf("ParseTFBuddyMarker() workspace = %q, want %q", ws, tt.wantWS)
			}
			if action != tt.wantAct {
				t.Fatalf("ParseTFBuddyMarker() action = %q, want %q", action, tt.wantAct)
			}
		})
	}
}

func TestFormatThenParse_RoundTrip(t *testing.T) {
	workspaces := []string{"brave-phoenix", "service-tfbuddy", "service-tfbuddy-staging", "my-workspace-123"}
	actions := []string{"plan", "apply"}

	for _, ws := range workspaces {
		for _, action := range actions {
			t.Run(fmt.Sprintf("%s/%s", ws, action), func(t *testing.T) {
				marker := FormatTFBuddyMarker(ws, action)
				gotWS, gotAction, found := ParseTFBuddyMarker(marker)
				if !found {
					t.Fatal("expected marker to be found in formatted output")
				}
				if gotWS != ws {
					t.Fatalf("workspace = %q, want %q", gotWS, ws)
				}
				if gotAction != action {
					t.Fatalf("action = %q, want %q", gotAction, action)
				}
			})
		}
	}
}

func TestParseTFBuddyMarker_DistinguishesWorkspacesAndActions(t *testing.T) {
	bodyWSA := "content\n<!-- tfbuddy:ws=workspace-a:action=plan -->"
	bodyWSB := "content\n<!-- tfbuddy:ws=workspace-b:action=plan -->"
	bodyWSA_Apply := "content\n<!-- tfbuddy:ws=workspace-a:action=apply -->"

	wsA, actA, foundA := ParseTFBuddyMarker(bodyWSA)
	wsB, actB, foundB := ParseTFBuddyMarker(bodyWSB)
	wsAApply, actAApply, foundAApply := ParseTFBuddyMarker(bodyWSA_Apply)

	if !foundA || !foundB || !foundAApply {
		t.Fatal("all markers should be found")
	}

	if wsA == wsB {
		t.Fatal("workspace-a and workspace-b should be distinct")
	}
	if wsA != "workspace-a" || wsB != "workspace-b" {
		t.Fatalf("got ws %q and %q", wsA, wsB)
	}
	if actA != "plan" || actB != "plan" {
		t.Fatal("both should be plan")
	}
	if wsAApply != "workspace-a" || actAApply != "apply" {
		t.Fatalf("apply marker: ws=%q action=%q", wsAApply, actAApply)
	}
}
