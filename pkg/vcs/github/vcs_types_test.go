package github

import (
	"testing"

	gogithub "github.com/google/go-github/v69/github"
	"github.com/stretchr/testify/assert"
)

func TestGithubPRIssueComment_GetMRNotes(t *testing.T) {
	comment := &GithubPRIssueComment{}

	result := comment.GetMRNotes()
	assert.Nil(t, result)
}

func TestGithubPRIssueComment_GetDiscussionID(t *testing.T) {
	tests := []struct {
		name     string
		comment  *GithubPRIssueComment
		expected string
	}{
		{
			name:     "nil IssueComment",
			comment:  &GithubPRIssueComment{},
			expected: "",
		},
		{
			name: "nil ID",
			comment: &GithubPRIssueComment{
				IssueComment: &gogithub.IssueComment{},
			},
			expected: "",
		},
		{
			name: "valid ID",
			comment: &GithubPRIssueComment{
				IssueComment: &gogithub.IssueComment{
					ID: int64Ptr(123),
				},
			},
			expected: "123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.comment.GetDiscussionID()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGithubPR_GetSourceBranch(t *testing.T) {
	tests := []struct {
		name     string
		pr       *GithubPR
		expected string
	}{
		{
			name: "valid ref",
			pr: &GithubPR{
				PullRequest: &gogithub.PullRequest{
					Head: &gogithub.PullRequestBranch{
						Ref: stringPtr("feature-branch"),
					},
				},
			},
			expected: "feature-branch",
		},
		{
			name: "nil head",
			pr: &GithubPR{
				PullRequest: &gogithub.PullRequest{},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.pr.GetSourceBranch()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGithubPR_GetInternalID(t *testing.T) {
	number := 42
	pr := &GithubPR{
		PullRequest: &gogithub.PullRequest{
			Number: &number,
		},
	}

	result := pr.GetInternalID()
	assert.Equal(t, 42, result)
}

func TestGithubPR_GetWebURL(t *testing.T) {
	url := "https://github.com/owner/repo/pull/42"
	pr := &GithubPR{
		PullRequest: &gogithub.PullRequest{
			HTMLURL: &url,
		},
	}

	result := pr.GetWebURL()
	assert.Equal(t, "https://github.com/owner/repo/pull/42", result)
}

func TestGithubPR_GetTitle(t *testing.T) {
	title := "Fix bug in authentication"
	pr := &GithubPR{
		PullRequest: &gogithub.PullRequest{
			Title: &title,
		},
	}

	result := pr.GetTitle()
	assert.Equal(t, "Fix bug in authentication", result)
}

func TestGithubPR_GetTargetBranch(t *testing.T) {
	ref := "main"
	pr := &GithubPR{
		PullRequest: &gogithub.PullRequest{
			Base: &gogithub.PullRequestBranch{
				Ref: &ref,
			},
		},
	}

	result := pr.GetTargetBranch()
	assert.Equal(t, "main", result)
}

func TestGithubPR_HasConflicts(t *testing.T) {
	tests := []struct {
		name      string
		mergeable *bool
		expected  bool
	}{
		{
			name:      "mergeable true",
			mergeable: boolPtr(true),
			expected:  false,
		},
		{
			name:      "mergeable false",
			mergeable: boolPtr(false),
			expected:  true,
		},
		{
			name:      "mergeable nil",
			mergeable: nil,
			expected:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := &GithubPR{
				PullRequest: &gogithub.PullRequest{
					Mergeable: tt.mergeable,
				},
			}

			result := pr.HasConflicts()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGithubPR_IsApproved(t *testing.T) {
	mergeableState := "clean"
	pr := &GithubPR{
		PullRequest: &gogithub.PullRequest{
			MergeableState: &mergeableState,
		},
	}

	result := pr.IsApproved()
	assert.True(t, result)

	// Test blocked state
	blockedState := "blocked"
	pr.MergeableState = &blockedState
	result = pr.IsApproved()
	assert.False(t, result)
}

func TestGithubPR_GetAuthor(t *testing.T) {
	login := "testuser"
	pr := &GithubPR{
		PullRequest: &gogithub.PullRequest{
			User: &gogithub.User{
				Login: &login,
			},
		},
	}

	author := pr.GetAuthor()
	assert.NotNil(t, author)
	assert.Equal(t, "testuser", author.GetUsername())
}

func TestGithubPRAuthor_GetUsername(t *testing.T) {
	login := "testuser"
	author := &GithubPRAuthor{
		User: &gogithub.User{
			Login: &login,
		},
	}

	result := author.GetUsername()
	assert.Equal(t, "testuser", result)
}

func TestIssueComment_GetNoteID(t *testing.T) {
	tests := []struct {
		name     string
		comment  *IssueComment
		expected int64
	}{
		{
			name:     "nil IssueComment",
			comment:  &IssueComment{},
			expected: 0,
		},
		{
			name: "nil ID",
			comment: &IssueComment{
				IssueComment: &gogithub.IssueComment{},
			},
			expected: 0,
		},
		{
			name: "valid ID",
			comment: &IssueComment{
				IssueComment: &gogithub.IssueComment{
					ID: int64Ptr(456),
				},
			},
			expected: 456,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.comment.GetNoteID()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIssueComment_GetDiscussionID(t *testing.T) {
	tests := []struct {
		name     string
		comment  *IssueComment
		expected string
	}{
		{
			name:     "nil IssueComment",
			comment:  &IssueComment{},
			expected: "",
		},
		{
			name: "nil ID",
			comment: &IssueComment{
				IssueComment: &gogithub.IssueComment{},
			},
			expected: "",
		},
		{
			name: "valid ID",
			comment: &IssueComment{
				IssueComment: &gogithub.IssueComment{
					ID: int64Ptr(789),
				},
			},
			expected: "789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.comment.GetDiscussionID()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIssueComment_GetMRNotes(t *testing.T) {
	comment := &IssueComment{}

	result := comment.GetMRNotes()
	assert.NotNil(t, result)
	assert.Len(t, result, 0)
}

func TestPRApproved_IsApproved(t *testing.T) {
	tests := []struct {
		name     string
		approved bool
	}{
		{
			name:     "approved",
			approved: true,
		},
		{
			name:     "not approved",
			approved: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := &PRApproved{
				approvalStatus: tt.approved,
			}

			result := pr.IsApproved()
			assert.Equal(t, tt.approved, result)
		})
	}
}

// Helper function to create bool pointer
func boolPtr(b bool) *bool {
	return &b
}

// Helper function to create int64 pointer
func int64Ptr(i int64) *int64 {
	return &i
}

// Helper function to create string pointer
func stringPtr(s string) *string {
	return &s
}
