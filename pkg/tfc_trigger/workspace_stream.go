package tfc_trigger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
	"github.com/sl1pm4t/gongs"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// WorkspaceTriggerStreamName is the JetStream that carries one message per
// workspace that needs a TFC run dispatched. Each MR/PR event fans out into
// these messages so the upstream hook subscriber can ACK quickly and avoid
// JetStream redelivering when the batch has many workspaces.
const (
	WorkspaceTriggerStreamName = "TFBUDDY_WORKSPACE_TRIGGERS"
	workspaceTriggerSubject    = WorkspaceTriggerStreamName + ".dispatch"
	workspaceTriggerQueueName  = "tfbuddy_workspace_trigger_worker"

	// Sized to match HOOKS/RUN_EVENTS so a noisy day cannot push older
	// streams off the broker. At ~1KB per msg, 10240 msgs is ~10MB peak.
	workspaceStreamMaxMsgs = 10240

	// A workspace trigger that hasn't been picked up within an hour is stale
	// (the user has likely re-triggered or the MR is gone). Dropping it is
	// safer than dispatching a run against an old commit.
	workspaceStreamMaxAge = time.Hour
)

// WorkspaceTriggerMsg is the unit of work consumed by WorkspaceTriggerWorker.
// It contains everything required to drive a single workspace's TFC run from
// scratch, with no shared state across workspaces.
type WorkspaceTriggerMsg struct {
	Opts      TFCTriggerOptions      `json:"opts"`
	Workspace TFCWorkspace           `json:"workspace"`
	Carrier   propagation.MapCarrier `json:"Carrier"`

	ctx context.Context
}

// GetId returns the JetStream message ID, used as a dedup key inside
// JetStream's deduplication window. A duplicate webhook (or redelivery of
// the parent MR event) cannot enqueue the same workspace+commit+action twice.
//
// The ID is a hex SHA-256 of the canonical fields rather than a delimited
// string so it can't be accidentally collided by exotic project or workspace
// names that contain the delimiter.
func (m *WorkspaceTriggerMsg) GetId(ctx context.Context) string {
	h := sha256.New()
	enc := json.NewEncoder(h)
	_ = enc.Encode([]any{
		m.Opts.ProjectNameWithNamespace,
		m.Opts.MergeRequestIID,
		m.Opts.CommitSHA,
		m.Opts.Action.String(),
		m.Opts.VcsProvider,
		m.Workspace.Name,
		m.Workspace.Organization,
	})
	return hex.EncodeToString(h.Sum(nil))
}

func (m *WorkspaceTriggerMsg) DecodeEventData(b []byte) error {
	if err := json.Unmarshal(b, m); err != nil {
		return err
	}
	m.ctx = otel.GetTextMapPropagator().Extract(context.Background(), m.Carrier)
	return nil
}

func (m *WorkspaceTriggerMsg) EncodeEventData(ctx context.Context) []byte {
	m.Carrier = make(map[string]string)
	otel.GetTextMapPropagator().Inject(ctx, m.Carrier)
	b, err := json.Marshal(m)
	if err != nil {
		log.Error().Err(err).Msg("could not encode WorkspaceTriggerMsg")
	}
	return b
}

func (m *WorkspaceTriggerMsg) Context() context.Context {
	if m.ctx != nil {
		return m.ctx
	}
	return context.Background()
}

// WorkspaceStream wraps a gongs.GenericStream so callers don't need to import
// gongs directly to publish or subscribe.
type WorkspaceStream struct {
	stream *gongs.GenericStream[WorkspaceTriggerMsg, *WorkspaceTriggerMsg]
}

// NewWorkspaceStream provisions the JetStream that backs workspace fan-out
// and returns a publisher/subscriber wrapper.
func NewWorkspaceStream(js nats.JetStreamContext) (*WorkspaceStream, error) {
	cfg := &nats.StreamConfig{
		Name:        WorkspaceTriggerStreamName,
		Description: "Fan-out queue: one message per workspace dispatched by an MR/PR trigger",
		Subjects:    []string{fmt.Sprintf("%s.>", WorkspaceTriggerStreamName)},
		Retention:   nats.WorkQueuePolicy,
		MaxMsgs:     workspaceStreamMaxMsgs,
		MaxAge:      workspaceStreamMaxAge,
		Replicas:    1,
	}

	info, err := js.StreamInfo(cfg.Name)
	switch {
	case errors.Is(err, nats.ErrStreamNotFound):
		if _, err := js.AddStream(cfg); err != nil {
			return nil, fmt.Errorf("could not create %s stream: %w", cfg.Name, err)
		}
	case err != nil:
		return nil, fmt.Errorf("could not read %s stream info: %w", cfg.Name, err)
	case info != nil:
		if _, err := js.UpdateStream(cfg); err != nil {
			return nil, fmt.Errorf("could not update %s stream: %w", cfg.Name, err)
		}
	}

	return &WorkspaceStream{
		stream: gongs.NewGenericStream[WorkspaceTriggerMsg](js, workspaceTriggerSubject, WorkspaceTriggerStreamName),
	}, nil
}

// Publish fans out a workspace trigger.
func (s *WorkspaceStream) Publish(ctx context.Context, msg *WorkspaceTriggerMsg) error {
	if s == nil || s.stream == nil {
		return errors.New("workspace stream not configured")
	}
	_, err := s.stream.Publish(ctx, msg)
	return err
}

// QueueSubscribe registers a worker on the shared queue. All replicas of the
// hooks server share the same queue group, so each message is delivered to
// exactly one worker.
func (s *WorkspaceStream) QueueSubscribe(handler func(*WorkspaceTriggerMsg) error) (*nats.Subscription, error) {
	return s.stream.QueueSubscribe(workspaceTriggerQueueName, handler)
}
