package hooks

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/go-github/v49/github"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/hooks_stream"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
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
	Payload *github.PullRequestEvent `json:"payload"`
	Carrier propagation.MapCarrier   `json:"Carrier"`
	Context context.Context
}

func (e *PullRequestEventMsg) GetId(ctx context.Context) string {
	return *e.Payload.PullRequest.URL
}

func (e *PullRequestEventMsg) DecodeEventData(b []byte) error {
	log.Debug().RawJSON("event_data", b).Msg("worker got PR event")
	err := json.Unmarshal(b, e)
	if err != nil {
		return err
	}
	e.Context = otel.GetTextMapPropagator().Extract(context.Background(), e.Carrier)
	return nil
}

func (e *PullRequestEventMsg) EncodeEventData(ctx context.Context) []byte {
	ctx, span := otel.Tracer("hooks").Start(ctx, "encode_event_data",
		trace.WithAttributes(
			attribute.String("event_type", "PullRequestEvent"),
			attribute.String("vcs", "github"),
		))
	defer span.End()
	e.Carrier = make(map[string]string)
	otel.GetTextMapPropagator().Inject(ctx, e.Carrier)
	b, _ := json.Marshal(e)
	return b
}

// ----------------------------------------------------------------------------
const IssueCommentEvent = "IssueCommentEvent"

type GithubIssueCommentEventMsg struct {
	Payload *github.IssueCommentEvent `json:"payload"`
	Carrier propagation.MapCarrier    `json:"Carrier"`
	Context context.Context
}

func (e *GithubIssueCommentEventMsg) GetId(ctx context.Context) string {
	return fmt.Sprintf("%d", *e.Payload.Comment.ID)
}

func (e *GithubIssueCommentEventMsg) DecodeEventData(b []byte) error {
	log.Trace().RawJSON("event_data", b).Msg("decoding issue_comment event")
	err := json.Unmarshal(b, e)
	if err != nil {
		log.Error().Err(err).Msg("could not decode Github IssueCommentEvent")
		return err
	}

	e.Context = otel.GetTextMapPropagator().Extract(context.Background(), e.Carrier)
	return nil
}

func (e *GithubIssueCommentEventMsg) EncodeEventData(ctx context.Context) []byte {
	ctx, span := otel.Tracer("hooks").Start(ctx, "encode_event_data",
		trace.WithAttributes(
			attribute.String("event_type", "issue_comment_event"),
			attribute.String("vcs", "github"),
		))
	defer span.End()
	e.Carrier = make(map[string]string)
	otel.GetTextMapPropagator().Inject(ctx, e.Carrier)
	b, err := json.Marshal(e)
	if err != nil {
		log.Error().Err(err).Msg("could not encode github IssueCommentEvent")
	}
	return b
}

// ----------------------------------------------------------------------------
