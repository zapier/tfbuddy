package tfc_trigger

//go:generate mockgen -source interfaces.go -destination=../mocks/mock_tfc_trigger.go -package=mocks github.com/zapier/tfbuddy/pkg/tfc_trigger
type Trigger interface {
	TriggerTFCEvents() (*TriggeredTFCWorkspaces, error)
	GetConfig() TriggerConfig
	TriggerCleanupEvent() error
}
type TriggerAction int
type TriggerSource int
type TriggerConfig interface {
	GetAction() TriggerAction
	SetAction(action TriggerAction)
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
	SetWorkspace(workspace string)
	GetVcsProvider() string
}
