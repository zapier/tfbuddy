package runstream

import (
	"context"
	"time"
)

//go:generate mockgen -source interfaces.go -destination=../mocks/mock_runstream.go -package=mocks github.com/zapier/tfbuddy/pkg/runstream

type StreamClient interface {
	HealthCheck() error
	PublishTFRunEvent(ctx context.Context, re RunEvent) error
	AddRunMeta(rmd RunMetadata) error
	GetRunMeta(runID string) (RunMetadata, error)
	EnsureAutoMergeState(state *AutoMergeState) error
	BeginAutoMergeApply(ref AutoMergeRef, workspaces []string) error
	RegisterAutoMergeRun(ref AutoMergeRef) error
	RecordAutoMergeSuccess(ref AutoMergeRef) (bool, error)
	ReleaseAutoMergeClaim(ref AutoMergeRef) error
	RecordAutoMergeRequested(ref AutoMergeRef) error
	FinalizeAutoMerge(ref AutoMergeRef, merged bool) (AutoMergeResult, bool, error)
	ClaimWorkspaceDispatch(claim WorkspaceDispatchClaim) (bool, error)
	RecordRunFinished(provider string, re RunEvent)
	// MarkAutoModeManual records that a merge request runs in Manual Mode until
	// it merges, and reports whether this call made the record.
	MarkAutoModeManual(ref AutoModeMRRef, reason string) (bool, error)
	// IsAutoModeManual reports whether a merge request has fallen back to
	// Manual Mode, and why.
	IsAutoModeManual(ref AutoModeMRRef) (bool, string, error)
	// DeleteAutoModeMRState forgets a merged merge request's Auto Mode state.
	DeleteAutoModeMRState(ref AutoModeMRRef) error
	// GetAutoModeSummaryNote and SetAutoModeSummaryNote keep the ID of the MR
	// comment that summarizes its class plans.
	GetAutoModeSummaryNote(ref AutoModeMRRef) (int64, error)
	SetAutoModeSummaryNote(ref AutoModeMRRef, noteID int64) error
	// BeginClassPlans, RegisterClassPlanRun and RecordClassPlanResult keep the
	// outcome of each class plan of an Auto Mode commit.
	BeginClassPlans(ref ClassPlanRef, class string, workspaces []string) error
	RegisterClassPlanRun(ref ClassPlanRef, workspace, runID string) error
	RecordClassPlanResult(ref ClassPlanRef, workspace, runID string, passed bool, detail string) (*ClassPlanState, bool, error)
	NewTFRunPollingTask(meta RunMetadata, delay time.Duration) RunPollingTask
	SubscribeTFRunPollingTasks(cb func(task RunPollingTask) bool) (closer func(), err error)
	SubscribeTFRunEvents(queue string, cb func(run RunEvent) bool) (closer func(), err error)
}

type RunEvent interface {
	GetRunID() string
	GetContext() context.Context
	SetContext(context.Context)
	SetCarrier(map[string]string)
	GetNewStatus() string
	GetMetadata() RunMetadata
	SetMetadata(RunMetadata)
}

type RunMetadata interface {
	GetAction() string
	GetMRInternalID() int
	GetRootNoteID() int64
	GetMRProjectNameWithNamespace() string
	GetDiscussionID() string
	GetRunID() string
	GetWorkspace() string
	GetCommitSHA() string
	GetOrganization() string
	GetVcsProvider() string
	GetAutoMerge() bool
	GetAutoMergeGeneration() string
	GetAutoMergeSequence() int64
	GetChangeClass() string
	GetClassWorkspaces() []string
}

type RunPollingTask interface {
	Schedule(ctx context.Context) error
	Reschedule(ctx context.Context) error
	Completed() error
	GetRunID() string
	GetContext() context.Context
	SetCarrier(map[string]string)
	GetLastStatus() string
	SetLastStatus(string)
	GetRunMetaData() RunMetadata
}
