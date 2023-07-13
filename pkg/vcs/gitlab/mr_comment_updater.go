package gitlab

import (
	"fmt"

	"github.com/zapier/tfbuddy/pkg/comment_formatter"

	"github.com/hashicorp/go-tfe"
	"github.com/rs/zerolog/log"

	"github.com/zapier/tfbuddy/pkg/runstream"
)

func (p *RunStatusUpdater) postRunStatusComment(run *tfe.Run, rmd runstream.RunMetadata) {

	commentBody, topLevelNoteBody, resolveDiscussion := comment_formatter.FormatRunStatusCommentBody(p.tfc, run, rmd)

	var oldUrls string
	var err error
	// TODO: Make this configurable behaviour
	if run.Status == tfe.RunErrored || run.Status == tfe.RunCanceled || run.Status == tfe.RunDiscarded || run.Status == tfe.RunPlannedAndFinished {
		oldUrls, err = p.client.GetOldRunUrls(rmd.GetMRInternalID(), rmd.GetMRProjectNameWithNamespace(), int(rmd.GetRootNoteID()), true)
		if err != nil {
			log.Error().Err(err).Msg("could not retrieve old run urls")
		}
		if oldUrls != "" {
			topLevelNoteBody = fmt.Sprintf("%s\n%s", oldUrls, topLevelNoteBody)
		}
	}
	//oldURLBlock := utils.CaptureSubstring(, prefix string, suffix string)
	if _, err := p.client.UpdateMergeRequestDiscussionNote(
		rmd.GetMRInternalID(),
		int(rmd.GetRootNoteID()),
		rmd.GetMRProjectNameWithNamespace(),
		rmd.GetDiscussionID(),
		topLevelNoteBody,
	); err != nil {
		log.Error().Err(err).Msg("could not update MR thread")
	}

	if commentBody != "" {
		p.postComment(fmt.Sprintf(
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
			rmd.GetMRProjectNameWithNamespace(),
			rmd.GetMRInternalID(),
			rmd.GetDiscussionID(),
		)
		if err != nil {
			log.Error().Err(err).Msg("Could not mark MR discussion thread as resolved.")
		}
	}
}

func (p *RunStatusUpdater) postComment(commentBody, projectID string, mrIID int, discussionID string) error {
	content := fmt.Sprintf(MR_COMMENT_FORMAT, commentBody)

	if discussionID != "" {
		_, err := p.client.AddMergeRequestDiscussionReply(mrIID, projectID, discussionID, content)
		if err != nil {
			log.Error().Err(err).Msg("error posting Gitlab discussion reply")
			return err
		}
		return nil
	} else {
		err := p.client.CreateMergeRequestComment(mrIID, projectID, content)
		if err != nil {
			log.Error().Err(err).Msg("error posting Gitlab comment to MR")
			return err
		}
		return nil
	}
}

func hasChanges(plan *tfe.Plan) bool {
	if plan.ResourceAdditions > 0 {
		return true
	}
	if plan.ResourceDestructions > 0 {
		return true
	}
	if plan.ResourceChanges > 0 {
		return true
	}
	return false
}

const MR_COMMENT_FORMAT = `
### Terraform Cloud
%s
`
