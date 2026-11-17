package vcs

// ClassStatusName is the commit status TFBuddy posts on the head commit of a
// Auto Mode merge request. It starts with CommitStatusPrefix, so the pipeline gate
// treats it as one of TFBuddy's own statuses and never waits on it.
const ClassStatusName = CommitStatusPrefix + "class"

// MRCommit is one commit on a merge request. The emails are git metadata
// declared by whoever made the commit, not a GitLab identity.
type MRCommit struct {
	SHA            string
	AuthorEmail    string
	CommitterEmail string
}

// ClassStatus is the TFC/class commit status of an Auto Mode merge request.
type ClassStatus struct {
	Project         string // path with namespace
	CommitSHA       string
	MergeRequestIID int
	State           CommitState
	Description     string
}
