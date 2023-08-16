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
