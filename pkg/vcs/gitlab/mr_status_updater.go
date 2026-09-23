package gitlab

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/hashicorp/go-tfe"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/utils"
	"github.com/zapier/tfbuddy/pkg/vcs"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
	"go.opentelemetry.io/otel"
)

// Sentinel error
var errNoPipelineStatus = errors.New("nil pipeline status")

func (p *RunStatusUpdater) updateCommitStatusForRun(ctx context.Context, run *tfe.Run, rmd runstream.RunMetadata) error {
	ctx, span := otel.Tracer("TFC").Start(ctx, "updateCommitStatusForRun")
	defer span.End()

	switch run.Status {
	// https://www.terraform.io/cloud-docs/api-docs/run#run-states
	case tfe.RunPending:
		// The initial status of a run once it has been created.
		if rmd.GetAction() == runstream.PlanAction {
			p.updateStatus(ctx, gogitlab.Pending, "plan", rmd)
			p.updateStatus(ctx, gogitlab.Failed, "apply", rmd)
		} else {
			p.updateStatus(ctx, gogitlab.Pending, "apply", rmd)
		}

	case tfe.RunApplyQueued:
		// Once the changes in the plan have been confirmed, the run run will transition to apply_queued.
		// This status indicates that the run should start as soon as the backend services have available capacity.
		p.updateStatus(ctx, gogitlab.Pending, "apply", rmd)

	case tfe.RunApplying:
		// The applying phase of a run is in progress.
		p.updateStatus(ctx, gogitlab.Running, "apply", rmd)

	case tfe.RunApplied:
		if len(run.TargetAddrs) > 0 {
			p.updateStatus(ctx, gogitlab.Pending, "apply", rmd)
			return nil
		}
		// The applying phase of a run has completed.
		p.updateStatus(ctx, gogitlab.Success, "apply", rmd)
		return p.mergeMRIfPossible(ctx, rmd)

	case tfe.RunCanceled:
		// The run has been discarded. This is a final state.
		p.updateStatus(ctx, gogitlab.Failed, rmd.GetAction(), rmd)

	case tfe.RunDiscarded:
		// The run has been discarded. This is a final state.
		p.updateStatus(ctx, gogitlab.Failed, "plan", rmd)
		p.updateStatus(ctx, gogitlab.Failed, "apply", rmd)

	case tfe.RunErrored:
		// The run has errored. This is a final state.
		p.updateStatus(ctx, gogitlab.Failed, rmd.GetAction(), rmd)

	case tfe.RunPlanning:
		// The planning phase of a run is in progress.
		p.updateStatus(ctx, gogitlab.Running, rmd.GetAction(), rmd)

	case tfe.RunPlanned:
		// this status is for Apply runs (as opposed to `RunPlannedAndFinished` below, so don't update the status.
		return nil

	case tfe.RunPlannedAndFinished:
		// The completion of a run containing a plan only, or a run the produces a plan with no changes to apply.
		// This is a final state.
		log.Debug().Str("project", rmd.GetMRProjectNameWithNamespace()).Int("mergeRequestID", rmd.GetMRInternalID()).Msg("planned and finished")
		p.updateStatus(ctx, gogitlab.Success, rmd.GetAction(), rmd)
		if run.HasChanges {
			p.updateStatus(ctx, gogitlab.Pending, "apply", rmd)
		} else {
			// if the apply returns no changes we can still go ahead and merge if auto-merge is enabled
			if len(run.TargetAddrs) == 0 && rmd.GetAction() == runstream.ApplyAction {
				return p.mergeMRIfPossible(ctx, rmd)
			}
		}

	case tfe.RunPolicySoftFailed:
		// A sentinel policy has soft failed for a plan-only run. This is a final state.
		// During the apply, the policy failure will need to be overriden.
		log.Debug().Str("project", rmd.GetMRProjectNameWithNamespace()).Int("mergeRequestID", rmd.GetMRInternalID()).Msg("policy soft failed")
		if p.cfg.FailCIOnSentinelSoftFail && rmd.GetAction() == runstream.PlanAction {
			p.updateStatus(ctx, gogitlab.Failed, "plan", rmd)
		} else {
			p.updateStatus(ctx, gogitlab.Success, rmd.GetAction(), rmd)
		}

	case tfe.RunPolicyChecked:
		// The sentinel policy checking phase of a run has completed.

		// no op

	default:
		log.Debug().Str("project", rmd.GetMRProjectNameWithNamespace()).Int("mergeRequestID", rmd.GetMRInternalID()).Str("status", string(run.Status)).Msg("ignoring run status")
		return nil
	}

	return nil
}

func (p *RunStatusUpdater) updateStatus(ctx context.Context, state gogitlab.BuildStateValue, action string, rmd runstream.RunMetadata) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "updateStatus")
	defer span.End()

	status := &gogitlab.SetCommitStatusOptions{
		Name:        statusName(rmd.GetWorkspace(), action),
		Context:     statusName(rmd.GetWorkspace(), action),
		TargetURL:   runUrlForTFRunMetadata(rmd),
		Description: descriptionForState(state),
		State:       state,
	}

	// Look up the latest pipeline ID for this MR, since Gitlab is eventually consistent
	// Once we have a pipeline ID returned, we know we have a valid pipeline to set commit status for
	var pipelineID *int
	getPipelineIDFn := func() error {
		log.Debug().Str("project", rmd.GetMRProjectNameWithNamespace()).Int("mergeRequestID", rmd.GetMRInternalID()).Msg("getting pipeline status")
		pipelineID = p.getLatestPipelineID(ctx, rmd)
		if pipelineID == nil {
			return errNoPipelineStatus
		}
		return nil
	}

	err := backoff.Retry(getPipelineIDFn, configureBackOff())
	if err != nil {
		log.Warn().Str("project", rmd.GetMRProjectNameWithNamespace()).Int("mergeRequestID", rmd.GetMRInternalID()).Msg("could not retrieve pipeline id after multiple attempts")
	}
	if pipelineID != nil {
		log.Trace().Int("pipeline_id", *pipelineID).Msg("pipeline status")
		status.PipelineID = pipelineID
	}

	log.Debug().Str("project", rmd.GetMRProjectNameWithNamespace()).Int("mergeRequestID", rmd.GetMRInternalID()).Interface("new_status", status).Msg("updating Gitlab commit status")
	cs, err := p.client.SetCommitStatus(
		ctx,
		rmd.GetMRProjectNameWithNamespace(),
		rmd.GetCommitSHA(),
		&GitlabCommitStatusOptions{status},
	)
	if err != nil {
		log.Error().Str("project", rmd.GetMRProjectNameWithNamespace()).Int("mergeRequestID", rmd.GetMRInternalID()).Err(err).Interface("status", status).Msg("could not update status")
		return
	}
	log.Debug().Str("project", rmd.GetMRProjectNameWithNamespace()).Int("mergeRequestID", rmd.GetMRInternalID()).Interface("commit_status", cs.Info()).Msg("updated Commit Status")
}

// statusName builds the commit status name TFBuddy publishes. The
// vcs.CommitStatusPrefix prefix is what lets the apply gate tell TFBuddy's own
// statuses apart from a project's CI jobs, so the two must stay in step.
func statusName(ws, action string) *string {
	return ptr(fmt.Sprintf("%s%v/%s", vcs.CommitStatusPrefix, action, ws))
}

func descriptionForState(state gogitlab.BuildStateValue) *string {
	switch state {
	case gogitlab.Pending:
		return ptr("pending...")
	case gogitlab.Running:
		return ptr("in progress...")
	case gogitlab.Failed:
		return ptr("failed.")
	case gogitlab.Success:
		return ptr("succeeded.")
	}
	return ptr("unknown")
}

func runUrlForTFRunMetadata(rmd runstream.RunMetadata) *string {
	return ptr(fmt.Sprintf(
		"https://app.terraform.io/app/%s/workspaces/%s/runs/%s",
		rmd.GetOrganization(),
		rmd.GetWorkspace(),
		rmd.GetRunID(),
	))
}

func (p *RunStatusUpdater) getLatestPipelineID(ctx context.Context, rmd runstream.RunMetadata) *int {
	pipelines, err := p.client.GetPipelinesForCommit(ctx, rmd.GetMRProjectNameWithNamespace(), rmd.GetCommitSHA())
	if err != nil {
		log.Error().Str("project", rmd.GetMRProjectNameWithNamespace()).Int("mergeRequestID", rmd.GetMRInternalID()).Err(err).Msg("could not retrieve pipelines for commit")
		return nil
	}
	log.Trace().Interface("pipelines", pipelines).Msg("retrieved pipelines for commit")
	// Prefers the merge request pipeline, and otherwise falls back to the newest
	// pipeline when GitLab reports no merge request pipeline.
	selected := vcs.SelectPipelineForCommit(pipelines)
	if selected == nil {
		return nil
	}
	if selected.GetSource() != vcs.PipelineSourceMergeRequestEvent {
		log.Debug().Str("project", rmd.GetMRProjectNameWithNamespace()).Int("mergeRequestID", rmd.GetMRInternalID()).Msg("No merge request pipeline ID found for the commit. Using latest pipeline ID as fallback...")
	}
	return ptr(selected.GetID())
}

func (p *RunStatusUpdater) mergeMRIfPossible(ctx context.Context, rmd runstream.RunMetadata) error {
	ctx, span := otel.Tracer("TFC").Start(ctx, "mergeMRIfPossible")
	defer span.End()

	if !p.cfg.AllowAutoMerge {
		return nil
	}

	ref := runstream.AutoMergeRefForRun(rmd)
	shouldMerge, err := p.rs.RecordAutoMergeSuccess(ref)
	if errors.Is(err, runstream.ErrAutoMergeStateNotFound) {
		log.Warn().
			Str("project", rmd.GetMRProjectNameWithNamespace()).
			Int("mergeRequestID", rmd.GetMRInternalID()).
			Str("commitSHA", rmd.GetCommitSHA()).
			Msg("not auto-merging because no aggregate workspace state exists")
		return nil
	}
	if err != nil {
		span.RecordError(err)
		log.Error().Err(err).
			Str("project", rmd.GetMRProjectNameWithNamespace()).
			Int("mergeRequestID", rmd.GetMRInternalID()).
			Msg("could not update aggregate auto-merge state")
		return err
	}
	if !shouldMerge {
		return nil
	}

	comment := "All expected workspaces have been applied successfully. Auto-merging this MR."
	intentCommentID, commentErr := p.client.CreateMergeRequestCommentWithID(
		ctx,
		rmd.GetMRInternalID(),
		rmd.GetMRProjectNameWithNamespace(),
		comment,
	)
	if commentErr != nil {
		log.Warn().Err(commentErr).
			Str("project", rmd.GetMRProjectNameWithNamespace()).
			Int("mergeRequestID", rmd.GetMRInternalID()).
			Msg("could not post auto-merge intent comment")
	}

	err = p.client.MergeMRAtSHA(ctx, rmd.GetMRInternalID(), rmd.GetMRProjectNameWithNamespace(), rmd.GetCommitSHA())
	if err != nil {
		span.RecordError(err)
		if errors.Is(err, utils.ErrPermanent) {
			// Keep the claim set. Redelivery cannot make a permanent rejection
			// succeed, and releasing it would let duplicate events repeatedly
			// submit the same rejected merge request.
			p.postAutoMergeFailureComment(ctx, rmd, intentCommentID, err)
			log.Warn().Err(err).
				Str("project", rmd.GetMRProjectNameWithNamespace()).
				Int("mergeRequestID", rmd.GetMRInternalID()).
				Msg("auto-merge rejected permanently by GitLab")
			return nil
		}
		if releaseErr := p.rs.ReleaseAutoMergeClaim(ref); releaseErr != nil {
			span.RecordError(releaseErr)
			log.Error().Err(releaseErr).Msg("could not release failed auto-merge claim")
			// The persisted claim makes redelivery unable to retry safely.
			// ACK this event and require operator intervention rather than storm GitLab.
			p.postAutoMergeFailureComment(ctx, rmd, intentCommentID, err)
			return nil
		}
	} else if stateErr := p.rs.RecordAutoMergeRequested(ref); stateErr != nil {
		span.RecordError(stateErr)
		log.Error().Err(stateErr).
			Str("project", rmd.GetMRProjectNameWithNamespace()).
			Int("mergeRequestID", rmd.GetMRInternalID()).
			Msg("merge was accepted but auto-merge state could not record the request")
	}
	log.Debug().Str("project", rmd.GetMRProjectNameWithNamespace()).Int("mergeRequestID", rmd.GetMRInternalID()).Str("commitSHA", rmd.GetCommitSHA()).AnErr("err", err).Msg("merge MR")
	return err
}

func (p *RunStatusUpdater) postAutoMergeFailureComment(
	ctx context.Context,
	rmd runstream.RunMetadata,
	intentCommentID int64,
	mergeErr error,
) {
	reason := autoMergeFailureReason(mergeErr)
	serviceAccount := "TFBuddy service account"
	if accountName, err := p.client.GetAuthenticatedAccountName(ctx); err != nil {
		log.Warn().Err(err).
			Msg("could not retrieve authenticated account name for auto-merge failure comment")
	} else if strings.TrimSpace(accountName) != "" {
		serviceAccount = accountName
	}
	comment := fmt.Sprintf(
		"Failed to auto-merge this MR.\n\nReason: %s: %s. Please merge manually.",
		serviceAccount,
		reason,
	)
	if intentCommentID != 0 {
		if err := p.client.UpdateMergeRequestComment(
			ctx,
			rmd.GetMRInternalID(),
			intentCommentID,
			rmd.GetMRProjectNameWithNamespace(),
			comment,
		); err == nil {
			return
		} else {
			log.Warn().Err(err).
				Str("project", rmd.GetMRProjectNameWithNamespace()).
				Int("mergeRequestID", rmd.GetMRInternalID()).
				Int64("noteID", intentCommentID).
				Msg("could not update auto-merge intent comment")
		}
	}
	if err := p.client.CreateMergeRequestComment(
		ctx,
		rmd.GetMRInternalID(),
		rmd.GetMRProjectNameWithNamespace(),
		comment,
	); err != nil {
		log.Warn().Err(err).
			Str("project", rmd.GetMRProjectNameWithNamespace()).
			Int("mergeRequestID", rmd.GetMRInternalID()).
			Msg("could not post auto-merge failure comment")
	}
}

var gitLabHTTPErrorPattern = regexp.MustCompile(`\b(\d{3} \{.*\})$`)

func autoMergeFailureReason(err error) string {
	reason := strings.TrimSuffix(err.Error(), " "+utils.ErrPermanent.Error())
	if match := gitLabHTTPErrorPattern.FindStringSubmatch(reason); len(match) == 2 {
		return match[1]
	}
	return reason
}

// configureBackOff returns a backoff configuration to use to retry requests
func configureBackOff() *backoff.ExponentialBackOff {

	// Lets setup backoff logic to retry this request for 30 seconds
	expBackOff := backoff.NewExponentialBackOff()
	expBackOff.MaxInterval = 10 * time.Second
	expBackOff.MaxElapsedTime = 30 * time.Second

	return expBackOff
}
