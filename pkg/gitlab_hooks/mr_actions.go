package gitlab_hooks

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/allow_list"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
	"go.opentelemetry.io/otel"
)

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

	trigger := tfc_trigger.NewTFCTrigger(w.cfg, w.gl, w.tfc, w.runstream, cfg)
	if w.workspaceStream != nil {
		trigger.SetWorkspaceStream(w.workspaceStream)
	}
	switch event.ObjectAttributes.Action {
	case "open", "reopen":
		log.Debug().Str("project", projectName).Int("mergeRequestID", event.ObjectAttributes.IID).Msg("triggering TFC events for merge request")
		executed, err := trigger.TriggerTFCEvents(ctx)
		logErroredWorkspaces(projectName, event.ObjectAttributes.IID, executed)
		return projectName, err

	case "update":
		log.Debug().Str("project", projectName).Int("mergeRequestID", event.ObjectAttributes.IID).Msg("triggering TFC events for merge request")
		if event.ObjectAttributes.OldRev != "" && event.ObjectAttributes.OldRev != event.ObjectAttributes.LastCommit.ID {
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
