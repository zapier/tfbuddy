package gitlab_hooks

import (
	"context"
	"testing"

	"github.com/rs/zerolog/log"
	"github.com/rzajac/zltest"
	"github.com/stretchr/testify/assert"
	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/vcs"
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
		labelSets  [][]*gogitlab.EventLabel
		skipLabels []string
		want       bool
	}{
		{
			name:       "no skip labels configured",
			labelSets:  [][]*gogitlab.EventLabel{{{Title: "do-not-plan"}}},
			skipLabels: nil,
			want:       false,
		},
		{
			name:       "mr has none of the configured labels",
			labelSets:  [][]*gogitlab.EventLabel{{{Title: "bug"}}},
			skipLabels: []string{"do-not-plan", "wip"},
			want:       false,
		},
		{
			name:       "mr has a configured label",
			labelSets:  [][]*gogitlab.EventLabel{{{Title: "bug"}, {Title: "wip"}}},
			skipLabels: []string{"do-not-plan", "wip"},
			want:       true,
		},
		{
			name:       "match is case-insensitive",
			labelSets:  [][]*gogitlab.EventLabel{{{Title: "WIP"}}},
			skipLabels: []string{"wip"},
			want:       true,
		},
		{
			name:       "mr has no labels",
			labelSets:  [][]*gogitlab.EventLabel{nil},
			skipLabels: []string{"wip"},
			want:       false,
		},
		{
			name: "match found only in the second label set (object_attributes.labels empty, top-level labels populated)",
			labelSets: [][]*gogitlab.EventLabel{
				nil,
				{{Title: "do-not-plan"}},
			},
			skipLabels: []string{"do-not-plan"},
			want:       true,
		},
		{
			name: "match found only in the first label set (top-level labels empty, object_attributes.labels populated)",
			labelSets: [][]*gogitlab.EventLabel{
				{{Title: "do-not-plan"}},
				nil,
			},
			skipLabels: []string{"do-not-plan"},
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, hasSkipPlanLabel(tt.skipLabels, tt.labelSets...))
		})
	}
}

// baseMergeEvent returns a minimal, allow-listed MergeEvent for the given
// webhook action, with no labels set.
func baseMergeEvent(action string) *gogitlab.MergeEvent {
	event := &gogitlab.MergeEvent{}
	event.Project.PathWithNamespace = "zapier/tfbuddy"
	event.Project.Name = "tfbuddy"
	event.ObjectAttributes.IID = 101
	event.ObjectAttributes.Action = action
	event.ObjectAttributes.SourceBranch = "feature-branch"
	event.ObjectAttributes.LastCommit.ID = "abc123"
	event.ObjectAttributes.OldRev = "def456"
	event.ObjectAttributes.Source = &gogitlab.Repository{PathWithNamespace: "zapier/tfbuddy"}
	return event
}

// TestProcessMergeRequestEventSkipPlanLabel checks that a matching
// TFBUDDY_SKIP_AUTO_PLAN_LABELS entry skips automatic plan triggering on webhook
// MR events without consulting TFC or the GitLab API.
func TestProcessMergeRequestEventSkipPlanLabel(t *testing.T) {
	tests := []struct {
		name             string
		action           string
		objectAttrLabels []*gogitlab.EventLabel
		topLevelLabels   []*gogitlab.EventLabel
		skipLabels       []string
	}{
		{
			name:             "open is skipped when object_attributes.labels has a skip-plan label",
			action:           "open",
			objectAttrLabels: []*gogitlab.EventLabel{{Title: "do-not-plan"}},
			skipLabels:       []string{"do-not-plan"},
		},
		{
			name:           "open is skipped when only the top-level labels field has a skip-plan label",
			action:         "open",
			topLevelLabels: []*gogitlab.EventLabel{{Title: "do-not-plan"}},
			skipLabels:     []string{"do-not-plan"},
		},
		{
			name:             "reopen is skipped when a skip-plan label is present",
			action:           "reopen",
			objectAttrLabels: []*gogitlab.EventLabel{{Title: "do-not-plan"}},
			skipLabels:       []string{"do-not-plan"},
		},
		{
			name:             "update with a new commit is skipped when a skip-plan label is present",
			action:           "update",
			objectAttrLabels: []*gogitlab.EventLabel{{Title: "do-not-plan"}},
			skipLabels:       []string{"do-not-plan"},
		},
		{
			name:           "update with a new commit is skipped when only the top-level labels field has a skip-plan label",
			action:         "update",
			topLevelLabels: []*gogitlab.EventLabel{{Title: "do-not-plan"}},
			skipLabels:     []string{"do-not-plan"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			// No expectations set on the trigger: if the skip check fails to
			// short-circuit, any call made against it fails the test.
			trigger := mocks.NewMockTrigger(ctrl)

			gl := mocks.NewMockGitClient(ctrl)
			gl.EXPECT().SetWorkspaceStatus(gomock.Any(), vcs.WorkspaceStatus{
				Project:         "zapier/tfbuddy",
				CommitSHA:       "abc123",
				MergeRequestIID: 101,
				Workspace:       skipPlanWorkspaceName,
				Action:          "plan",
				State:           vcs.CommitStatePending,
				Description:     "tfc plan skipped: merge request has a skip-auto-plan label",
			}).Return(nil)

			cfg := config.C
			cfg.GitlabProjectAllowList = []string{"zapier/"}
			cfg.SkipAutoPlanLabels = tt.skipLabels

			worker := &GitlabEventWorker{
				cfg:       cfg,
				gl:        gl,
				tfc:       mocks.NewMockApiClient(ctrl),
				runstream: mocks.NewMockStreamClient(ctrl),
				triggerCreation: func(appCfg config.Config, gl vcs.GitClient, tfc tfc_api.ApiClient, rs runstream.StreamClient, cfg *tfc_trigger.TFCTriggerOptions) tfc_trigger.Trigger {
					return trigger
				},
			}

			event := baseMergeEvent(tt.action)
			event.ObjectAttributes.Labels = tt.objectAttrLabels
			event.Labels = tt.topLevelLabels

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

// TestSkipAutoPlanPostsPendingStatusEvenOnPostError checks that a failure
// posting the skip-plan commit status is only logged: it must not turn into
// an error returned from processMergeRequestEvent, since a commit status is a
// best-effort notification, not a precondition for skipping the plan.
func TestSkipAutoPlanPostsPendingStatusEvenOnPostError(t *testing.T) {
	ctrl := gomock.NewController(t)

	trigger := mocks.NewMockTrigger(ctrl)

	gl := mocks.NewMockGitClient(ctrl)
	gl.EXPECT().SetWorkspaceStatus(gomock.Any(), gomock.Any()).Return(assert.AnError)

	cfg := config.C
	cfg.GitlabProjectAllowList = []string{"zapier/"}
	cfg.SkipAutoPlanLabels = []string{"do-not-plan"}

	worker := &GitlabEventWorker{
		cfg:       cfg,
		gl:        gl,
		tfc:       mocks.NewMockApiClient(ctrl),
		runstream: mocks.NewMockStreamClient(ctrl),
		triggerCreation: func(appCfg config.Config, gl vcs.GitClient, tfc tfc_api.ApiClient, rs runstream.StreamClient, cfg *tfc_trigger.TFCTriggerOptions) tfc_trigger.Trigger {
			return trigger
		},
	}

	event := baseMergeEvent("open")
	event.ObjectAttributes.Labels = []*gogitlab.EventLabel{{Title: "do-not-plan"}}

	msg := &MergeRequestEventMsg{
		Payload:    event,
		DeliveryID: "delivery-1",
		Context:    context.Background(),
	}

	proj, err := worker.processMergeRequestEvent(msg)
	assert.NoError(t, err)
	assert.Equal(t, "zapier/tfbuddy", proj)
}

// TestProcessMergeRequestEventTriggersPlanWhenNotSkipped guards against a
// regression where the skip-plan-label check swallows every automatic plan:
// it checks that open/reopen/push-update still reach TriggerTFCEvents when
// TFBUDDY_SKIP_AUTO_PLAN_LABELS is unset, or set but not matching the MR's labels.
func TestProcessMergeRequestEventTriggersPlanWhenNotSkipped(t *testing.T) {
	tests := []struct {
		name             string
		action           string
		objectAttrLabels []*gogitlab.EventLabel
		topLevelLabels   []*gogitlab.EventLabel
		skipLabels       []string
	}{
		{
			name:   "open triggers a plan when no skip labels are configured",
			action: "open",
		},
		{
			name:             "open triggers a plan when the mr's labels do not match",
			action:           "open",
			objectAttrLabels: []*gogitlab.EventLabel{{Title: "bug"}},
			skipLabels:       []string{"do-not-plan"},
		},
		{
			name:   "reopen triggers a plan when no skip labels are configured",
			action: "reopen",
		},
		{
			name:   "update with a new commit triggers a plan when no skip labels are configured",
			action: "update",
		},
		{
			name:           "update with a new commit triggers a plan when the mr's labels do not match",
			action:         "update",
			topLevelLabels: []*gogitlab.EventLabel{{Title: "bug"}},
			skipLabels:     []string{"do-not-plan"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			trigger := mocks.NewMockTrigger(ctrl)
			trigger.EXPECT().TriggerTFCEvents(gomock.Any()).Return(&tfc_trigger.TriggeredTFCWorkspaces{
				Executed: []string{"service-tf-buddy"},
			}, nil)

			cfg := config.C
			cfg.GitlabProjectAllowList = []string{"zapier/"}
			cfg.SkipAutoPlanLabels = tt.skipLabels

			worker := &GitlabEventWorker{
				cfg:       cfg,
				gl:        mocks.NewMockGitClient(ctrl),
				tfc:       mocks.NewMockApiClient(ctrl),
				runstream: mocks.NewMockStreamClient(ctrl),
				triggerCreation: func(appCfg config.Config, gl vcs.GitClient, tfc tfc_api.ApiClient, rs runstream.StreamClient, cfg *tfc_trigger.TFCTriggerOptions) tfc_trigger.Trigger {
					return trigger
				},
			}

			event := baseMergeEvent(tt.action)
			event.ObjectAttributes.Labels = tt.objectAttrLabels
			event.Labels = tt.topLevelLabels

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

// TestProcessMergeRequestEventMergeCloseAlwaysCleansUp guards against a
// regression where the skip-plan-label check also suppresses cleanup: merge
// and close events must still reach TriggerCleanupEvent (releasing locks,
// etc.) even when the merge request carries a configured skip-plan label,
// since the label only gates the automatic plan.
func TestProcessMergeRequestEventMergeCloseAlwaysCleansUp(t *testing.T) {
	for _, action := range []string{"merge", "close"} {
		t.Run(action, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			trigger := mocks.NewMockTrigger(ctrl)
			trigger.EXPECT().TriggerCleanupEvent(gomock.Any()).Return(nil)

			cfg := config.C
			cfg.GitlabProjectAllowList = []string{"zapier/"}
			cfg.SkipAutoPlanLabels = []string{"do-not-plan"}

			worker := &GitlabEventWorker{
				cfg:       cfg,
				gl:        mocks.NewMockGitClient(ctrl),
				tfc:       mocks.NewMockApiClient(ctrl),
				runstream: mocks.NewMockStreamClient(ctrl),
				triggerCreation: func(appCfg config.Config, gl vcs.GitClient, tfc tfc_api.ApiClient, rs runstream.StreamClient, cfg *tfc_trigger.TFCTriggerOptions) tfc_trigger.Trigger {
					return trigger
				},
			}

			event := baseMergeEvent(action)
			event.ObjectAttributes.Labels = []*gogitlab.EventLabel{{Title: "do-not-plan"}}

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
