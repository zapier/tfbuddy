package tfc_trigger

import (
	"context"
	"fmt"
	"os"

	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/vcs"
)

// WorkspaceTriggerWorker drains the WorkspaceTriggerStream and dispatches one
// TFC run per message. Each delivery is processed independently, so the
// JetStream AckWait window applies per workspace rather than per MR/PR — this
// is the durability boundary the fan-out provides.
type WorkspaceTriggerWorker struct {
	gl        vcs.GitClient
	tfc       tfc_api.ApiClient
	runstream runstream.StreamClient
}

// NewWorkspaceTriggerWorker subscribes to the workspace stream and starts
// processing messages. The subscription is queue-bound so multiple replicas
// share the load.
func NewWorkspaceTriggerWorker(stream *WorkspaceStream, gl vcs.GitClient, tfc tfc_api.ApiClient, rs runstream.StreamClient) (*WorkspaceTriggerWorker, error) {
	w := NewWorkspaceTriggerWorkerWithoutSubscription(gl, tfc, rs)
	if _, err := stream.QueueSubscribe(w.HandleMsg); err != nil {
		return nil, fmt.Errorf("could not subscribe to workspace trigger stream: %w", err)
	}
	return w, nil
}

// NewWorkspaceTriggerWorkerWithoutSubscription constructs a worker without
// subscribing to a stream. Used by tests that drive HandleMsg directly.
func NewWorkspaceTriggerWorkerWithoutSubscription(gl vcs.GitClient, tfc tfc_api.ApiClient, rs runstream.StreamClient) *WorkspaceTriggerWorker {
	return &WorkspaceTriggerWorker{gl: gl, tfc: tfc, runstream: rs}
}

// HandleMsg processes a single workspace trigger delivery. JetStream calls
// this via the queue subscription; tests call it directly.
func (w *WorkspaceTriggerWorker) HandleMsg(msg *WorkspaceTriggerMsg) (rerr error) {
	ctx, span := otel.Tracer("TFC").Start(msg.Context(), "WorkspaceTriggerWorker.handle",
		trace.WithAttributes(
			attribute.String("workspace", msg.Workspace.Name),
			attribute.String("project", msg.Opts.ProjectNameWithNamespace),
			attribute.Int("mr", msg.Opts.MergeRequestIID),
			attribute.String("action", msg.Opts.Action.String()),
		))
	defer span.End()

	defer func() {
		// Never let a panic propagate into JetStream — that would tear down
		// the subscription. Treat it like any other failure and surface it on
		// the MR so the user knows their request didn't go through.
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).
				Str("workspace", msg.Workspace.Name).
				Msg("recovered from panic in workspace worker")
			w.postWorkspaceError(ctx, &msg.Opts, msg.Workspace.Name, fmt.Errorf("internal error: %v", r))
			rerr = nil
		}
	}()

	if err := w.runWorkspace(ctx, msg); err != nil {
		log.Error().Err(err).
			Str("workspace", msg.Workspace.Name).
			Str("project", msg.Opts.ProjectNameWithNamespace).
			Int("mr", msg.Opts.MergeRequestIID).
			Msg("workspace trigger failed")
		w.postWorkspaceError(ctx, &msg.Opts, msg.Workspace.Name, err)
		// Swallow the error and ACK: the user has been notified on the MR.
		// Retrying would create duplicate discussions/runs, which is exactly
		// the duplicate-state class of bug this fan-out is meant to prevent.
		return nil
	}
	return nil
}

func (w *WorkspaceTriggerWorker) runWorkspace(ctx context.Context, msg *WorkspaceTriggerMsg) error {
	ctx, span := otel.Tracer("TFC").Start(ctx, "WorkspaceTriggerWorker.run")
	defer span.End()

	cfg, err := NewTFCTriggerConfig(&msg.Opts)
	if err != nil {
		return fmt.Errorf("invalid trigger config: %w", err)
	}
	trigger := &TFCTrigger{
		gl:        w.gl,
		tfc:       w.tfc,
		runstream: w.runstream,
		cfg:       cfg,
	}

	mr, err := trigger.gl.GetMergeRequest(ctx, trigger.GetMergeRequestIID(), trigger.GetProjectNameWithNamespace())
	if err != nil {
		return fmt.Errorf("could not read MergeRequest data from VCS API: %w", err)
	}

	repo, err := trigger.cloneGitRepo(ctx, mr)
	if err != nil {
		return fmt.Errorf("could not clone repo: %w", err)
	}
	defer os.RemoveAll(repo.GetLocalDirectory())

	// Re-check that this workspace's paths haven't moved on the target branch
	// since the MR diverged. The MR-event worker can't do this cheaply (no
	// clone), so we do it here per workspace.
	if blocked, reason, err := trigger.workspaceBlockedByTargetBranch(ctx, mr, repo, &msg.Workspace); err != nil {
		log.Warn().Err(err).Str("ws", msg.Workspace.Name).Msg("could not evaluate target-branch modifications")
	} else if blocked {
		return fmt.Errorf("%s", reason)
	}

	return trigger.triggerRunForWorkspace(ctx, &msg.Workspace, mr, repo.GetLocalDirectory())
}

// postWorkspaceError surfaces a workspace failure back to the MR/PR. The
// per-workspace discussion is only created inside triggerRunForWorkspace, so
// most failures happen before that and need a top-level comment instead.
func (w *WorkspaceTriggerWorker) postWorkspaceError(ctx context.Context, opts *TFCTriggerOptions, ws string, err error) {
	if w.gl == nil {
		return
	}
	body := fmt.Sprintf(":no_entry: %s could not be run because: %s", ws, err.Error())
	if cerr := w.gl.CreateMergeRequestComment(ctx, opts.MergeRequestIID, opts.ProjectNameWithNamespace, body); cerr != nil {
		log.Error().Err(cerr).Str("workspace", ws).Msg("could not post workspace error to MR")
	}
}

// workspaceBlockedByTargetBranch is a workspace-scoped equivalent of
// getModifiedWorkspacesOnTargetBranch, kept on TFCTrigger so the per-workspace
// worker can use it without iterating the whole project config.
func (t *TFCTrigger) workspaceBlockedByTargetBranch(ctx context.Context, mr vcs.MR, repo vcs.GitRepo, ws *TFCWorkspace) (bool, string, error) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "workspaceBlockedByTargetBranch")
	defer span.End()

	if err := repo.FetchUpstreamBranch(mr.GetTargetBranch()); err != nil {
		return false, "", fmt.Errorf("could not fetch target branch %s: %w", mr.GetTargetBranch(), err)
	}
	commonSHA, err := repo.GetMergeBase(mr.GetSourceBranch(), mr.GetTargetBranch())
	if err != nil {
		return false, "", fmt.Errorf("could not find merge base: %w", err)
	}
	targetModifiedFiles, err := repo.GetModifiedFileNamesBetweenCommits(commonSHA, mr.GetTargetBranch())
	if err != nil {
		return false, "", fmt.Errorf("could not list modified files between %s..%s: %w", commonSHA, mr.GetTargetBranch(), err)
	}
	if len(targetModifiedFiles) == 0 || !hasChangesForWorkspace(ws, targetModifiedFiles) {
		return false, "", nil
	}
	return true, fmt.Sprintf("Blocked: workspace-relevant paths (dir: '%s', triggerDirs: %v) have been modified on the target branch since this branch diverged. Please rebase/merge the target branch to resolve this.", ws.Dir, ws.TriggerDirs), nil
}
