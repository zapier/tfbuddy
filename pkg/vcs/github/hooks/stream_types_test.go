package hooks

import (
	"context"
	"testing"

	"github.com/google/go-github/v69/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
)

func TestGetGithubJetstreamName(t *testing.T) {
	result := getGithubJetstreamName()
	assert.Contains(t, result, "github")
}

func TestGetGithubJetstreamSubject(t *testing.T) {
	evtType := "test-event"
	result := getGithubJetstreamSubject(evtType)
	assert.Contains(t, result, "github")
	assert.Contains(t, result, evtType)
}

func TestPullRequestEventMsg_GetId(t *testing.T) {
	url := "https://api.github.com/repos/owner/repo/pulls/1"
	msg := &PullRequestEventMsg{
		Payload: &github.PullRequestEvent{
			PullRequest: &github.PullRequest{
				URL: &url,
			},
		},
	}

	result := msg.GetId(context.Background())
	assert.Equal(t, url, result)
}

func TestPullRequestEventMsg_EncodeDecodeEventData(t *testing.T) {
	ctx := context.Background()
	url := "https://api.github.com/repos/owner/repo/pulls/1"
	number := 1

	original := &PullRequestEventMsg{
		Payload: &github.PullRequestEvent{
			PullRequest: &github.PullRequest{
				URL:    &url,
				Number: &number,
			},
		},
		Carrier: make(propagation.MapCarrier),
	}

	// Encode
	encoded := original.EncodeEventData(ctx)
	assert.NotEmpty(t, encoded)

	// Decode
	decoded := &PullRequestEventMsg{}
	err := decoded.DecodeEventData(encoded)
	require.NoError(t, err)

	assert.Equal(t, *original.Payload.PullRequest.URL, *decoded.Payload.PullRequest.URL)
	assert.Equal(t, *original.Payload.PullRequest.Number, *decoded.Payload.PullRequest.Number)
}

func TestGithubIssueCommentEventMsg_GetId(t *testing.T) {
	id := int64(123)
	msg := &GithubIssueCommentEventMsg{
		Payload: &github.IssueCommentEvent{
			Comment: &github.IssueComment{
				ID: &id,
			},
		},
	}

	result := msg.GetId(context.Background())
	assert.Equal(t, "123", result)
}

func TestGithubIssueCommentEventMsg_EncodeDecodeEventData(t *testing.T) {
	ctx := context.Background()
	id := int64(456)
	body := "Test comment"

	original := &GithubIssueCommentEventMsg{
		Payload: &github.IssueCommentEvent{
			Comment: &github.IssueComment{
				ID:   &id,
				Body: &body,
			},
		},
		Carrier: make(propagation.MapCarrier),
	}

	// Encode
	encoded := original.EncodeEventData(ctx)
	assert.NotEmpty(t, encoded)

	// Decode
	decoded := &GithubIssueCommentEventMsg{}
	err := decoded.DecodeEventData(encoded)
	require.NoError(t, err)

	assert.Equal(t, *original.Payload.Comment.ID, *decoded.Payload.Comment.ID)
	assert.Equal(t, *original.Payload.Comment.Body, *decoded.Payload.Comment.Body)
}

func TestGithubIssueCommentEventMsg_DecodeEventData_InvalidJSON(t *testing.T) {
	msg := &GithubIssueCommentEventMsg{}
	err := msg.DecodeEventData([]byte("invalid json"))
	assert.Error(t, err)
}

func TestPullRequestEventMsg_DecodeEventData_InvalidJSON(t *testing.T) {
	msg := &PullRequestEventMsg{}
	err := msg.DecodeEventData([]byte("invalid json"))
	assert.Error(t, err)
}

func TestConstants(t *testing.T) {
	assert.Equal(t, "PullRequestEvent", PullRequestEventType)
	assert.Equal(t, "IssueCommentEvent", IssueCommentEvent)
	assert.Equal(t, "github", GithubJetstreamTopic)
}
