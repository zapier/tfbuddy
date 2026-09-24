package gitlab

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/cenkalti/backoff/v4"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/vcs"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
	"go.opentelemetry.io/otel"
)

// maxDescriptionLen mirrors GitLab's own validation:
//
//	validates :ref, :target_url, :description, length: { maximum: 255 }
//
// GitLab rejects an over-long description rather than truncating it, and
// wrapped TFC error strings routinely exceed it, so we cut it ourselves.
const maxDescriptionLen = 255

// buildStateFor maps a provider-agnostic state onto the GitLab build state.
// Every vcs.CommitState has a GitLab equivalent; an unknown value is reported
// as failed rather than silently dropped.
func buildStateFor(state vcs.CommitState) gogitlab.BuildStateValue {
	switch state {
	case vcs.CommitStatePending:
		return gogitlab.Pending
	case vcs.CommitStateRunning:
		return gogitlab.Running
	case vcs.CommitStateSuccess:
		return gogitlab.Success
	case vcs.CommitStateFailed:
		return gogitlab.Failed
	case vcs.CommitStateCanceled:
		return gogitlab.Canceled
	case vcs.CommitStateSkipped:
		return gogitlab.Skipped
	}
	log.Warn().Str("state", string(state)).Msg("unknown commit state, reporting as failed")
	return gogitlab.Failed
}

// commitStateFor is the inverse of buildStateFor, used by the run-event path
// which still speaks gogitlab.BuildStateValue internally.
func commitStateFor(state gogitlab.BuildStateValue) vcs.CommitState {
	switch state {
	case gogitlab.Pending:
		return vcs.CommitStatePending
	case gogitlab.Running:
		return vcs.CommitStateRunning
	case gogitlab.Success:
		return vcs.CommitStateSuccess
	case gogitlab.Canceled:
		return vcs.CommitStateCanceled
	case gogitlab.Skipped:
		return vcs.CommitStateSkipped
	}
	return vcs.CommitStateFailed
}

// truncateDescription cuts s to maxDescriptionLen bytes, backing up to a rune
// boundary so the result is always valid UTF-8.
func truncateDescription(s string) string {
	if len(s) <= maxDescriptionLen {
		return s
	}
	const ellipsis = "..."
	cut := maxDescriptionLen - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + ellipsis
}

// SetWorkspaceStatus publishes one TFC/<action>/<workspace> commit status.
// This is the only place in the codebase that builds one.
func (c *GitlabClient) SetWorkspaceStatus(ctx context.Context, ws vcs.WorkspaceStatus) error {
	ctx, span := otel.Tracer("TFC").Start(ctx, "SetWorkspaceStatus")
	defer span.End()

	state := buildStateFor(ws.State)
	description := ws.Description
	if description == "" {
		description = *descriptionForState(state)
	}
	description = truncateDescription(description)

	status := &gogitlab.SetCommitStatusOptions{
		Name:        statusName(ws.Workspace, ws.Action),
		Context:     statusName(ws.Workspace, ws.Action),
		Description: &description,
		State:       state,
	}
	if ws.TargetURL != "" {
		status.TargetURL = &ws.TargetURL
	}

	// Look up the pipeline ID, since GitLab is eventually consistent. Without
	// one the status lands on a stray "external" pipeline instead of the
	// merge request's.
	var pipelineID *int
	if err := backoff.Retry(func() error {
		pipelineID = c.latestPipelineID(ctx, ws.Project, ws.CommitSHA, ws.MergeRequestIID)
		if pipelineID == nil {
			return errNoPipelineStatus
		}
		return nil
	}, configureBackOff()); err != nil {
		log.Warn().Str("project", ws.Project).Int("mergeRequestID", ws.MergeRequestIID).
			Msg("could not retrieve pipeline id after multiple attempts")
	}
	status.PipelineID = pipelineID

	cs, err := c.SetCommitStatus(ctx, ws.Project, ws.CommitSHA, &GitlabCommitStatusOptions{status})
	if err != nil {
		return fmt.Errorf("could not set commit status for workspace %s: %w", ws.Workspace, err)
	}
	log.Debug().Str("project", ws.Project).Int("mergeRequestID", ws.MergeRequestIID).
		Interface("commit_status", cs.Info()).Msg("updated Commit Status")
	return nil
}

// latestPipelineID prefers the merge request pipeline and falls back to the
// newest pipeline for the commit.
func (c *GitlabClient) latestPipelineID(ctx context.Context, project, commitSHA string, mrIID int) *int {
	pipelines, err := c.GetPipelinesForCommit(ctx, project, commitSHA)
	if err != nil {
		log.Error().Str("project", project).Int("mergeRequestID", mrIID).Err(err).
			Msg("could not retrieve pipelines for commit")
		return nil
	}
	log.Trace().Interface("pipelines", pipelines).Msg("retrieved pipelines for commit")
	selected := vcs.SelectPipelineForCommit(pipelines)
	if selected == nil {
		return nil
	}
	if selected.GetSource() != vcs.PipelineSourceMergeRequestEvent {
		log.Debug().Str("project", project).Int("mergeRequestID", mrIID).
			Msg("No merge request pipeline ID found for the commit. Using latest pipeline ID as fallback...")
	}
	return ptr(selected.GetID())
}
