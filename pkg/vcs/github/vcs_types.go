package github

import (
	"fmt"

	gogithub "github.com/google/go-github/v69/github"
	"github.com/zapier/tfbuddy/pkg/vcs"
)

// ----------------------------------------------------------------------------
// ensure type complies with interface
var _ vcs.MRDiscussion = (*GithubPRIssueComment)(nil)

type GithubPRIssueComment struct {
	*gogithub.IssueComment
}

func (c *GithubPRIssueComment) GetDiscussionID() string {
	return fmt.Sprintf("%d", *c.ID)
}

func (c *GithubPRIssueComment) GetMRNotes() []vcs.MRNote {
	return nil
}

// ----------------------------------------------------------------------------

// ensure type complies with interface
var _ vcs.MR = (*GithubPR)(nil)

type GithubPR struct {
	*gogithub.PullRequest
}

func (gm *GithubPR) HasConflicts() bool {
	// https://docs.github.com/en/graphql/reference/enums#mergeablestate
	return !gm.PullRequest.GetMergeable() // TODO: does this really represent HasConflicts?
}
func (gm *GithubPR) GetSourceBranch() string {
	return gm.PullRequest.GetHead().GetRef()
}
func (gm *GithubPR) GetInternalID() int {
	return *gm.PullRequest.Number // TODO: which ID to use
}
func (gm *GithubPR) GetWebURL() string {
	return gm.PullRequest.GetHTMLURL()
}
func (gm *GithubPR) GetAuthor() vcs.MRAuthor {
	return &GithubPRAuthor{gm.GetUser()}
}
func (gm *GithubPR) GetTitle() string {
	return gm.PullRequest.GetTitle()
}
func (gm *GithubPR) GetTargetBranch() string {
	return gm.PullRequest.GetBase().GetRef()
}
func (gm *GithubPR) IsApproved() bool {
	return *gm.MergeableState != "blocked"
}

// ----------------------------------------------------------------------------
// ensure type complies with interface
var _ vcs.MRAuthor = (*GithubPRAuthor)(nil)

type GithubPRAuthor struct {
	*gogithub.User
}

func (ga *GithubPRAuthor) GetUsername() string {
	return ga.User.GetLogin()
}

// ----------------------------------------------------------------------------
// ensure type complies with interface
var _ vcs.MRDiscussion = (*IssueComment)(nil)

type IssueComment struct {
	*gogithub.IssueComment
}

func (c *IssueComment) GetNoteID() int64 {
	return *c.ID
}
func (c *IssueComment) GetDiscussionID() string {
	return fmt.Sprintf("%d", *c.ID)
}

func (c *IssueComment) GetMRNotes() []vcs.MRNote {
	return []vcs.MRNote{}
}

// ----------------------------------------------------------------------------
// ensure type complies with interface
var _ vcs.MRApproved = (*PRApproved)(nil)

type PRApproved struct {
	approvalStatus bool
}

func (p *PRApproved) IsApproved() bool {
	return p.approvalStatus
}
