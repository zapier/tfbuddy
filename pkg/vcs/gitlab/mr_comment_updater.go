package gitlab

import (
	"context"
	"fmt"

	"github.com/zapier/tfbuddy/pkg/comment_formatter"
	"go.opentelemetry.io/otel"

	"github.com/hashicorp/go-tfe"
	"github.com/rs/zerolog/log"

	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
)

// releaseWorkspaceLockTag removes the TFBuddy tag-based workspace lock when an
// apply-triggered run reaches a terminal state. This prevents stale lock tags
// from blocking future applies after a run finishes while the MR stays open.
func (p *RunStatusUpdater) releaseWorkspaceLockTag(ctx context.Context, run *tfe.Run, rmd runstream.RunMetadata) {
	if rmd.GetAction() != runstream.ApplyAction {
		return
	}
	switch run.Status {
	case tfe.RunApplied, tfe.RunErrored, tfe.RunCanceled, tfe.RunDiscarded:
		if run.Workspace == nil || run.Workspace.ID == "" {
			log.Warn().Str("runID", run.ID).Msg("skipping workspace lock tag cleanup: run.Workspace not populated")
			return
		}
		tag := fmt.Sprintf("%s-%d", tfc_trigger.TFLockTagPrefix, rmd.GetMRInternalID())
		if err := p.tfc.RemoveTagsByQuery(ctx, run.Workspace.ID, tag); err != nil {
			log.Error().Err(err).
				Str("workspace", run.Workspace.Name).
				Str("workspaceID", run.Workspace.ID).
				Int("mrIID", rmd.GetMRInternalID()).
				Msg("could not remove workspace lock tag after apply completion")
		} else {
			log.Info().
				Str("workspace", run.Workspace.Name).
				Int("mrIID", rmd.GetMRInternalID()).
				Str("runStatus", string(run.Status)).
				Msg("released workspace lock tag after apply reached terminal state")
		}
	}
}

func (p *RunStatusUpdater) postRunStatusComment(ctx context.Context, run *tfe.Run, rmd runstream.RunMetadata) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "postRunStatusComment")
	defer span.End()

	p.releaseWorkspaceLockTag(ctx, run, rmd)

	commentBody, topLevelNoteBody, resolveDiscussion := comment_formatter.FormatRunStatusCommentBody(p.tfc, run, rmd)

	var oldUrls string
	var err error

	if run.Status == tfe.RunErrored || run.Status == tfe.RunCanceled || run.Status == tfe.RunDiscarded || run.Status == tfe.RunPlannedAndFinished {
		oldUrls, err = p.client.GetOldRunUrls(ctx, rmd.GetMRInternalID(), rmd.GetMRProjectNameWithNamespace(), int(rmd.GetRootNoteID()), run.Workspace.Name, rmd.GetAction())
		if err != nil {
			log.Error().Str("project", rmd.GetMRProjectNameWithNamespace()).Int("mergeRequestID", rmd.GetMRInternalID()).Err(err).Msg("could not retrieve old run urls")
		}
		if oldUrls != "" {
			topLevelNoteBody = fmt.Sprintf("%s\n\n%s", oldUrls, topLevelNoteBody)
		}
	}
	if topLevelNoteBody != "" {
		if _, err := p.client.UpdateMergeRequestDiscussionNote(
			ctx,
			rmd.GetMRInternalID(),
			int(rmd.GetRootNoteID()),
			rmd.GetMRProjectNameWithNamespace(),
			rmd.GetDiscussionID(),
			topLevelNoteBody,
		); err != nil {
			log.Error().Str("project", rmd.GetMRProjectNameWithNamespace()).Int("mergeRequestID", rmd.GetMRInternalID()).Str("discussionID", rmd.GetDiscussionID()).Err(err).Msg("could not update MR thread")
		}
	}

	if commentBody != "" {
		p.postComment(ctx, fmt.Sprintf(
			"Status: `%s`<br>%s",
			run.Status,
			commentBody),
			rmd.GetMRProjectNameWithNamespace(),
			rmd.GetMRInternalID(),
			rmd.GetDiscussionID(),
		)
	}

	if resolveDiscussion {

		err := p.client.ResolveMergeRequestDiscussion(
			ctx,
			rmd.GetMRProjectNameWithNamespace(),
			rmd.GetMRInternalID(),
			rmd.GetDiscussionID(),
		)
		if err != nil {
			log.Error().Str("project", rmd.GetMRProjectNameWithNamespace()).Int("mergeRequestID", rmd.GetMRInternalID()).Str("discussionID", rmd.GetDiscussionID()).Err(err).Msg("Could not mark MR discussion thread as resolved.")
		}
	}
}

func (p *RunStatusUpdater) postComment(ctx context.Context, commentBody, projectID string, mrIID int, discussionID string) error {
	ctx, span := otel.Tracer("TFC").Start(ctx, "postComment")
	defer span.End()

	content := fmt.Sprintf(MR_COMMENT_FORMAT, commentBody)

	if discussionID != "" {
		_, err := p.client.AddMergeRequestDiscussionReply(ctx, mrIID, projectID, discussionID, content)
		if err != nil {
			log.Error().Str("project", projectID).Int("mergeRequestID", mrIID).Str("discussionID", discussionID).Err(err).Msg("error posting Gitlab discussion reply")
			return err
		}
		return nil
	} else {
		err := p.client.CreateMergeRequestComment(ctx, mrIID, projectID, content)
		if err != nil {
			log.Error().Str("project", projectID).Int("mergeRequestID", mrIID).Err(err).Msg("error posting Gitlab comment to MR")
			return err
		}
		return nil
	}
}

const MR_COMMENT_FORMAT = `
### Terraform Cloud
%s
`
