package tfc_trigger

import (
	"context"
	"fmt"
	"os"

	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/vcs"
)

// WorkspaceTriggerWorker drains the WorkspaceTriggerStream. Each delivery gets
// its own AckWait window, which is the durability boundary the fan-out provides.
// The stream is shared across VCS providers, so the worker routes each message
// to the matching client by Opts.VcsProvider.
type WorkspaceTriggerWorker struct {
	appCfg    config.Config
	clients   map[string]vcs.GitClient
	tfc       tfc_api.ApiClient
	runstream runstream.StreamClient
}

func NewWorkspaceTriggerWorker(stream *WorkspaceStream, appCfg config.Config, clients map[string]vcs.GitClient, tfc tfc_api.ApiClient, rs runstream.StreamClient) (*WorkspaceTriggerWorker, error) {
	w := NewWorkspaceTriggerWorkerWithoutSubscription(appCfg, clients, tfc, rs)
	if _, err := stream.QueueSubscribe(w.HandleMsg); err != nil {
		return nil, fmt.Errorf("could not subscribe to workspace trigger stream: %w", err)
	}
	return w, nil
}

// NewWorkspaceTriggerWorkerWithoutSubscription constructs a worker without
// subscribing. Used by tests that drive HandleMsg directly.
func NewWorkspaceTriggerWorkerWithoutSubscription(appCfg config.Config, clients map[string]vcs.GitClient, tfc tfc_api.ApiClient, rs runstream.StreamClient) *WorkspaceTriggerWorker {
	cp := make(map[string]vcs.GitClient, len(clients))
	for k, v := range clients {
		if v != nil {
			cp[k] = v
		}
	}
	return &WorkspaceTriggerWorker{appCfg: appCfg, clients: cp, tfc: tfc, runstream: rs}
}

func (w *WorkspaceTriggerWorker) HandleMsg(msg *WorkspaceTriggerMsg) (rerr error) {
	ctx, span := otel.Tracer("TFC").Start(msg.Context(), "WorkspaceTriggerWorker.handle",
		trace.WithAttributes(
			attribute.String("workspace", msg.Workspace.Name),
			attribute.String("project", msg.Opts.ProjectNameWithNamespace),
			attribute.Int("mr", msg.Opts.MergeRequestIID),
			attribute.String("action", msg.Opts.Action.String()),
			attribute.String("vcs_provider", msg.Opts.VcsProvider),
		))
	defer span.End()

	gl, ok := w.clients[msg.Opts.VcsProvider]
	if !ok {
		// ACK and rely on logs/tracing — a redelivery would still match no provider.
		err := fmt.Errorf("no VCS client configured for provider %q", msg.Opts.VcsProvider)
		log.Error().Err(err).
			Str("workspace", msg.Workspace.Name).
			Str("project", msg.Opts.ProjectNameWithNamespace).
			Int("mr", msg.Opts.MergeRequestIID).
			Msg("dropping workspace trigger with unknown VCS provider")
		span.RecordError(err)
		return nil
	}

	defer func() {
		// Never propagate panics into JetStream — that tears down the subscription.
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).
				Str("workspace", msg.Workspace.Name).
				Msg("recovered from panic in workspace worker")
			span.RecordError(fmt.Errorf("panic recovered: %v", r))
			userErr := fmt.Errorf("internal error")
			if traceID := span.SpanContext().TraceID(); traceID.IsValid() {
				userErr = fmt.Errorf("internal error (trace id: %s)", traceID.String())
			}
			w.postWorkspaceError(ctx, gl, &msg.Opts, msg.Workspace.Name, userErr)
			rerr = nil
		}
	}()

	if err := w.runWorkspace(ctx, gl, msg); err != nil {
		log.Error().Err(err).
			Str("workspace", msg.Workspace.Name).
			Str("project", msg.Opts.ProjectNameWithNamespace).
			Int("mr", msg.Opts.MergeRequestIID).
			Msg("workspace trigger failed")
		w.postWorkspaceError(ctx, gl, &msg.Opts, msg.Workspace.Name, err)
		// ACK after notifying the user. Retrying would create duplicate
		// discussions/runs — the exact bug this fan-out is meant to prevent.
		return nil
	}
	return nil
}

func (w *WorkspaceTriggerWorker) runWorkspace(ctx context.Context, gl vcs.GitClient, msg *WorkspaceTriggerMsg) error {
	ctx, span := otel.Tracer("TFC").Start(ctx, "WorkspaceTriggerWorker.run")
	defer span.End()

	cfg, err := NewTFCTriggerConfig(&msg.Opts)
	if err != nil {
		return fmt.Errorf("invalid trigger config: %w", err)
	}
	trigger := &TFCTrigger{
		appCfg:    w.appCfg,
		gl:        gl,
		tfc:       w.tfc,
		runstream: w.runstream,
		cfg:       cfg,
	}

	mr, err := gl.GetMergeRequest(ctx, trigger.GetMergeRequestIID(), trigger.GetProjectNameWithNamespace())
	if err != nil {
		return fmt.Errorf("could not read MergeRequest data from VCS API: %w", err)
	}

	repo, err := trigger.cloneGitRepo(ctx, mr)
	if err != nil {
		return fmt.Errorf("could not clone repo: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(repo.GetLocalDirectory()); err != nil {
			log.Error().Err(err).Str("path", repo.GetLocalDirectory()).Msg("could not remove cloned repository directory")
		}
	}()

	return trigger.triggerRunForWorkspace(ctx, &msg.Workspace, mr, repo.GetLocalDirectory())
}

// postWorkspaceError surfaces a workspace failure to the MR. The per-workspace
// discussion only exists inside triggerRunForWorkspace, so failures before that
// land as a top-level comment.
func (w *WorkspaceTriggerWorker) postWorkspaceError(ctx context.Context, gl vcs.GitClient, opts *TFCTriggerOptions, ws string, err error) {
	if gl == nil {
		return
	}
	body := fmt.Sprintf(":no_entry: %s could not be run because: %s", ws, err.Error())
	if cerr := gl.CreateMergeRequestComment(ctx, opts.MergeRequestIID, opts.ProjectNameWithNamespace, body); cerr != nil {
		log.Error().Err(cerr).Str("workspace", ws).Msg("could not post workspace error to MR")
	}
}
