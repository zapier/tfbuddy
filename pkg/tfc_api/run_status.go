package tfc_api

import (
	"slices"

	"github.com/hashicorp/go-tfe"
)

// finalRunStatuses are the statuses a run does not leave. policy_soft_failed
// is final for the plan-only runs that reach it; a run that can still be
// applied goes to policy_override instead.
// https://developer.hashicorp.com/terraform/cloud-docs/api-docs/run#run-states
var finalRunStatuses = []tfe.RunStatus{
	tfe.RunApplied,
	tfe.RunPlannedAndFinished,
	tfe.RunPlannedAndSaved,
	tfe.RunDiscarded,
	tfe.RunErrored,
	tfe.RunCanceled,
	tfe.RunPolicySoftFailed,
}

// IsFinalRunStatus reports whether a run in this status has finished and
// will not change status again.
func IsFinalRunStatus(status tfe.RunStatus) bool {
	return slices.Contains(finalRunStatuses, status)
}

// FinalRunStatuses returns every final run status.
func FinalRunStatuses() []tfe.RunStatus {
	return slices.Clone(finalRunStatuses)
}

// IsStaleRunEvent reports whether a run event reporting eventStatus is out of
// date, given the run's current status. Run events can be delivered late and
// out of order, so an event for an earlier status can arrive after the run
// has finished. Acting on it would report a finished run as still in
// progress, and nothing would correct it afterwards.
func IsStaleRunEvent(current tfe.RunStatus, eventStatus string) bool {
	return IsFinalRunStatus(current) && current != tfe.RunStatus(eventStatus)
}
