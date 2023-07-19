package vcs

//go:generate mockgen -source interfaces.go -destination=../mocks/mock_vcs.go -package=mocks github.com/zapier/tfbuddy/pkg/vcs

type GitClient interface {
	GetMergeRequestApprovals(id int, project string) (MRApproved, error)
	CreateMergeRequestComment(id int, fullPath string, comment string) error
	CreateMergeRequestDiscussion(mrID int, fullPath string, comment string) (MRDiscussionNotes, error)
	GetMergeRequest(int, string) (DetailedMR, error)
	GetRepoFile(string, string, string) ([]byte, error)
	GetMergeRequestModifiedFiles(mrIID int, projectID string) ([]string, error)
	CloneMergeRequest(string, MR, string) (GitRepo, error)
	UpdateMergeRequestDiscussionNote(mrIID, noteID int, project, discussionID, comment string) (MRNote, error)
	ResolveMergeRequestDiscussion(string, int, string) error
	AddMergeRequestDiscussionReply(mrIID int, project, discussionID, comment string) (MRNote, error)
	SetCommitStatus(projectWithNS string, commitSHA string, status CommitStatusOptions) (CommitStatus, error)
	GetPipelinesForCommit(projectWithNS string, commitSHA string) ([]ProjectPipeline, error)
	GetOldRunUrls(mrIID int, project string, rootCommentID int) (string, error)
}
type GitRepo interface {
	FetchUpstreamBranch(string) error
	GetMergeBase(oldest, newest string) (string, error)
	GetModifiedFileNamesBetweenCommits(oldest, newest string) ([]string, error)
	GetLocalDirectory() string
}
type MRApproved interface {
	IsApproved() bool
}

type MRDiscussion interface {
	GetDiscussionID() string
}
type MRDiscussionNotes interface {
	GetMRNotes() []MRNote
	MRDiscussion
}
type MRNote interface {
	GetNoteID() int64
}
type DetailedMR interface {
	HasConflicts() bool
	MR
	GetWebURL() string
	GetTitle() string
}
type MR interface {
	MRBranches
	GetAuthor() MRAuthor
	GetInternalID() int
}
type MRBranches interface {
	GetSourceBranch() string
	GetTargetBranch() string
}
type MRAuthor interface {
	GetUsername() string
}

type CommitStatusOptions interface {
	GetName() string
	GetContext() string
	GetTargetURL() string
	GetDescription() string
	GetState() string
	GetPipelineID() int
}

type CommitStatus interface {
	Info() string
}

type ProjectPipeline interface {
	GetSource() string
	GetID() int
}

type Project interface {
	GetPathWithNamespace() string
}
type MRCommentEvent interface {
	GetProject() Project
	GetMR() MR
	GetAttributes() MRAttributes
	GetLastCommit() Commit
}

type MRAttributes interface {
	GetNote() string
	GetType() string
	MRDiscussion
}

type Commit interface {
	GetSHA() string
}
