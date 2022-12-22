package hooks

import (
	"errors"
	"fmt"

	gogithub "github.com/google/go-github/v48/github"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/allow_list"
	"github.com/zapier/tfbuddy/pkg/comment_actions"
	"github.com/zapier/tfbuddy/pkg/github"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
)

func (h *GithubHooksHandler) processIssueCommentEvent(msg *GithubIssueCommentEventMsg) error {
	if msg == nil || msg.payload == nil {
		return errors.New("msg is nil")
	}
	event := msg.payload

	// Check if fullName is allowed
	log.Debug().Str("repo", *event.Repo.FullName).Msg("processIssueCommentEvent")
	fullName := event.Repo.FullName
	if !allow_list.IsGithubRepoAllowed(*fullName) {
		return nil
	}

	// Parse comment
	opts, err := comment_actions.ParseCommentCommand(*event.Comment.Body)
	if err != nil {
		if err == comment_actions.ErrOtherTFTool {
			h.postPullRequestComment(event, "Use 'tfc' to interact with TFBuddy")
		}
		if err == comment_actions.ErrNotTFCCommand || err == comment_actions.ErrOtherTFTool {
			githubWebHookIgnored.WithLabelValues(
				"issue_comment_created",
				*fullName,
				"not-tfc-command",
			).Inc()
			return nil
		}
		return err
	}

	pr, err := h.vcs.GetMergeRequest(*event.Issue.Number, event.GetRepo().GetFullName())
	if err != nil {
		log.Error().Err(err).Msg("could not process GitHub IssueCommentEvent")
		return err
	}
	pullReq := pr.(*github.GithubPR)

	trigger := h.triggerCreation(h.vcs, h.tfc, h.runstream,
		&tfc_trigger.TFCTriggerConfig{
			Branch:                   pr.GetSourceBranch(),
			CommitSHA:                pullReq.GetBase().GetSHA(),
			ProjectNameWithNamespace: event.GetRepo().GetFullName(),
			MergeRequestIID:          *event.Issue.Number,
			TriggerSource:            tfc_trigger.CommentTrigger,
			VcsProvider:              "github",
		})

	//// TODO: support additional commands and arguments (e.g. destroy, refresh, lock, unlock)
	//// TODO: this should be refactored and be agnostic to the VCS type
	switch opts.Args.Command {
	case "apply":
		log.Info().Msg("Got TFC apply command")
		if !pullReq.IsApproved() {
			h.postPullRequestComment(event, ":no_entry: Apply failed. Pull Request requires approval.")
			return nil
		}

		if pullReq.HasConflicts() {
			h.postPullRequestComment(event, ":no_entry: Apply failed. Pull Request has conflicts that need to be resolved.")
			return nil
		}
		trigger.GetConfig().SetAction(tfc_trigger.ApplyAction)
		trigger.GetConfig().SetWorkspace(opts.Workspace)

	case "lock":
		log.Info().Msg("Got TFC lock command")
		trigger.GetConfig().SetAction(tfc_trigger.LockAction)
		trigger.GetConfig().SetWorkspace(opts.Workspace)

	case "plan":
		log.Info().Msg("Got TFC plan command")
		trigger.GetConfig().SetAction(tfc_trigger.PlanAction)
		trigger.GetConfig().SetWorkspace(opts.Workspace)

	case "unlock":
		log.Info().Msg("Got TFC unlock command")
		trigger.GetConfig().SetAction(tfc_trigger.UnlockAction)
		trigger.GetConfig().SetWorkspace(opts.Workspace)

	default:
		return fmt.Errorf("could not parse command")
	}
	executedWorkspaces, tfError := trigger.TriggerTFCEvents()
	if tfError == nil && len(executedWorkspaces.Errored) > 0 {
		failedMsg := ""
		for _, failedWS := range executedWorkspaces.Errored {
			failedMsg += fmt.Sprintf("%s could not be run because: %s\n", failedWS.Name, failedWS.Error)
		}
		h.postPullRequestComment(event, fmt.Sprintf(":no_entry: %s", failedMsg))
		return nil
	}
	return tfError

}

func (h *GithubHooksHandler) postPullRequestComment(event *gogithub.IssueCommentEvent, body string) error {
	log.Debug().Msg("postPullRequestComment")

	prID := event.GetIssue().GetNumber()
	log.Debug().Str("repo", event.GetRepo().GetFullName()).Int("PR", prID).Msg("postPullRequestComment")
	return h.vcs.CreateMergeRequestComment(prID, event.GetRepo().GetFullName(), body)
}
