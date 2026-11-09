package gitlab_hooks

import (
	"context"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/allow_list"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
	"go.opentelemetry.io/otel"
)

// skipPlanReason is the commit status description posted for every workspace
// TFBUDDY_SKIP_AUTO_PLAN_LABELS skips.
const skipPlanReason = "skipped: merge request has a skip-auto-plan label"

func (w *GitlabEventWorker) processMergeRequestEvent(msg *MergeRequestEventMsg) (projectName string, err error) {
	ctx, span := otel.Tracer("GitlabHandler").Start(msg.Context, "processMergeRequestEvent")
	defer span.End()

	log.Trace().Msg("processMergeRequestEvent()")

	labels := prometheus.Labels{}
	labels["eventType"] = string(gogitlab.EventTypeMergeRequest)

	event := msg.Payload

	projectName = event.Project.PathWithNamespace
	labels["project"] = projectName
	if !allow_list.IsGitlabProjectAllowed(w.cfg, event.Project.PathWithNamespace) {
		log.Warn().Str("project", event.Project.Name).Msg("project not authorized")
		labels["reason"] = "project-not-authorized"
		gitlabWebHookIgnored.With(labels).Inc()
		return projectName, nil
	}

	cfg, err := tfc_trigger.NewTFCTriggerConfig(&tfc_trigger.TFCTriggerOptions{
		Action:                   tfc_trigger.PlanAction,
		Branch:                   event.ObjectAttributes.SourceBranch,
		CommitSHA:                event.ObjectAttributes.LastCommit.ID,
		ProjectNameWithNamespace: event.ObjectAttributes.Source.PathWithNamespace,
		MergeRequestIID:          event.ObjectAttributes.IID,
		TriggerSource:            tfc_trigger.MergeRequestEventTrigger,
		VcsProvider:              "gitlab",
		DeliveryID:               msg.DeliveryID,
	})
	if err != nil {
		log.Error().Err(err).Msg("could not create TFCTriggerConfig")
		return projectName, err
	}

	trigger := w.triggerCreation(w.cfg, w.gl, w.tfc, w.runstream, cfg)
	switch event.ObjectAttributes.Action {
	case "open", "reopen":
		if w.skipAutoPlan(ctx, projectName, event, trigger, labels) {
			return projectName, nil
		}
		log.Debug().Str("project", projectName).Int("mergeRequestID", event.ObjectAttributes.IID).Msg("triggering TFC events for merge request")
		executed, err := trigger.TriggerTFCEvents(ctx)
		logErroredWorkspaces(projectName, event.ObjectAttributes.IID, executed)
		return projectName, err

	case "update":
		if event.ObjectAttributes.OldRev != "" && event.ObjectAttributes.OldRev != event.ObjectAttributes.LastCommit.ID {
			if w.skipAutoPlan(ctx, projectName, event, trigger, labels) {
				return projectName, nil
			}
			log.Debug().Str("project", projectName).Int("mergeRequestID", event.ObjectAttributes.IID).Msg("triggering TFC events for merge request")
			executed, err := trigger.TriggerTFCEvents(ctx)
			logErroredWorkspaces(projectName, event.ObjectAttributes.IID, executed)
			return projectName, err
		}

	case "merge", "close":
		return projectName, trigger.TriggerCleanupEvent(ctx)
	default:
		labels["reason"] = "unhandled-action"
		gitlabWebHookIgnored.With(labels).Inc()
		log.Debug().Str("project", projectName).Int("mergeRequestID", event.ObjectAttributes.IID).Str("action", event.ObjectAttributes.Action).Msg("ignoring unknown MR action")
	}

	return projectName, nil
}

// skipAutoPlan reports whether TFBUDDY_SKIP_AUTO_PLAN_LABELS matches one of
// the merge request's labels. When it does, it records the metric/log an
// operator needs to explain the skip, then runs the same workspace discovery
// TriggerTFCEvents would have used and posts a pending TFC/plan/<ws> and
// TFC/apply/<ws> commit status for every workspace the MR would have
// affected — without ever cloning the repo or creating a Terraform Cloud
// run — so a workspace cannot merge in having never been planned, and so a
// gate waiting on any TFC/* status (e.g. `tfc status` in CI) does not time
// out. It returns true so the caller skips calling TriggerTFCEvents.
func (w *GitlabEventWorker) skipAutoPlan(ctx context.Context, projectName string, event *gogitlab.MergeEvent, trigger tfc_trigger.Trigger, metricLabels prometheus.Labels) bool {
	// GitLab's merge request webhook duplicates the MR's labels at both the
	// top level and under object_attributes. Checking both guards against
	// either one being empty, since GitLab does not document either as
	// authoritative.
	if !hasSkipPlanLabel(w.cfg.SkipAutoPlanLabels, event.ObjectAttributes.Labels, event.Labels) {
		return false
	}
	log.Info().Str("project", projectName).Int("mergeRequestID", event.ObjectAttributes.IID).
		Msg("skipping automatic tfc plan: merge request has a skip-plan label")
	metricLabels["reason"] = "skip-plan-label"
	gitlabWebHookIgnored.With(metricLabels).Inc()

	executed, err := trigger.SkipTFCEvents(ctx, skipPlanReason)
	if err != nil {
		log.Error().Err(err).Str("project", projectName).Int("mergeRequestID", event.ObjectAttributes.IID).
			Msg("could not post skip-auto-plan commit statuses")
		return true
	}
	logErroredWorkspaces(projectName, event.ObjectAttributes.IID, executed)
	return true
}

// hasSkipPlanLabel reports whether any label set in labelSets contains any of
// skipLabels. The comparison is case-insensitive since GitLab label titles
// are free text and a mismatched case would otherwise silently fail to skip
// the plan.
func hasSkipPlanLabel(skipLabels []string, labelSets ...[]*gogitlab.EventLabel) bool {
	for _, skip := range skipLabels {
		for _, mrLabels := range labelSets {
			for _, label := range mrLabels {
				if label != nil && strings.EqualFold(label.Title, skip) {
					return true
				}
			}
		}
	}
	return false
}

// logErroredWorkspaces records workspaces that did not run for a merge request
// event. The pipeline carries the user-facing signal — each errored workspace
// leaves a failed or skipped TFC/* commit status — so this is for operators
// only. Posting comments here would repeat on every push.
func logErroredWorkspaces(project string, mrIID int, executed *tfc_trigger.TriggeredTFCWorkspaces) {
	if executed == nil {
		return
	}
	for _, ws := range executed.Errored {
		log.Warn().Str("project", project).Int("mergeRequestID", mrIID).
			Str("workspace", ws.Name).Str("reason", ws.Error).
			Msg("workspace did not run for merge request event")
	}
}
