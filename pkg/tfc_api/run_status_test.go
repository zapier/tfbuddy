package tfc_api

import (
	"testing"

	"github.com/hashicorp/go-tfe"
)

func TestIsStaleRunEvent(t *testing.T) {
	tests := []struct {
		name        string
		current     tfe.RunStatus
		eventStatus string
		want        bool
	}{
		{"earlier status after the plan finished", tfe.RunPlannedAndFinished, "planning", true},
		{"earlier status after policy checks finished", tfe.RunPlannedAndFinished, "policy_checking", true},
		{"earlier status after the apply finished", tfe.RunApplied, "applying", true},
		{"earlier status after the run errored", tfe.RunErrored, "planning", true},
		{"the final status itself", tfe.RunPlannedAndFinished, "planned_and_finished", false},
		{"a redelivered final status", tfe.RunApplied, "applied", false},
		{"the run is still in progress", tfe.RunPolicyChecking, "planning", false},
		{"waiting for confirmation is not final", tfe.RunPlanned, "planning", false},
		{"a policy override is not final", tfe.RunPolicyOverride, "policy_checking", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsStaleRunEvent(tt.current, tt.eventStatus); got != tt.want {
				t.Errorf("IsStaleRunEvent(%q, %q) = %v, want %v", tt.current, tt.eventStatus, got, tt.want)
			}
		})
	}
}
