package vcs

import "time"

// ClassStatusName is the commit status TFBuddy posts on the head commit of a
// Mode 2 merge request. It starts with CommitStatusPrefix, so the pipeline gate
// treats it as one of TFBuddy's own statuses and never waits on it.
const ClassStatusName = CommitStatusPrefix + "class"

// MRCommit is one commit on a merge request. The emails are git metadata
// declared by whoever made the commit, not a GitLab identity.
type MRCommit struct {
	SHA            string
	AuthorEmail    string
	CommitterEmail string
}

// MRApprovalState is a merge request's approvals, with the time of each.
type MRApprovalState struct {
	// Approved reports whether every approval rule is satisfied.
	Approved   bool
	ApprovedBy []MRApproval
}

// MRApproval is one approval. ApprovedAt is nil when GitLab did not report it.
type MRApproval struct {
	Username   string
	ApprovedAt *time.Time
}

// MRVersion is one diff version of a merge request. GitLab creates a version
// for every push, so a version's CreatedAt is when its head was pushed.
type MRVersion struct {
	HeadSHA   string
	CreatedAt time.Time
}

// ClassStatus is the TFC/class commit status of a Mode 2 merge request.
type ClassStatus struct {
	Project         string // path with namespace
	CommitSHA       string
	MergeRequestIID int
	State           CommitState
	Description     string
}
