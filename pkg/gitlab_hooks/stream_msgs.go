package gitlab_hooks

import (
	"context"
	"encoding/json"
	"fmt"

	gogitlab "github.com/xanzy/go-gitlab"
	"github.com/zapier/tfbuddy/pkg/hooks_stream"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"github.com/zapier/tfbuddy/pkg/vcs/gitlab"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const GitlabHooksSubject = "gitlab"
const MergeRequestEventsSubject = "mrevents"
const NoteEventsSubject = "noteevents"

func noteEventsStreamSubject() string {
	return fmt.Sprintf("%s.%s.%s", hooks_stream.HooksStreamName, GitlabHooksSubject, NoteEventsSubject)
}

// GitlabHookEvent represents all types of Gitlab Hooks events to be processed.
type GitlabHookEvent struct {
}

func (e *GitlabHookEvent) GetPlatform() string {
	return "gitlab"
}

// ----------------------------------------------

type NoteEventMsg struct {
	GitlabHookEvent

	Payload *gitlab.GitlabMergeCommentEvent `json:"payload"`
	Carrier propagation.MapCarrier          `json:"Carrier"`
	Context context.Context
}

func (e *NoteEventMsg) GetId(ctx context.Context) string {
	return e.Payload.GetDiscussionID()
}

func (e *NoteEventMsg) DecodeEventData(b []byte) error {
	err := json.Unmarshal(b, e)
	if err != nil {
		return err
	}
	e.Context = otel.GetTextMapPropagator().Extract(context.Background(), e.Carrier)
	return nil
}

func (e *NoteEventMsg) EncodeEventData(ctx context.Context) []byte {
	ctx, span := otel.Tracer("hooks").Start(ctx, "encode_event_data",
		trace.WithAttributes(
			attribute.String("event_type", "NoteEvent"),
			attribute.String("vcs", "gitlab"),
		))
	defer span.End()
	e.Carrier = make(map[string]string)
	otel.GetTextMapPropagator().Inject(ctx, e.Carrier)
	b, _ := json.Marshal(e)
	return b
}

func (e *NoteEventMsg) GetProject() vcs.Project {
	return e.Payload.GetProject()
}

func (e *NoteEventMsg) GetMR() vcs.MR {
	return e.Payload
}

func (e *NoteEventMsg) GetAttributes() vcs.MRAttributes {
	return e.Payload.GetAttributes()
}

func (e *NoteEventMsg) GetLastCommit() vcs.Commit {
	return e.Payload.GetLastCommit()
}

// ----------------------------------------------

func mrEventsStreamSubject() string {
	return fmt.Sprintf("%s.%s.%s", hooks_stream.HooksStreamName, GitlabHooksSubject, MergeRequestEventsSubject)
}

type MergeRequestEventMsg struct {
	GitlabHookEvent

	Payload *gogitlab.MergeEvent   `json:"payload"`
	Carrier propagation.MapCarrier `json:"Carrier"`
	Context context.Context
}

func (e *MergeRequestEventMsg) GetId(ctx context.Context) string {
	return fmt.Sprintf("%d-%s", e.Payload.ObjectAttributes.ID, e.Payload.ObjectAttributes.Action)
}

func (e *MergeRequestEventMsg) DecodeEventData(b []byte) error {
	err := json.Unmarshal(b, e)
	if err != nil {
		return err
	}
	e.Context = otel.GetTextMapPropagator().Extract(context.Background(), e.Carrier)
	return nil
}

func (e *MergeRequestEventMsg) EncodeEventData(ctx context.Context) []byte {
	ctx, span := otel.Tracer("hooks").Start(ctx, "encode_event_data",
		trace.WithAttributes(
			attribute.String("event_type", "MergeRequestEventMsg"),
			attribute.String("vcs", "gitlab"),
		))
	defer span.End()
	e.Carrier = make(map[string]string)
	otel.GetTextMapPropagator().Inject(ctx, e.Carrier)
	b, _ := json.Marshal(e)
	return b
}

func (e *MergeRequestEventMsg) GetType(ctx context.Context) string {
	return "MergeRequestEventMsg"
}

func (e *MergeRequestEventMsg) GetPayload() interface{} {
	return *e.Payload
}
