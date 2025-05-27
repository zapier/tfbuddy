package gitlab

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zapier/tfbuddy/pkg/utils"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

func TestNewGitlabClient(t *testing.T) {
	tests := []struct {
		name      string
		envVars   map[string]string
		expectNil bool
	}{
		{
			name: "with GITLAB_TOKEN",
			envVars: map[string]string{
				"GITLAB_TOKEN": "test-token",
			},
			expectNil: false,
		},
		{
			name: "with GITLAB_ACCESS_TOKEN",
			envVars: map[string]string{
				"GITLAB_ACCESS_TOKEN": "test-token",
			},
			expectNil: false,
		},
		{
			name:      "without token",
			envVars:   map[string]string{},
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Unsetenv("GITLAB_TOKEN")
			os.Unsetenv("GITLAB_ACCESS_TOKEN")

			for k, v := range tt.envVars {
				os.Setenv(k, v)
			}

			client := NewGitlabClient()

			if tt.expectNil {
				assert.Nil(t, client)
			} else {
				assert.NotNil(t, client)
			}

			for k := range tt.envVars {
				os.Unsetenv(k)
			}
		})
	}
}

func TestCreateBackOffWithRetries(t *testing.T) {
	backoff := createBackOffWithRetries()
	require.NotNil(t, backoff)
}

func TestGitlabCommitStatusOptions(t *testing.T) {
	name := "test-name"
	ctx := "test-context"
	url := "http://example.com"
	desc := "test description"
	state := gogitlab.Success
	pipelineID := 123

	opts := &GitlabCommitStatusOptions{
		SetCommitStatusOptions: &gogitlab.SetCommitStatusOptions{
			Name:        &name,
			Context:     &ctx,
			TargetURL:   &url,
			Description: &desc,
			State:       state,
			PipelineID:  &pipelineID,
		},
	}

	assert.Equal(t, name, opts.GetName())
	assert.Equal(t, ctx, opts.GetContext())
	assert.Equal(t, url, opts.GetTargetURL())
	assert.Equal(t, desc, opts.GetDescription())
	assert.Equal(t, string(state), opts.GetState())
	assert.Equal(t, pipelineID, opts.GetPipelineID())
}

func TestGitlabCommitStatus(t *testing.T) {
	status := &GitlabCommitStatus{
		CommitStatus: &gogitlab.CommitStatus{
			Author: gogitlab.Author{
				Username: "testuser",
			},
			Name: "test-status",
			SHA:  "abc123",
		},
	}

	expected := "testuser test-status abc123"
	assert.Equal(t, expected, status.Info())
}

func TestGitlabMRNote(t *testing.T) {
	note := &GitlabMRNote{
		Note: &gogitlab.Note{
			ID: 456,
		},
	}

	assert.Equal(t, int64(456), note.GetNoteID())
}

func TestGitlabMRDiscussion(t *testing.T) {
	discussion := &GitlabMRDiscussion{
		Discussion: &gogitlab.Discussion{
			ID: "disc123",
			Notes: []*gogitlab.Note{
				{ID: 1},
				{ID: 2},
			},
		},
	}

	assert.Equal(t, "disc123", discussion.GetDiscussionID())

	notes := discussion.GetMRNotes()
	assert.Len(t, notes, 2)
	assert.Equal(t, int64(1), notes[0].GetNoteID())
	assert.Equal(t, int64(2), notes[1].GetNoteID())
}

func TestGitlabMR(t *testing.T) {
	mr := &GitlabMR{
		MergeRequest: &gogitlab.MergeRequest{
			BasicMergeRequest: gogitlab.BasicMergeRequest{
				IID:          123,
				SourceBranch: "feature",
				TargetBranch: "main",
				Title:        "Test MR",
				Author: &gogitlab.BasicUser{
					Username: "testuser",
				},
			},
		},
	}

	assert.Equal(t, 123, mr.GetInternalID())
	assert.Equal(t, "feature", mr.GetSourceBranch())
	assert.Equal(t, "main", mr.GetTargetBranch())
	assert.Equal(t, "Test MR", mr.GetTitle())

	author := mr.GetAuthor()
	assert.Equal(t, "testuser", author.GetUsername())
}

func TestGitlabMRAuthor(t *testing.T) {
	author := &GitlabMRAuthor{
		BasicUser: &gogitlab.BasicUser{
			Username: "testuser",
		},
	}

	assert.Equal(t, "testuser", author.GetUsername())
}

func TestGitlabMRApproval(t *testing.T) {
	approval := &GitlabMRApproval{
		MergeRequestApprovals: &gogitlab.MergeRequestApprovals{
			Approved: true,
		},
	}

	assert.True(t, approval.IsApproved())
}

func TestGitlabPipeline(t *testing.T) {
	pipeline := &GitlabPipeline{
		PipelineInfo: &gogitlab.PipelineInfo{
			ID:     789,
			Source: "merge_request_event",
		},
	}

	assert.Equal(t, 789, pipeline.GetID())
	assert.Equal(t, "merge_request_event", pipeline.GetSource())
}

func TestPtr(t *testing.T) {
	str := "test"
	num := 42

	strPtr := ptr(str)
	numPtr := ptr(num)

	assert.Equal(t, "test", *strPtr)
	assert.Equal(t, 42, *numPtr)
}

func TestDescriptionForState(t *testing.T) {
	tests := []struct {
		state    gogitlab.BuildStateValue
		expected string
	}{
		{gogitlab.Pending, "pending..."},
		{gogitlab.Running, "in progress..."},
		{gogitlab.Failed, "failed."},
		{gogitlab.Success, "succeeded."},
		{gogitlab.BuildStateValue("unknown"), "unknown"},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			result := descriptionForState(tt.state)
			assert.Equal(t, tt.expected, *result)
		})
	}
}

func TestStatusName(t *testing.T) {
	result := statusName("workspace1", "plan")
	assert.Equal(t, "TFC/plan/workspace1", *result)
}

func TestConfigureBackOff(t *testing.T) {
	backoff := configureBackOff()
	assert.NotNil(t, backoff)
}

func TestCreatePermanentError(t *testing.T) {
	result := utils.CreatePermanentError(nil)
	assert.Nil(t, result)

	normalErr := assert.AnError
	result = utils.CreatePermanentError(normalErr)
	assert.NotNil(t, result)
	assert.ErrorIs(t, result, utils.ErrPermanent)
}
