package gitlab

import (
	"context"
	"errors"
	"testing"

	"github.com/hashicorp/go-tfe"
	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.uber.org/mock/gomock"
)

func testPlanRunEvent(status tfe.RunStatus) *runstream.TFRunEvent {
	re := &runstream.TFRunEvent{
		RunID:        "run-123",
		Organization: "zapier",
		Workspace:    "service-tfbuddy",
		NewStatus:    string(status),
		Metadata: &runstream.TFRunMetadata{
			RunID:                                "run-123",
			Organization:                         "zapier",
			Workspace:                            "service-tfbuddy",
			Action:                               runstream.PlanAction,
			CommitSHA:                            "commit-123",
			MergeRequestProjectNameWithNamespace: "zapier/tfbuddy",
			MergeRequestIID:                      101,
			DiscussionID:                         "discussion-1",
			RootNoteID:                           1,
			VcsProvider:                          "gitlab",
		},
	}
	re.SetContext(context.Background())
	return re
}

func testRun(status tfe.RunStatus) *tfe.Run {
	return &tfe.Run{
		ID:        "run-123",
		Status:    status,
		Workspace: &tfe.Workspace{Name: "service-tfbuddy", Organization: &tfe.Organization{Name: "zapier"}},
	}
}

// An event for an earlier status that arrives after the run has finished
// must not touch the merge request: acting on it would mark the finished
// plan as running again.
func TestEventStreamCallbackSkipsStaleEvent(t *testing.T) {
	for _, stale := range []tfe.RunStatus{tfe.RunPlanQueued, tfe.RunPlanning, tfe.RunPolicyChecking} {
		t.Run(string(stale), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			tfc := mocks.NewMockApiClient(ctrl)
			tfc.EXPECT().GetRun(gomock.Any(), "run-123").Return(testRun(tfe.RunPlannedAndFinished), nil)
			// No expectations on the Git client: any status write or comment fails the test.
			r := &RunStatusUpdater{cfg: config.C, tfc: tfc, client: mocks.NewMockGitClient(ctrl)}

			if !r.eventStreamCallback(testPlanRunEvent(stale)) {
				t.Fatal("a stale event must be acked, not retried")
			}
		})
	}
}

// While the run is in progress, every event is handled as before.
func TestEventStreamCallbackHandlesEventsForARunInProgress(t *testing.T) {
	ctrl := gomock.NewController(t)
	tfc := mocks.NewMockApiClient(ctrl)
	// The run has moved on to policy checks, but it has not finished, both
	// when the event arrives and when the status is written.
	tfc.EXPECT().GetRun(gomock.Any(), "run-123").Return(testRun(tfe.RunPolicyChecking), nil).Times(2)
	client := mocks.NewMockGitClient(ctrl)
	client.EXPECT().UpdateMergeRequestDiscussionNote(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	client.EXPECT().AddMergeRequestDiscussionReply(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	var got vcs.WorkspaceStatus
	client.EXPECT().SetWorkspaceStatus(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s vcs.WorkspaceStatus) error {
			got = s
			return nil
		}).Times(1)
	r := &RunStatusUpdater{cfg: config.C, tfc: tfc, client: client}

	if !r.eventStreamCallback(testPlanRunEvent(tfe.RunPlanning)) {
		t.Fatal("event was not acked")
	}
	if got.State != vcs.CommitStateRunning || got.Action != runstream.PlanAction {
		t.Errorf("status = %s/%s, want plan/running", got.Action, got.State)
	}
}

// The run finishes while the event for an earlier status is being handled,
// for example while its comment is posted. Another replica sets the final
// status meanwhile, so this event's in-progress status must not overwrite it.
func TestEventStreamCallbackSkipsTheStatusWhenTheRunFinishesMeanwhile(t *testing.T) {
	ctrl := gomock.NewController(t)
	tfc := mocks.NewMockApiClient(ctrl)
	gomock.InOrder(
		tfc.EXPECT().GetRun(gomock.Any(), "run-123").Return(testRun(tfe.RunPlanning), nil),
		tfc.EXPECT().GetRun(gomock.Any(), "run-123").Return(testRun(tfe.RunPlannedAndFinished), nil),
	)
	client := mocks.NewMockGitClient(ctrl)
	client.EXPECT().UpdateMergeRequestDiscussionNote(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	client.EXPECT().AddMergeRequestDiscussionReply(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	// No SetWorkspaceStatus expectation: writing "running" fails the test.
	r := &RunStatusUpdater{cfg: config.C, tfc: tfc, client: client}

	if !r.eventStreamCallback(testPlanRunEvent(tfe.RunPlanning)) {
		t.Fatal("event was not acked")
	}
}

// The event that reports the final status is still handled.
func TestEventStreamCallbackHandlesTheFinalEvent(t *testing.T) {
	ctrl := gomock.NewController(t)
	tfc := mocks.NewMockApiClient(ctrl)
	tfc.EXPECT().GetRun(gomock.Any(), "run-123").Return(testRun(tfe.RunErrored), nil)
	client := mocks.NewMockGitClient(ctrl)
	client.EXPECT().GetOldRunUrls(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	client.EXPECT().UpdateMergeRequestDiscussionNote(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	client.EXPECT().AddMergeRequestDiscussionReply(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	var got vcs.WorkspaceStatus
	client.EXPECT().SetWorkspaceStatus(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s vcs.WorkspaceStatus) error {
			got = s
			return nil
		}).Times(1)
	rs := mocks.NewMockStreamClient(ctrl)
	rs.EXPECT().RecordRunFinished("gitlab", gomock.Any())
	r := &RunStatusUpdater{cfg: config.C, tfc: tfc, client: client, rs: rs}

	if !r.eventStreamCallback(testPlanRunEvent(tfe.RunErrored)) {
		t.Fatal("event was not acked")
	}
	if got.State != vcs.CommitStateFailed || got.Action != runstream.PlanAction {
		t.Errorf("status = %s/%s, want plan/failed", got.Action, got.State)
	}
}

// A finished run is recorded once its final status has been handled and the
// event is about to be acked.
func TestEventStreamCallbackRecordsAFinishedRun(t *testing.T) {
	ctrl := gomock.NewController(t)
	tfc := mocks.NewMockApiClient(ctrl)
	tfc.EXPECT().GetRun(gomock.Any(), "run-123").Return(testRun(tfe.RunErrored), nil)
	client := mocks.NewMockGitClient(ctrl)
	client.EXPECT().GetOldRunUrls(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	client.EXPECT().UpdateMergeRequestDiscussionNote(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	client.EXPECT().AddMergeRequestDiscussionReply(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	client.EXPECT().SetWorkspaceStatus(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	rs := mocks.NewMockStreamClient(ctrl)
	re := testPlanRunEvent(tfe.RunErrored)
	rs.EXPECT().RecordRunFinished("gitlab", re).Times(1)
	r := &RunStatusUpdater{cfg: config.C, tfc: tfc, client: client, rs: rs}

	if !r.eventStreamCallback(re) {
		t.Fatal("event was not acked")
	}
}

// Events for a run in progress, stale events and events that will be retried
// do not record a finished run. The stream mock has no expectations, so any
// call fails the test.
func TestEventStreamCallbackDoesNotRecordUnfinishedRuns(t *testing.T) {
	t.Run("in progress", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		tfc := mocks.NewMockApiClient(ctrl)
		tfc.EXPECT().GetRun(gomock.Any(), "run-123").Return(testRun(tfe.RunPlanning), nil).Times(2)
		client := mocks.NewMockGitClient(ctrl)
		client.EXPECT().UpdateMergeRequestDiscussionNote(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
		client.EXPECT().AddMergeRequestDiscussionReply(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
		client.EXPECT().SetWorkspaceStatus(gomock.Any(), gomock.Any()).Return(nil).Times(1)
		r := &RunStatusUpdater{cfg: config.C, tfc: tfc, client: client, rs: mocks.NewMockStreamClient(ctrl)}

		r.eventStreamCallback(testPlanRunEvent(tfe.RunPlanning))
	})
	t.Run("stale", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		tfc := mocks.NewMockApiClient(ctrl)
		tfc.EXPECT().GetRun(gomock.Any(), "run-123").Return(testRun(tfe.RunPlannedAndFinished), nil)
		r := &RunStatusUpdater{cfg: config.C, tfc: tfc, client: mocks.NewMockGitClient(ctrl), rs: mocks.NewMockStreamClient(ctrl)}

		r.eventStreamCallback(testPlanRunEvent(tfe.RunPlanning))
	})
	t.Run("retried", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		tfc := mocks.NewMockApiClient(ctrl)
		tfc.EXPECT().GetRun(gomock.Any(), "run-123").Return(nil, errors.New("TFC is down"))
		r := &RunStatusUpdater{cfg: config.C, tfc: tfc, client: mocks.NewMockGitClient(ctrl), rs: mocks.NewMockStreamClient(ctrl)}

		if r.eventStreamCallback(testPlanRunEvent(tfe.RunErrored)) {
			t.Fatal("an event whose run could not be read must be retried")
		}
	})
}
