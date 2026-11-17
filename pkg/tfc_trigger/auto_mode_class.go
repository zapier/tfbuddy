package tfc_trigger

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.opentelemetry.io/otel"
)

// ClassPlanRef identifies the class plans of one commit of an Auto Mode MR.
func ClassPlanRef(project string, mrIID int, commitSHA string) runstream.ClassPlanRef {
	return runstream.ClassPlanRef{VcsProvider: "gitlab", Project: project, MergeRequest: mrIID, CommitSHA: commitSHA}
}

func (t *TFCTrigger) classPlanRef() runstream.ClassPlanRef {
	return ClassPlanRef(t.GetProjectNameWithNamespace(), t.GetMergeRequestIID(), t.GetCommitSHA())
}

// recordUnplannedWorkspaces fails, in the class plans, every class workspace
// that this trigger did not start planning: errored are the ones whose
// dispatch failed, and executed the ones it started. It returns the
// workspaces that did not plan, and the state when this finished it.
func (t *TFCTrigger) recordUnplannedWorkspaces(status *TriggeredTFCWorkspaces) ([]string, *runstream.ClassPlanState) {
	errored := map[string]string{}
	for _, ws := range status.Errored {
		errored[ws.Name] = ws.Error
	}
	var notPlanned []string
	var finishedState *runstream.ClassPlanState
	for _, name := range t.cfg.ClassWorkspaces {
		detail := ""
		if reason, ok := errored[name]; ok {
			detail = "did not plan: " + reason
		} else if !slices.Contains(status.Executed, name) {
			detail = "TFBuddy did not plan it"
		} else {
			continue
		}
		notPlanned = append(notPlanned, name)
		state, finished, err := t.runstream.RecordClassPlanResult(t.classPlanRef(), name, "", t.dispatchTrigger(), false, detail)
		if err != nil {
			// TFC/class still fails: the caller falls back to Manual Mode.
			log.Error().Err(err).Str("workspace", name).Msg("could not record a workspace that did not plan")
			continue
		}
		if finished {
			finishedState = state
		}
	}
	return notPlanned, finishedState
}

// ReportClassPlans publishes the result of a commit whose class plans have
// all finished: TFC/class, and one MR comment, posted once and then updated,
// that names the class, the commit and each workspace's plan run and result.
// TFC/class passes only when every plan passed and the MR has not fallen back
// to Manual Mode in the meantime; for an MR that has, it is skipped with the
// reason, so that it does not block the merge.
//
// It returns an error when the MR's mode could not be read, TFC/class could
// not be written, or the report could not be recorded, so that the caller
// can retry: until then the state is finished but not reported, and the next
// event for the commit reports it again. The summary comment is a
// notification, so a failure to post it is only logged. Plans that another
// trigger has begun again for the commit are not reported: the new plans
// report themselves.
func ReportClassPlans(ctx context.Context, gl vcs.GitClient, rs runstream.StreamClient, state *runstream.ClassPlanState) error {
	ctx, span := otel.Tracer("TFC").Start(ctx, "ReportClassPlans")
	defer span.End()

	ref := state.Ref()
	current, err := rs.GetClassPlans(ref)
	if err != nil && !errors.Is(err, runstream.ErrClassPlansNotFound) {
		span.RecordError(err)
		return fmt.Errorf("could not read the class plans of commit %s: %w", ref.CommitSHA, err)
	}
	if current != nil && current.Trigger != state.Trigger {
		log.Info().Str("project", ref.Project).Int("mergeRequestID", ref.MergeRequest).Str("commitSHA", ref.CommitSHA).
			Msg("not reporting class plans that another trigger has begun again")
		return nil
	}
	manual, reason, err := rs.IsAutoModeManual(ref.MR())
	if err != nil {
		log.Error().Err(err).Str("project", ref.Project).Int("mergeRequestID", ref.MergeRequest).
			Msg("could not read whether the MR fell back to Manual Mode; failing its class")
	}

	status := vcs.ClassStatus{Project: ref.Project, CommitSHA: ref.CommitSHA, MergeRequestIID: ref.MergeRequest}
	result := "failed"
	switch {
	case err != nil:
		status.State = vcs.CommitStateFailed
		status.Description = fmt.Sprintf("class %s failed: TFBuddy could not read whether the MR is in Manual Mode", state.Class)
	case manual:
		status.State = vcs.CommitStateSkipped
		status.Description = ManualModeClassDescription(reason)
		result = "skipped"
	case state.Passed():
		status.State = vcs.CommitStateSuccess
		status.Description = fmt.Sprintf("class %s: every plan passed", state.Class)
		result = "passed"
	default:
		status.State = vcs.CommitStateFailed
		status.Description = fmt.Sprintf("class %s failed: the plan for %s did not pass", state.Class, strings.Join(state.FailedWorkspaces(), ", "))
	}
	if serr := setClassStatus(ctx, gl, status); serr != nil {
		span.RecordError(serr)
		return fmt.Errorf("could not report the class plans of commit %s: %w", ref.CommitSHA, serr)
	}
	if err != nil {
		// TFC/class fails closed until a retry can read the mode.
		span.RecordError(err)
		return fmt.Errorf("could not read whether the MR fell back to Manual Mode: %w", err)
	}
	postClassSummary(ctx, gl, rs, ref, classSummary(state, result, reason))
	if err := rs.MarkClassPlansReported(ref, state.Trigger); err != nil {
		span.RecordError(err)
		return fmt.Errorf("could not record that the class plans of commit %s were reported: %w", ref.CommitSHA, err)
	}
	return nil
}

// classSummary is the body of the class plan comment. result is what TFC/class
// became, and manualReason why the MR is in Manual Mode, if it is.
func classSummary(state *runstream.ClassPlanState, result, manualReason string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### Auto Mode: class `%s`\n\n", state.Class)
	fmt.Fprintf(&b, "Class plans of head commit `%s`: `%s` **%s**.", state.CommitSHA, vcs.ClassStatusName, result)
	if manualReason != "" {
		fmt.Fprintf(&b, " The MR is in Manual Mode because %s: a person must approve it, and someone must comment `tfc apply`.", manualReason)
	}
	b.WriteString("\n\n| Workspace | Plan run | Result |\n| --- | --- | --- |\n")
	names := make([]string, 0, len(state.Workspaces))
	for name := range state.Workspaces {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		ws := state.Workspaces[name]
		run := "none"
		if ws.RunID != "" {
			run = "`" + ws.RunID + "`"
		}
		outcome := string(ws.Outcome)
		if ws.Detail != "" && ws.Outcome != runstream.ClassPlanPassed {
			outcome = fmt.Sprintf("%s: %s", ws.Outcome, strings.ReplaceAll(ws.Detail, "|", "\\|"))
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", name, run, outcome)
	}
	return b.String()
}

// postClassSummary updates the MR's class plan comment, or posts one when
// there is none or it can no longer be updated.
func postClassSummary(ctx context.Context, gl vcs.GitClient, rs runstream.StreamClient, ref runstream.ClassPlanRef, body string) {
	logger := log.With().Str("project", ref.Project).Int("mergeRequestID", ref.MergeRequest).Logger()
	noteID, err := rs.GetAutoModeSummaryNote(ref.MR())
	if err != nil {
		logger.Warn().Err(err).Msg("could not read the class plan comment's ID; posting a new one")
	}
	if noteID != 0 {
		if err := gl.UpdateMergeRequestComment(ctx, ref.MergeRequest, noteID, ref.Project, body); err == nil {
			return
		} else {
			logger.Warn().Err(err).Int64("noteID", noteID).Msg("could not update the class plan comment; posting a new one")
		}
	}
	noteID, err = gl.CreateMergeRequestCommentWithID(ctx, ref.MergeRequest, ref.Project, body)
	if err != nil {
		logger.Error().Err(err).Msg("could not post the class plan comment")
		return
	}
	if err := rs.SetAutoModeSummaryNote(ref.MR(), noteID); err != nil {
		logger.Warn().Err(err).Msg("could not record the class plan comment's ID")
	}
}
