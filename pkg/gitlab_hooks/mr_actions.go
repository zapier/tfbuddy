package gitlab_hooks

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
	gogitlab "github.com/xanzy/go-gitlab"
	"github.com/zapier/tfbuddy/pkg/allow_list"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
)

func (w *GitlabEventWorker) processMergeRequestEvent(msg *MergeRequestEventMsg) (projectName string, err error) {
	log.Trace().Msg("processMergeRequestEvent()")

	labels := prometheus.Labels{}
	labels["eventType"] = string(gogitlab.EventTypeMergeRequest)

	event := msg.payload
	projectName = event.Project.PathWithNamespace
	labels["project"] = projectName
	if !allow_list.IsGitlabProjectAllowed(event.Project.PathWithNamespace) {
		log.Warn().Str("project", event.Project.Name).Msg("project not authorized")
		labels["reason"] = "project-not-authorized"
		gitlabWebHookIgnored.With(labels).Inc()
		return projectName, nil
	}

	trigger := tfc_trigger.NewTFCTrigger(w.gl, w.tfc, w.runstream,
		&tfc_trigger.TFCTriggerConfig{
			Action:                   tfc_trigger.PlanAction,
			Branch:                   event.ObjectAttributes.SourceBranch,
			CommitSHA:                event.ObjectAttributes.LastCommit.ID,
			ProjectNameWithNamespace: event.ObjectAttributes.Source.PathWithNamespace,
			MergeRequestIID:          event.ObjectAttributes.IID,
			TriggerSource:            tfc_trigger.MergeRequestEventTrigger,
			VcsProvider:              "gitlab",
		})
	switch event.ObjectAttributes.Action {
	case "open", "reopen":
		_, err := trigger.TriggerTFCEvents()
		return projectName, err

	case "update":
		if event.ObjectAttributes.OldRev != "" && event.ObjectAttributes.OldRev != event.ObjectAttributes.LastCommit.ID {
			_, err := trigger.TriggerTFCEvents()
			return projectName, err
		}

	case "merge", "close":
		return projectName, trigger.TriggerCleanupEvent()
	default:
		labels["reason"] = "unhandled-action"
		gitlabWebHookIgnored.With(labels).Inc()
		log.Debug().Str("action", event.ObjectAttributes.Action).Msg("ignoring unknown MR action")
	}

	return projectName, nil
}
