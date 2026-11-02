package tfc_trigger_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/go-tfe"
	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"go.uber.org/mock/gomock"
)

func everyChangeWorkspace() *tfc_trigger.ProjectConfig {
	return &tfc_trigger.ProjectConfig{Workspaces: []*tfc_trigger.TFCWorkspace{{
		Name:         "service-tfbuddy",
		Organization: "zapier-test",
		Mode:         "apply-before-merge",
		AutoMerge:    true,
		TriggerDirs:  []string{"**"},
	}}}
}

func newCleanupTrigger(ts *mocks.TestSuite, action tfc_trigger.TriggerAction, source tfc_trigger.TriggerSource) tfc_trigger.Trigger {
	tCfg, _ := tfc_trigger.NewTFCTriggerConfig(&tfc_trigger.TFCTriggerOptions{
		Action:                   action,
		Branch:                   ts.MetaData.SourceBranch,
		CommitSHA:                "abcd12233",
		ProjectNameWithNamespace: ts.MetaData.ProjectNameNS,
		MergeRequestIID:          ts.MetaData.MRIID,
		TriggerSource:            source,
	})
	return tfc_trigger.NewTFCTrigger(config.C, ts.MockGitClient, ts.MockApiClient, ts.MockStreamClient, tCfg)
}

func TestTriggerCleanupEvent_KeepsLockUnlessMerged(t *testing.T) {
	for _, state := range []string{"merged", "closed"} {
		t.Run(state, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: everyChangeWorkspace()}, t)
			ts.MockGitMR.EXPECT().GetState().Return(state).AnyTimes()
			ts.MockApiClient.EXPECT().GetWorkspaceByName(gomock.Any(), "zapier-test", "service-tfbuddy").
				Return(&tfe.Workspace{ID: "ws-1"}, nil)
			ts.MockApiClient.EXPECT().GetTagsByQuery(gomock.Any(), "ws-1", "tfbuddylock-101").
				Return([]string{"tfbuddylock-101"}, nil)
			if state == "merged" {
				ts.MockApiClient.EXPECT().RemoveTagsByName(gomock.Any(), "ws-1", []string{"tfbuddylock-101"}).Return(nil)
				ts.MockGitClient.EXPECT().CreateMergeRequestDiscussion(gomock.Any(), ts.MetaData.MRIID, ts.MetaData.ProjectNameNS,
					"Released locks for workspaces: service-tfbuddy").Return(ts.MockGitDisc, nil)
			} else {
				// No RemoveTagsByName: gomock fails the test if the lock is released.
				ts.MockGitClient.EXPECT().CreateMergeRequestDiscussion(gomock.Any(), ts.MetaData.MRIID, ts.MetaData.ProjectNameNS,
					fmt.Sprintf(tfc_trigger.MRCommentClosedKeepsLocks, "service-tfbuddy", "main")).Return(ts.MockGitDisc, nil)
			}
			ts.InitTestSuite()

			trigger := newCleanupTrigger(ts, tfc_trigger.PlanAction, tfc_trigger.MergeRequestEventTrigger)
			if err := trigger.TriggerCleanupEvent(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A lock left by an MR closed without merging is not stale: another MR's apply
// must not auto-clean it.
func TestTFCEvents_StaleLock_ClosedMR_Rejects(t *testing.T) {
	ctrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(ctrl, mocks.TestOverrides{ProjectConfig: everyChangeWorkspace()}, t)
	ts.MockApiClient.EXPECT().GetTagsByQuery(gomock.Any(), gomock.Any(), "tfbuddylock").Return([]string{"tfbuddylock-99"}, nil)
	lockingMR := mocks.NewMockDetailedMR(ctrl)
	lockingMR.EXPECT().GetState().Return("closed")
	lockingMR.EXPECT().GetWebURL().Return("https://gitlab.com/zapier/tfbuddy/-/merge_requests/99")
	ts.MockGitClient.EXPECT().GetMergeRequest(gomock.Any(), 99, ts.MetaData.ProjectNameNS).Return(lockingMR, nil)
	// No RemoveTagsByName or AddTags expectation.
	ts.InitTestSuite()

	triggered, err := newCleanupTrigger(ts, tfc_trigger.ApplyAction, tfc_trigger.CommentTrigger).TriggerTFCEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(triggered.Errored) != 1 || !strings.Contains(triggered.Errored[0].Error, "closed without merging") ||
		!strings.Contains(triggered.Errored[0].Error, "tfc unlock -w service-tfbuddy") {
		t.Fatalf("expected the closed MR's lock to block the apply, got %+v", triggered.Errored)
	}
}
