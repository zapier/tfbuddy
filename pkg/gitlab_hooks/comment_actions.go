package gitlab_hooks

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/allow_list"
	"github.com/zapier/tfbuddy/pkg/comment_actions"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/vcs"
	gitlab "gitlab.com/gitlab-org/api/client-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

// deliveryIDProvider is the optional accessor production envelopes implement
// to thread the webhook delivery ID without widening vcs.MRCommentEvent.
type deliveryIDProvider interface {
	GetDeliveryID() string
}

type eventSequenceProvider interface {
	GetEventSequence() int64
}

// processNoteEvent processes GitLab Webhooks for Note events
// In the Gitlab API, MR comments are called Notes
func (w *GitlabEventWorker) processNoteEvent(ctx context.Context, event vcs.MRCommentEvent) (projectName string, err error) {
	ctx, span := otel.Tracer("hooks").Start(ctx, "ProcessNoteEvent")
	defer span.End()

	proj := event.GetProject().GetPathWithNamespace()
	if !allow_list.IsGitlabProjectAllowed(w.cfg, proj) {
		return proj, nil
	}

	// cleanup comment string for processing
	opts, err := comment_actions.ParseCommentCommand(event.GetAttributes().GetNote())
	if err != nil {
		if err == comment_actions.ErrOtherTFTool {
			w.postMessageToMergeRequest(ctx, event, "Use tfc to interact with tfbuddy")
		}
		if err == comment_actions.ErrNotTFCCommand || err == comment_actions.ErrOtherTFTool {
			gitlabWebHookIgnored.WithLabelValues("comment", "not-tfc-command", proj).Inc()
			return proj, nil
		}
		return proj, err
	}

	opts.TriggerOpts.Branch = event.GetMR().GetSourceBranch()
	opts.TriggerOpts.CommitSHA = event.GetLastCommit().GetSHA()
	opts.TriggerOpts.ProjectNameWithNamespace = proj
	opts.TriggerOpts.MergeRequestIID = event.GetMR().GetInternalID()
	opts.TriggerOpts.TriggerSource = tfc_trigger.CommentTrigger
	opts.TriggerOpts.VcsProvider = "gitlab"
	if dp, ok := event.(deliveryIDProvider); ok {
		opts.TriggerOpts.DeliveryID = dp.GetDeliveryID()
	}
	if sp, ok := event.(eventSequenceProvider); ok {
		opts.TriggerOpts.AutoMergeSequence = sp.GetEventSequence()
	}

	cfg, err := tfc_trigger.NewTFCTriggerConfig(opts.TriggerOpts)
	if err != nil {
		log.Error().Err(err).Msg("could not create TFCTriggerConfig")
		return proj, err
	}

	trigger := w.triggerCreation(w.cfg, w.gl, w.tfc, w.runstream, cfg)
	if w.workspaceStream != nil {
		trigger.SetWorkspaceStream(w.workspaceStream)
	}

	if event.GetAttributes().GetType() == string(gitlab.DiscussionNote) {
		trigger.SetMergeRequestDiscussionID(event.GetAttributes().GetDiscussionID())
	}

	span.SetAttributes(
		attribute.String("agent", opts.Args.Agent),
		attribute.String("command", opts.Args.Command),
		attribute.String("branch", opts.TriggerOpts.Branch),
		attribute.String("commit_sha", opts.TriggerOpts.CommitSHA),
		attribute.String("project_name", opts.TriggerOpts.ProjectNameWithNamespace),
		attribute.Int("merge_request_iid", opts.TriggerOpts.MergeRequestIID),
		attribute.String("vcs_provider", opts.TriggerOpts.VcsProvider),
	)

	// TODO: support additional commands and arguments (e.g. destroy, refresh, lock, unlock)
	// TODO: this should be refactored and be agnostic to the VCS type
	switch opts.Args.Command {
	case "apply":
		log.Debug().Str("project", proj).Int("mergeRequestID", event.GetMR().GetInternalID()).Msg("Got TFC apply command")
		if !w.checkApproval(ctx, event) {
			w.postMessageToMergeRequest(ctx, event, ":no_entry: Apply failed. Merge Request requires approval.")
			return proj, nil
		}
		if !w.checkForMergeConflicts(ctx, event) {
			w.postMessageToMergeRequest(ctx, event, ":no_entry: Apply failed. Merge Request has conflicts that need to be resolved.")
			return proj, nil
		}
		// checkPipelineStatus posts its own reason, since it names the blocking job.
		if !w.checkPipelineStatus(ctx, event, opts.TriggerOpts.CommitSHA) {
			return proj, nil
		}
	case "lock":
		log.Debug().Str("project", proj).Int("mergeRequestID", event.GetMR().GetInternalID()).Msg("Got TFC lock command")
	case "plan":
		log.Debug().Str("project", proj).Int("mergeRequestID", event.GetMR().GetInternalID()).Msg("Got TFC plan command")
	case "unlock":
		log.Debug().Str("project", proj).Int("mergeRequestID", event.GetMR().GetInternalID()).Msg("Got TFC unlock command")
	default:
		return proj, nil
	}
	executedWorkspaces, tfError := trigger.TriggerTFCEvents(ctx)
	if tfError == nil && executedWorkspaces != nil {
		if len(executedWorkspaces.Errored) > 0 {
			for _, failedWS := range executedWorkspaces.Errored {
				w.postMessageToMergeRequest(ctx, event, fmt.Sprintf(":no_entry: %s could not be run because: %s", failedWS.Name, failedWS.Error))
			}
			return proj, nil
		}
	}
	if tfError != nil {
		w.postMessageToMergeRequest(ctx, event, fmt.Sprintf(":no_entry: could not be run because: %s", tfError.Error()))
	}
	return proj, tfError

}

func (w *GitlabEventWorker) checkApproval(ctx context.Context, event vcs.MRCommentEvent) bool {
	ctx, span := otel.Tracer("hooks").Start(ctx, "checkApproval")
	defer span.End()

	mrIID := event.GetMR().GetInternalID()
	proj := event.GetProject().GetPathWithNamespace()
	approvals, err := w.gl.GetMergeRequestApprovals(ctx, mrIID, proj)
	if err != nil {
		w.postErrorToMergeRequest(ctx, event, fmt.Errorf("could not get MergeRequest from GitlabAPI: %v", err))
		return false
	}

	return approvals.IsApproved()
}

func (w *GitlabEventWorker) checkForMergeConflicts(ctx context.Context, event vcs.MRCommentEvent) bool {
	ctx, span := otel.Tracer("hooks").Start(ctx, "CheckForMergeConflicts")
	defer span.End()

	mrIID := event.GetMR().GetInternalID()
	proj := event.GetProject().GetPathWithNamespace()
	mr, err := w.gl.GetMergeRequest(ctx, mrIID, proj)
	if err != nil {
		w.postErrorToMergeRequest(ctx, event, fmt.Errorf("could not get MergeRequest from GitlabAPI: %v", err))
		return false
	}
	// fail if the MR has conflicts only.
	return !mr.HasConflicts()
}

func (w *GitlabEventWorker) postMessageToMergeRequest(ctx context.Context, event vcs.MRCommentEvent, msg string) {
	ctx, span := otel.Tracer("GitlabHooks").Start(context.Background(), "postMessageToMergeRequest")
	defer span.End()

	if err := w.gl.CreateMergeRequestComment(
		ctx,
		event.GetMR().GetInternalID(),
		event.GetProject().GetPathWithNamespace(),
		msg,
	); err != nil {
		log.Error().Err(err).Msg("could not post message to MR")
	}
}

func (w *GitlabEventWorker) postErrorToMergeRequest(ctx context.Context, event vcs.MRCommentEvent, err error) {
	ctx, span := otel.Tracer("GitlabHooks").Start(context.Background(), "postErrorToMergeRequest")
	defer span.End()
	span.RecordError(err)

	w.postMessageToMergeRequest(ctx, event, fmt.Sprintf(":fire: <br> Error: %v", err))
}

// nonBlockingJobStatuses are the GitLab job states that leave a pipeline
// eligible for apply. "manual" and "skipped" jobs never run on their own, so
// waiting on them would block forever; everything else (failed, canceled,
// pending, running, created, ...) means the pipeline has not succeeded yet.
var nonBlockingJobStatuses = map[string]bool{
	"success": true,
	"skipped": true,
	"manual":  true,
}

// checkPipelineStatus reports whether the merge request pipeline for commitSHA
// has succeeded, and posts the reason to the MR when it has not. It gates on
// two things: TFBuddy's require-pipeline-success setting, and the GitLab
// project's own only_allow_merge_if_pipeline_succeeds.
//
// TFBuddy's own TFC/* commit statuses are attached to that same pipeline, and
// TFC/apply/<workspace> is pending exactly when an apply is due, so the gate
// judges the pipeline's individual job statuses rather than its rolled-up
// status. Otherwise the pending apply status would block the apply meant to
// clear it.
func (w *GitlabEventWorker) checkPipelineStatus(ctx context.Context, event vcs.MRCommentEvent, commitSHA string) bool {
	if !w.cfg.RequirePipelineSuccess {
		return true
	}

	ctx, span := otel.Tracer("hooks").Start(ctx, "checkPipelineStatus")
	defer span.End()

	proj := event.GetProject().GetPathWithNamespace()

	// GitLab already records whether a red pipeline should stop a merge, so
	// TFBuddy defers to that setting rather than making projects opt in twice.
	settings, err := w.gl.GetProjectSettings(ctx, proj)
	if err != nil {
		w.postErrorToMergeRequest(ctx, event, fmt.Errorf("could not get project settings from GitlabAPI: %v", err))
		return false
	}
	if !settings.OnlyAllowMergeIfPipelineSucceeds() {
		log.Warn().Str("project", proj).Msg("require-pipeline-success is enabled but the project does not set only_allow_merge_if_pipeline_succeeds, not gating apply")
		return true
	}

	pipelines, err := w.gl.GetPipelinesForCommit(ctx, proj, commitSHA)
	if err != nil {
		w.postErrorToMergeRequest(ctx, event, fmt.Errorf("could not get pipelines for commit from GitlabAPI: %v", err))
		return false
	}

	// Pipelines with source "external" are the ones GitLab creates implicitly for
	// TFBuddy's own commit statuses, so they carry no CI of their own.
	candidates := make([]vcs.ProjectPipeline, 0, len(pipelines))
	for _, p := range pipelines {
		if p.GetSource() != vcs.PipelineSourceExternal {
			candidates = append(candidates, p)
		}
	}
	pipeline := vcs.SelectPipelineForCommit(candidates)
	if pipeline == nil {
		log.Debug().Str("project", proj).Str("commitSHA", commitSHA).Msg("no CI pipeline for commit, not gating apply")
		return true
	}

	statuses, err := w.gl.GetCommitJobStatuses(ctx, proj, commitSHA)
	if err != nil {
		w.postErrorToMergeRequest(ctx, event, fmt.Errorf("could not get commit statuses from GitlabAPI: %v", err))
		return false
	}

	// Collect every outstanding job rather than returning on the first. Naming
	// one blocker reads as though it is the only one, which invites a
	// fix-one-rerun-repeat loop when several jobs are red.
	var blocking []string
	for _, status := range statuses {
		if status.GetPipelineID() != pipeline.GetID() {
			continue
		}
		if vcs.IsTFBuddyCommitStatus(status) || status.GetAllowFailure() {
			continue
		}
		if nonBlockingJobStatuses[status.GetStatus()] {
			continue
		}
		blocking = append(blocking, fmt.Sprintf("%s (%s)", status.GetName(), status.GetStatus()))
	}
	if len(blocking) == 0 {
		return true
	}

	// Sort so the message is stable regardless of the order GitLab returns
	// statuses in, which keeps repeated comments comparable.
	sort.Strings(blocking)

	span.SetAttributes(
		attribute.Int("pipeline_id", pipeline.GetID()),
		attribute.String("pipeline_status", pipeline.GetStatus()),
		attribute.String("pipeline_url", pipeline.GetWebURL()),
		attribute.StringSlice("blocking_jobs", blocking),
	)
	w.postMessageToMergeRequest(ctx, event, fmt.Sprintf(
		":no_entry: Apply failed. All jobs in %s (%s) must succeed before apply. Still waiting on: %s.",
		vcs.DescribePipeline(pipeline), pipeline.GetStatus(), strings.Join(blocking, ", "),
	))
	return false
}
