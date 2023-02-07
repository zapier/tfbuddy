package hooks

import (
	"encoding/json"
	"fmt"

	"github.com/google/go-github/v49/github"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/hooks_stream"
)

// ----------------------------------------------------------------------------
const GithubJetstreamTopic = "github"

func getGithubJetstreamName() string {
	return fmt.Sprintf("%s.%s", hooks_stream.HooksStreamName, GithubJetstreamTopic)
}

func getGithubJetstreamSubject(evtType string) string {
	return fmt.Sprintf("%s.%s.%s", hooks_stream.HooksStreamName, GithubJetstreamTopic, evtType)
}

// ----------------------------------------------------------------------------
const PullRequestEventType = "PullRequestEvent"

type PullRequestEventMsg struct {
	payload *github.PullRequestEvent
}

func (e *PullRequestEventMsg) GetId() string {
	return *e.payload.PullRequest.URL
}

func (e *PullRequestEventMsg) DecodeEventData(b []byte) error {
	log.Debug().RawJSON("event_data", b).Msg("worker got PR event")
	d := &github.PullRequestEvent{}
	err := json.Unmarshal(b, d)
	if err != nil {
		return err
	}
	e.payload = d
	return nil
}

func (e *PullRequestEventMsg) EncodeEventData() []byte {
	b, _ := json.Marshal(e.payload)
	return b
}

// ----------------------------------------------------------------------------
const IssueCommentEvent = "IssueCommentEvent"

type GithubIssueCommentEventMsg struct {
	payload *github.IssueCommentEvent
}

func (e *GithubIssueCommentEventMsg) GetId() string {
	return fmt.Sprintf("%d", *e.payload.Comment.ID)
}

func (e *GithubIssueCommentEventMsg) DecodeEventData(b []byte) error {
	log.Trace().RawJSON("event_data", b).Msg("decoding issue_comment event")

	p := &github.IssueCommentEvent{}
	err := json.Unmarshal(b, p)
	if err != nil {
		log.Error().Err(err).Msg("could not decode Github IssueCommentEvent")
		return err
	}
	e.payload = p
	return nil
}

func (e *GithubIssueCommentEventMsg) EncodeEventData() []byte {
	b, err := json.Marshal(e.payload)
	if err != nil {
		log.Error().Err(err).Msg("could not encode github IssueCommentEvent")
	}
	return b
}

// ----------------------------------------------------------------------------
