package vcs

// CommitState is the provider-agnostic state of a TFBuddy commit status.
// GitLab maps these onto gogitlab.BuildStateValue. The set is deliberately
// limited to the values GitLab's commit status API accepts:
//
//	values: %w[pending running success failed canceled skipped]
type CommitState string

const (
	CommitStatePending  CommitState = "pending"
	CommitStateRunning  CommitState = "running"
	CommitStateSuccess  CommitState = "success"
	CommitStateFailed   CommitState = "failed"
	CommitStateCanceled CommitState = "canceled"
	CommitStateSkipped  CommitState = "skipped"
)

// WorkspaceStatus is one TFC/<action>/<workspace> commit status. It carries
// everything a VCS needs to place the status on the right pipeline without
// exposing provider types to callers in pkg/tfc_trigger.
type WorkspaceStatus struct {
	Project         string // path with namespace
	CommitSHA       string
	MergeRequestIID int
	Workspace       string
	Action          string // "plan" or "apply"
	State           CommitState
	Description     string // optional; falls back to a per-state default
	TargetURL       string // optional; the TFC run URL once one exists
}
