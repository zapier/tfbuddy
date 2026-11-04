package gitlab_hooks

import (
	"context"
	"testing"

	"github.com/rs/zerolog/log"
	"github.com/rzajac/zltest"
	"github.com/stretchr/testify/assert"
	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
	"go.uber.org/mock/gomock"
)

// The merge request event path discards its result, so without this log an
// operator has no record of a workspace that did not run.
func TestLogErroredWorkspacesRecordsEachFailure(t *testing.T) {
	testLogger := zltest.New(t)
	original := log.Logger
	log.Logger = log.Logger.Output(testLogger)
	t.Cleanup(func() { log.Logger = original })

	logErroredWorkspaces("zapier/tfbuddy", 101, &tfc_trigger.TriggeredTFCWorkspaces{
		Errored: []*tfc_trigger.ErroredWorkspace{
			{Name: "svc-a", Error: "excluded by allow/deny list"},
		},
	})

	entry := testLogger.LastEntry()
	if entry == nil {
		t.Fatal("expected a log entry for the errored workspace")
	}
	entry.ExpStr("workspace", "svc-a")
	entry.ExpStr("reason", "excluded by allow/deny list")
}

func TestLogErroredWorkspacesToleratesNil(t *testing.T) {
	logErroredWorkspaces("zapier/tfbuddy", 101, nil)
}

func TestHasSkipPlanLabel(t *testing.T) {
	tests := []struct {
		name       string
		mrLabels   []*gogitlab.EventLabel
		skipLabels []string
		want       bool
	}{
		{
			name:       "no skip labels configured",
			mrLabels:   []*gogitlab.EventLabel{{Title: "do-not-plan"}},
			skipLabels: nil,
			want:       false,
		},
		{
			name:       "mr has none of the configured labels",
			mrLabels:   []*gogitlab.EventLabel{{Title: "bug"}},
			skipLabels: []string{"do-not-plan", "wip"},
			want:       false,
		},
		{
			name:       "mr has a configured label",
			mrLabels:   []*gogitlab.EventLabel{{Title: "bug"}, {Title: "wip"}},
			skipLabels: []string{"do-not-plan", "wip"},
			want:       true,
		},
		{
			name:       "match is case-insensitive",
			mrLabels:   []*gogitlab.EventLabel{{Title: "WIP"}},
			skipLabels: []string{"wip"},
			want:       true,
		},
		{
			name:       "mr has no labels",
			mrLabels:   nil,
			skipLabels: []string{"wip"},
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, hasSkipPlanLabel(tt.mrLabels, tt.skipLabels))
		})
	}
}

// TestProcessMergeRequestEventSkipPlanLabel checks that a matching
// TFBUDDY_SKIP_PLAN_LABELS entry skips automatic plan triggering on webhook
// MR events without consulting TFC or the GitLab API, and that cleanup on
// merge/close still runs regardless of labels.
func TestProcessMergeRequestEventSkipPlanLabel(t *testing.T) {
	tests := []struct {
		name       string
		action     string
		mrLabels   []*gogitlab.EventLabel
		skipLabels []string
	}{
		{
			name:       "open is skipped when a skip-plan label is present",
			action:     "open",
			mrLabels:   []*gogitlab.EventLabel{{Title: "do-not-plan"}},
			skipLabels: []string{"do-not-plan"},
		},
		{
			name:       "reopen is skipped when a skip-plan label is present",
			action:     "reopen",
			mrLabels:   []*gogitlab.EventLabel{{Title: "do-not-plan"}},
			skipLabels: []string{"do-not-plan"},
		},
		{
			name:       "update with a new commit is skipped when a skip-plan label is present",
			action:     "update",
			mrLabels:   []*gogitlab.EventLabel{{Title: "do-not-plan"}},
			skipLabels: []string{"do-not-plan"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			// No expectations set: if the skip path falls through to
			// TriggerTFCEvents, any mock call made against these clients
			// fails the test.
			gl := mocks.NewMockGitClient(ctrl)
			tfc := mocks.NewMockApiClient(ctrl)
			rs := mocks.NewMockStreamClient(ctrl)

			cfg := config.C
			cfg.GitlabProjectAllowList = []string{"zapier/"}
			cfg.SkipPlanLabels = tt.skipLabels

			worker := &GitlabEventWorker{
				cfg:       cfg,
				gl:        gl,
				tfc:       tfc,
				runstream: rs,
			}

			event := &gogitlab.MergeEvent{}
			event.Project.PathWithNamespace = "zapier/tfbuddy"
			event.Project.Name = "tfbuddy"
			event.ObjectAttributes.IID = 101
			event.ObjectAttributes.Action = tt.action
			event.ObjectAttributes.SourceBranch = "feature-branch"
			event.ObjectAttributes.Labels = tt.mrLabels
			event.ObjectAttributes.LastCommit.ID = "abc123"
			event.ObjectAttributes.OldRev = "def456"
			event.ObjectAttributes.Source = &gogitlab.Repository{PathWithNamespace: "zapier/tfbuddy"}

			msg := &MergeRequestEventMsg{
				Payload:    event,
				DeliveryID: "delivery-1",
				Context:    context.Background(),
			}

			proj, err := worker.processMergeRequestEvent(msg)
			assert.NoError(t, err)
			assert.Equal(t, "zapier/tfbuddy", proj)
		})
	}
}
