package gitlab_hooks

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/xanzy/go-gitlab"
	"github.com/zapier/tfbuddy/pkg/allow_list"
	"github.com/zapier/tfbuddy/pkg/comment_actions"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

// processNoteEvent processes GitLab Webhooks for Note events
// In the Gitlab API, MR comments are called Notes
func (w *GitlabEventWorker) processNoteEvent(ctx context.Context, event vcs.MRCommentEvent) (projectName string, err error) {
	ctx, span := otel.Tracer("hooks").Start(ctx, "ProcessNoteEvent")
	defer span.End()

	proj := event.GetProject().GetPathWithNamespace()
	if !allow_list.IsGitlabProjectAllowed(proj) {
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

	cfg, err := tfc_trigger.NewTFCTriggerConfig(opts.TriggerOpts)
	if err != nil {
		log.Error().Err(err).Msg("could not create TFCTriggerConfig")
		return proj, err
	}

	trigger := w.triggerCreation(w.gl, w.tfc, w.runstream, cfg)

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
		log.Info().Msg("Got TFC apply command")
		if !w.checkApproval(ctx, event) {
			w.postMessageToMergeRequest(ctx, event, ":no_entry: Apply failed. Merge Request requires approval.")
			return proj, nil
		}
		if !w.checkForMergeConflicts(ctx, event) {
			w.postMessageToMergeRequest(ctx, event, ":no_entry: Apply failed. Merge Request has conflicts that need to be resolved.")
			return proj, nil
		}
	case "lock":
		log.Info().Msg("Got TFC lock command")
	case "plan":
		log.Info().Msg("Got TFC plan command")
	case "unlock":
		log.Info().Msg("Got TFC unlock command")
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
	ctx, span := otel.Tracer("hooks").Start(ctx, "CheckForMergeConflicts")
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
