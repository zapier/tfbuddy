package tfc_trigger

import "context"

//go:generate mockgen -source interfaces.go -destination=../mocks/mock_tfc_trigger.go -package=mocks github.com/zapier/tfbuddy/pkg/tfc_trigger
type Trigger interface {
	TriggerTFCEvents(context.Context) (*TriggeredTFCWorkspaces, error)
	TriggerCleanupEvent(context.Context) error
	GetAction() TriggerAction
	GetBranch() string
	GetCommitSHA() string
	GetProjectNameWithNamespace() string
	GetMergeRequestIID() int
	GetMergeRequestDiscussionID() string
	SetMergeRequestDiscussionID(mrdisID string)
	GetMergeRequestRootNoteID() int64
	SetMergeRequestRootNoteID(id int64)
	GetTriggerSource() TriggerSource
	GetWorkspace() string
	GetVcsProvider() string
}
type TriggerAction int
type TriggerSource int
