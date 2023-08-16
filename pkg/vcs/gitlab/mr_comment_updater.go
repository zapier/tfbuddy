package gitlab

import (
	"context"
	"fmt"

	"github.com/zapier/tfbuddy/pkg/comment_formatter"
	"go.opentelemetry.io/otel"

	"github.com/hashicorp/go-tfe"
	"github.com/rs/zerolog/log"

	"github.com/zapier/tfbuddy/pkg/runstream"
)

func (p *RunStatusUpdater) postRunStatusComment(ctx context.Context, run *tfe.Run, rmd runstream.RunMetadata) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "postRunStatusComment")
	defer span.End()

	commentBody, topLevelNoteBody, resolveDiscussion := comment_formatter.FormatRunStatusCommentBody(p.tfc, run, rmd)

	var oldUrls string
	var err error

	if run.Status == tfe.RunErrored || run.Status == tfe.RunCanceled || run.Status == tfe.RunDiscarded || run.Status == tfe.RunPlannedAndFinished {
		oldUrls, err = p.client.GetOldRunUrls(ctx, rmd.GetMRInternalID(), rmd.GetMRProjectNameWithNamespace(), int(rmd.GetRootNoteID()))
		if err != nil {
			log.Error().Err(err).Msg("could not retrieve old run urls")
		}
		if oldUrls != "" {
			topLevelNoteBody = fmt.Sprintf("%s\n%s", oldUrls, topLevelNoteBody)
		}
	}
	//oldURLBlock := utils.CaptureSubstring(, prefix string, suffix string)
	if _, err := p.client.UpdateMergeRequestDiscussionNote(
		ctx,
		rmd.GetMRInternalID(),
		int(rmd.GetRootNoteID()),
		rmd.GetMRProjectNameWithNamespace(),
		rmd.GetDiscussionID(),
		topLevelNoteBody,
	); err != nil {
		log.Error().Err(err).Msg("could not update MR thread")
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
			log.Error().Err(err).Msg("Could not mark MR discussion thread as resolved.")
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
			log.Error().Err(err).Msg("error posting Gitlab discussion reply")
			return err
		}
		return nil
	} else {
		err := p.client.CreateMergeRequestComment(ctx, mrIID, projectID, content)
		if err != nil {
			log.Error().Err(err).Msg("error posting Gitlab comment to MR")
			return err
		}
		return nil
	}
}

const MR_COMMENT_FORMAT = `
### Terraform Cloud
%s
`
