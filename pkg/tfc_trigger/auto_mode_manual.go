package tfc_trigger

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.opentelemetry.io/otel"
)

// AutoModeDeps are what a fallback to Manual Mode needs: the store to mark the
// MR in, the VCS to report on, and what asks for the re-plan.
type AutoModeDeps struct {
	AppCfg config.Config
	GL     vcs.GitClient
	TFC    tfc_api.ApiClient
	RS     runstream.StreamClient
	// Replanner asks for the Manual Mode re-plan; nil means none can be
	// asked for, and the MR is told to comment `tfc plan`.
	Replanner ManualModeReplanner
}

// ManualModeReplanner asks for a Manual Mode plan of commitSHA, the head of an
// MR that fell back to Manual Mode. It must return quickly: it is called from
// run-event and webhook callbacks that have to ack within their AckWait, so
// the plan itself runs elsewhere. TFBuddy's implementation publishes an MR
// event to the GitLab hooks stream, and the MR event worker plans it like a
// push.
type ManualModeReplanner interface {
	RequestManualModeReplan(ctx context.Context, project string, mrIID int, commitSHA string) error
}

// ManualModeFallback describes why an Auto Mode MR falls back to Manual Mode.
type ManualModeFallback struct {
	Project         string
	MergeRequestIID int
	// CommitSHA is the commit whose run or check failed.
	CommitSHA string
	// Class is the change class the MR had, if any.
	Class string
	// Reason completes "this MR now runs in Manual Mode because ...".
	Reason string
	// Replan asks for a plan of the MR's head commit without a class, so that
	// a code owner reviews Manual Mode plans. Every fallback sets it except
	// one decided while planning, whose plan is already a Manual Mode plan.
	Replan bool
}

// autoModeRef identifies the MR in the Auto Mode state store.
func autoModeRef(project string, mrIID int) runstream.AutoModeMRRef {
	return runstream.AutoModeMRRef{VcsProvider: "gitlab", Project: project, MergeRequest: mrIID}
}

func (t *TFCTrigger) autoModeRef() runstream.AutoModeMRRef {
	return autoModeRef(t.GetProjectNameWithNamespace(), t.GetMergeRequestIID())
}

func (t *TFCTrigger) autoModeDeps() AutoModeDeps {
	return AutoModeDeps{AppCfg: t.appCfg, GL: t.gl, TFC: t.tfc, RS: t.runstream, Replanner: t.replanner}
}

// fallBackToManualMode is FallBackToManualMode for this trigger's MR and
// commit.
func (t *TFCTrigger) fallBackToManualMode(ctx context.Context, reason string, replan bool) error {
	return FallBackToManualMode(ctx, t.autoModeDeps(), ManualModeFallback{
		Project:         t.GetProjectNameWithNamespace(),
		MergeRequestIID: t.GetMergeRequestIID(),
		CommitSHA:       t.GetCommitSHA(),
		Class:           t.cfg.ChangeClass,
		Reason:          reason,
		Replan:          replan,
	})
}

// FallBackToManualMode puts an Auto Mode MR in Manual Mode until it merges. It
// is the one fallback path for every trigger: an MR that fits no class, a
// failed class check, and an Auto Mode apply that soft-fails a policy or
// errors. The mark is what makes the fallback stick: resolveAutoMode reads it
// before anything else, so no later push, force-push, plan or `tfc apply`
// brings the class marker back, and the hard-mandatory class policy cannot
// fail the MR again and again.
//
// TFC/class becomes skipped on the commit, with the reason: skipped never
// passes the Auto Mode gate (or AppSec's approval check), and unlike failed it
// does not keep the pipeline red, so the Manual Mode MR can still merge. The
// first caller to set the mark also comments on the MR and, with Replan,
// asks for a plan of the head commit without a class. It returns
// an error only when the mark could not be recorded; TFC/class then fails.
func FallBackToManualMode(ctx context.Context, d AutoModeDeps, fb ManualModeFallback) error {
	ctx, span := otel.Tracer("TFC").Start(ctx, "FallBackToManualMode")
	defer span.End()

	logger := log.With().Str("project", fb.Project).Int("mergeRequestID", fb.MergeRequestIID).
		Str("commitSHA", fb.CommitSHA).Str("class", fb.Class).Str("reason", fb.Reason).Logger()

	first, markErr := d.RS.MarkAutoModeManual(autoModeRef(fb.Project, fb.MergeRequestIID), fb.Reason)
	status := vcs.ClassStatus{
		Project:         fb.Project,
		CommitSHA:       fb.CommitSHA,
		MergeRequestIID: fb.MergeRequestIID,
		State:           vcs.CommitStateSkipped,
		Description:     ManualModeClassDescription(fb.Reason),
	}
	if markErr != nil {
		// Without the mark the MR is not in Manual Mode for good, so its
		// class must not look settled.
		status.State = vcs.CommitStateFailed
		status.Description = fmt.Sprintf("class %s failed: %s; TFBuddy could not record the fallback to Manual Mode", fb.Class, fb.Reason)
	}
	if fb.CommitSHA != "" {
		postClassStatus(ctx, d.GL, status)
	}
	if markErr != nil {
		span.RecordError(markErr)
		logger.Error().Err(markErr).Msg("could not record that the MR fell back to Manual Mode")
		return fmt.Errorf("could not record that the MR fell back to Manual Mode: %w", markErr)
	}
	if !first {
		return nil
	}
	logger.Info().Msg("merge request fell back to Manual Mode until it merges")

	msg := fmt.Sprintf(":warning: **Auto Mode**: this MR now runs in Manual Mode because %s. "+
		"It stays in Manual Mode until it merges: a person must review and approve it (approvals from automatic approvers and Auto Mode authors do not count), and someone must comment `tfc apply`.", fb.Reason)
	if fb.Replan {
		msg += " TFBuddy is planning it again without a change class, so that the plans to review are Manual Mode plans."
	}
	if err := d.GL.CreateMergeRequestComment(ctx, fb.MergeRequestIID, fb.Project, msg); err != nil {
		logger.Error().Err(err).Msg("could not post the Manual Mode fallback to the MR")
	}
	if !fb.Replan {
		return nil
	}
	replanErr := errors.New("no re-planner is configured")
	if d.Replanner != nil {
		replanErr = d.Replanner.RequestManualModeReplan(ctx, fb.Project, fb.MergeRequestIID, fb.CommitSHA)
	}
	if replanErr != nil {
		span.RecordError(replanErr)
		logger.Error().Err(replanErr).Msg("could not ask for a Manual Mode re-plan")
		if cerr := d.GL.CreateMergeRequestComment(ctx, fb.MergeRequestIID, fb.Project, fmt.Sprintf(
			":warning: Auto Mode could not plan this MR again without a change class: %v. Comment `tfc plan` to plan it in Manual Mode.", replanErr)); cerr != nil {
			logger.Error().Err(cerr).Msg("could not post the failed re-plan to the MR")
		}
	}
	return nil
}

// ManualModeClassDescription is the TFC/class description on every commit of
// an MR that fell back to Manual Mode for reason.
func ManualModeClassDescription(reason string) string {
	return "Manual Mode: " + reason
}

// postClassStatus publishes a TFC/class status. Like a workspace status, it is
// a notification: a failure is logged and changes nothing else.
func postClassStatus(ctx context.Context, gl vcs.GitClient, status vcs.ClassStatus) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, statusWriteBudget)
		defer cancel()
	}
	if err := gl.SetClassStatus(ctx, status); err != nil {
		log.Error().Err(err).Str("project", status.Project).Int("mergeRequestID", status.MergeRequestIID).
			Msg("could not set class commit status")
	}
}
