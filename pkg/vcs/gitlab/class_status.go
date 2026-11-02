package gitlab

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/go-tfe"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.opentelemetry.io/otel"
)

// terminalPlanStatuses are the run states after which a plan run's
// TFC/plan/<workspace> status no longer changes.
var terminalPlanStatuses = map[tfe.RunStatus]bool{
	tfe.RunPlannedAndFinished: true,
	tfe.RunPolicySoftFailed:   true,
	tfe.RunErrored:            true,
	tfe.RunCanceled:           true,
	tfe.RunDiscarded:          true,
}

// updateClassStatus keeps the TFC/class status of an automode merge request in
// step with its plans. It runs after every run event, and only a plan run
// reaching a final state can change the result.
//
// The class is judged from the plans' own outcomes: a plan that errored, was
// canceled or discarded, or failed any policy fails the class. This proof of
// concept does not read a Sentinel class-policy result, because that policy
// does not exist yet; once it does, its result is what should decide TFC/class.
func (p *RunStatusUpdater) updateClassStatus(ctx context.Context, run *tfe.Run, rmd runstream.RunMetadata) {
	class := rmd.GetChangeClass()
	if class == "" || rmd.GetAction() != runstream.PlanAction || !terminalPlanStatuses[run.Status] {
		return
	}
	ctx, span := otel.Tracer("TFC").Start(ctx, "updateClassStatus")
	defer span.End()

	statuses, err := p.client.GetCommitJobStatuses(ctx, rmd.GetMRProjectNameWithNamespace(), rmd.GetCommitSHA())
	if err != nil {
		log.Error().Err(err).Str("project", rmd.GetMRProjectNameWithNamespace()).Int("mergeRequestID", rmd.GetMRInternalID()).
			Msg("could not read plan statuses for the class status")
		return
	}
	state, description := classResult(class, rmd.GetClassWorkspaces(), statuses)
	if state == "" {
		return
	}
	if err := p.client.SetClassStatus(ctx, vcs.ClassStatus{
		Project:         rmd.GetMRProjectNameWithNamespace(),
		CommitSHA:       rmd.GetCommitSHA(),
		MergeRequestIID: rmd.GetMRInternalID(),
		State:           state,
		Description:     description,
	}); err != nil {
		log.Error().Err(err).Str("project", rmd.GetMRProjectNameWithNamespace()).Int("mergeRequestID", rmd.GetMRInternalID()).
			Msg("could not set class status")
	}
}

// classResult derives TFC/class from the TFC/plan/<workspace> statuses of
// workspaces. It returns an empty state while any of them has not finished. A
// workspace with no plan status, or with a skipped one, never counts as clean.
// When a name has several statuses, the worst one counts.
//
// Reading the statuses back is racy: when the last two plans finish together,
// each can read the other as still running, and TFC/class stays pending. That
// fails safe, since nothing applies, but a real implementation should record
// plan outcomes in a compare-and-swap aggregate, as auto-merge does.
func classResult(class string, workspaces []string, statuses []vcs.CommitJobStatus) (vcs.CommitState, string) {
	if len(workspaces) == 0 {
		return vcs.CommitStateFailed, fmt.Sprintf("class %s failed: no workspaces to plan; the MR falls back to reviewed", class)
	}
	const (
		unknown = iota
		succeeded
		running
		failed
	)
	outcome := map[string]int{}
	for _, s := range statuses {
		ws, ok := strings.CutPrefix(s.GetName(), *statusName("", "plan"))
		if !ok {
			continue
		}
		o := failed
		switch s.GetStatus() {
		case "success":
			o = succeeded
		case "created", "pending", "running":
			o = running
		}
		outcome[ws] = max(outcome[ws], o)
	}
	var failedWS []string
	finished := true
	for _, ws := range workspaces {
		switch outcome[ws] {
		case failed:
			failedWS = append(failedWS, ws)
		case unknown, running:
			finished = false
		}
	}
	if len(failedWS) > 0 {
		return vcs.CommitStateFailed, fmt.Sprintf("class %s failed: the plan for %s did not pass; the MR falls back to reviewed", class, strings.Join(failedWS, ", "))
	}
	if !finished {
		return "", ""
	}
	return vcs.CommitStateSuccess, fmt.Sprintf("class %s: every plan passed", class)
}
