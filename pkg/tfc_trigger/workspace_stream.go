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

const (
	WorkspaceTriggerStreamName = "TFBUDDY_WORKSPACE_TRIGGERS"
	workspaceTriggerSubject    = WorkspaceTriggerStreamName + ".dispatch"
	workspaceTriggerQueueName  = "tfbuddy_workspace_trigger_worker"

	workspaceStreamMaxMsgs = 10240
	workspaceStreamMaxAge  = time.Hour
)

type WorkspaceTriggerMsg struct {
	Opts      TFCTriggerOptions      `json:"opts"`
	Workspace TFCWorkspace           `json:"workspace"`
	Carrier   propagation.MapCarrier `json:"Carrier"`

	ctx context.Context
}

// GetId is the JetStream dedup key. When DeliveryID is present (the normal
// path for GitHub X-GitHub-Delivery / GitLab Idempotency-Key), we compose the
// key directly from the opaque per-delivery identifier plus workspace identity.
// The sha256 fallback handles only legacy messages missing the delivery header.
func (m *WorkspaceTriggerMsg) GetId(ctx context.Context) string {
	if m.Opts.DeliveryID != "" {
		return m.Opts.DeliveryID + "/" + m.Workspace.Name + "/" + m.Workspace.Organization
	}

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

type WorkspaceStream struct {
	js     nats.JetStreamContext
	stream *gongs.GenericStream[WorkspaceTriggerMsg, *WorkspaceTriggerMsg]
}

func NewWorkspaceStream(js nats.JetStreamContext, workspaceStreamReplicas int) (*WorkspaceStream, error) {
	cfg := &nats.StreamConfig{
		Name:        WorkspaceTriggerStreamName,
		Description: "Fan-out queue: one message per workspace dispatched by an MR/PR trigger",
		Subjects:    []string{fmt.Sprintf("%s.>", WorkspaceTriggerStreamName)},
		Retention:   nats.WorkQueuePolicy,
		MaxMsgs:     workspaceStreamMaxMsgs,
		MaxAge:      workspaceStreamMaxAge,
		Replicas:    workspaceStreamReplicas,
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
		js:     js,
		stream: gongs.NewGenericStream[WorkspaceTriggerMsg](js, workspaceTriggerSubject, WorkspaceTriggerStreamName),
	}, nil
}

func (s *WorkspaceStream) Publish(ctx context.Context, msg *WorkspaceTriggerMsg) error {
	if s == nil || s.stream == nil {
		return errors.New("workspace stream not configured")
	}
	_, err := s.stream.Publish(ctx, msg)
	return err
}

// QueueSubscribe binds workers to a shared queue so each message is delivered
// to exactly one replica.
func (s *WorkspaceStream) QueueSubscribe(handler func(*WorkspaceTriggerMsg) error) (*nats.Subscription, error) {
	return s.stream.QueueSubscribe(workspaceTriggerQueueName, handler)
}

// HealthCheck reports unhealthy when the stream is missing from JetStream,
// or when it has no consumers (published messages would otherwise pile up
// undelivered).
func (s *WorkspaceStream) HealthCheck() error {
	if s == nil || s.js == nil {
		return errors.New("workspace stream not configured")
	}
	info, err := s.js.StreamInfo(WorkspaceTriggerStreamName)
	if err != nil {
		if errors.Is(err, nats.ErrStreamNotFound) {
			log.Warn().Str("stream", WorkspaceTriggerStreamName).
				Msg("Healthcheck status: workspace trigger stream not found in JetStream")
			return fmt.Errorf("%s stream not found in JetStream", WorkspaceTriggerStreamName)
		}
		return fmt.Errorf("could not read %s stream info: %w", WorkspaceTriggerStreamName, err)
	}
	if info.State.Consumers < 1 {
		ev := log.Warn().Str("stream", info.Config.Name).
			Int("consumers", info.State.Consumers).
			Uint64("msgs", info.State.Msgs)
		if info.Cluster != nil {
			ev = ev.Str("cluster_leader", info.Cluster.Leader)
		}
		ev.Msg("Healthcheck status.")
		return fmt.Errorf("%s stream has no consumers", info.Config.Name)
	}
	return nil
}
