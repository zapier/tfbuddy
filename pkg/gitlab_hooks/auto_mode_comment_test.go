package gitlab_hooks

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.uber.org/mock/gomock"
)

// recordFallbacks makes the MR's Manual Mode mark land, as the first one,
// with err (nil for a mark that lands), and returns the reasons it was
// asked for. It must run before expectMR.
func (f *autoModeFixture) recordFallbacks(err error) *[]string {
	var mu sync.Mutex
	reasons := &[]string{}
	f.stream.EXPECT().MarkAutoModeManual(runstream.AutoModeMRRef{VcsProvider: "gitlab", Project: gateProject, MergeRequest: gateMRIID}, gomock.Any()).
		DoAndReturn(func(_ runstream.AutoModeMRRef, reason string) (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			*reasons = append(*reasons, reason)
			return err == nil, err
		}).AnyTimes()
	return reasons
}

// A `tfc apply` comment on an Auto Mode MR whose gate passes runs in Auto Mode,
// like an approval's apply: the MR does not fall back.
func TestTakeOverAutoModeMR_GatePasses(t *testing.T) {
	f := newAutoModeFixture(t, true)
	fallbacks := f.recordFallbacks(nil)
	f.expectMR("tags-bot")
	f.expectGates(true, "success")

	if err := f.worker.takeOverAutoModeMR(context.Background(), gateProject, gateMRIID); err != nil {
		t.Fatal(err)
	}
	if len(*fallbacks) != 0 {
		t.Fatalf("an MR whose gate passes must stay in Auto Mode, got fallbacks %q", *fallbacks)
	}
}

// A person whose `tfc apply` the Auto Mode gate refuses takes the MR over: it
// falls back to Manual Mode, says why, and the apply that follows runs in
// Manual Mode, so a person can fix and apply what automation could not.
func TestTakeOverAutoModeMR_GateFailsTakesTheMROver(t *testing.T) {
	unfinished := passedClassPlans()
	unfinished.Finished = false
	unfinished.Workspaces["svc-a"].Outcome = runstream.ClassPlanPending
	for _, tt := range []struct {
		name, buildStatus string
		plans             *runstream.ClassPlanState
		wantReason        string
	}{
		{name: "the pipeline has not succeeded", buildStatus: "failed", wantReason: "still waiting on: aa:code-review (failed)"},
		{name: "the class plans have not finished", buildStatus: "success", plans: unfinished, wantReason: "have not all finished"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newAutoModeFixture(t, true)
			fallbacks := f.recordFallbacks(nil)
			f.expectMR("tags-bot")
			if tt.plans != nil {
				f.stream.EXPECT().GetClassPlans(gomock.Any()).Return(tt.plans, nil)
			}
			f.expectGates(true, tt.buildStatus)
			var comments []string
			f.client.EXPECT().CreateMergeRequestComment(gomock.Any(), gateMRIID, gateProject, gomock.Any()).
				DoAndReturn(func(_ context.Context, _ int, _ string, msg string) error {
					comments = append(comments, msg)
					return nil
				}).AnyTimes()

			if err := f.worker.takeOverAutoModeMR(context.Background(), gateProject, gateMRIID); err != nil {
				t.Fatal(err)
			}
			if len(*fallbacks) != 1 || !strings.HasPrefix((*fallbacks)[0], "a person commented `tfc apply` while the Auto Mode gate did not pass (") ||
				!strings.Contains((*fallbacks)[0], tt.wantReason) {
				t.Fatalf("expected one takeover naming %q, got %q", tt.wantReason, *fallbacks)
			}
			// The apply that follows plans without the class, so nothing is
			// planned again.
			if len(comments) != 1 || !strings.Contains(comments[0], "now runs in Manual Mode because a person commented `tfc apply`") ||
				strings.Contains(comments[0], "planning it again") {
				t.Errorf("expected one takeover comment, got %q", comments)
			}
		})
	}
}

// When the takeover cannot be recorded, the apply must not run: it would run
// in Auto Mode past a gate that refused it.
func TestTakeOverAutoModeMR_UnrecordedTakeoverFails(t *testing.T) {
	f := newAutoModeFixture(t, true)
	f.recordFallbacks(errors.New("nats: timeout"))
	f.expectMR("tags-bot")
	f.expectGates(true, "failed")

	if err := f.worker.takeOverAutoModeMR(context.Background(), gateProject, gateMRIID); err == nil {
		t.Fatal("a takeover that could not be recorded must stop the apply")
	}
}

// applyComment is a `tfc apply` comment on the fixture's MR.
func applyComment(ctrl *gomock.Controller) vcs.MRCommentEvent {
	project := mocks.NewMockProject(ctrl)
	project.EXPECT().GetPathWithNamespace().Return(gateProject).AnyTimes()
	lastCommit := mocks.NewMockCommit(ctrl)
	lastCommit.EXPECT().GetSHA().Return(gateCommitSHA).AnyTimes()
	attributes := mocks.NewMockMRAttributes(ctrl)
	attributes.EXPECT().GetNote().Return("tfc apply").AnyTimes()
	attributes.EXPECT().GetType().Return("SomeNote").AnyTimes()
	mr := mocks.NewMockMR(ctrl)
	mr.EXPECT().GetSourceBranch().Return("tags-bot/tags").AnyTimes()
	mr.EXPECT().GetInternalID().Return(gateMRIID).AnyTimes()
	event := mocks.NewMockMRCommentEvent(ctrl)
	event.EXPECT().GetProject().Return(project).AnyTimes()
	event.EXPECT().GetAttributes().Return(attributes).AnyTimes()
	event.EXPECT().GetLastCommit().Return(lastCommit).AnyTimes()
	event.EXPECT().GetMR().Return(mr).AnyTimes()
	return event
}

// The takeover is wired into the `tfc apply` comment: after a takeover the
// apply runs (as a Manual Mode apply, which the trigger decides), and when
// the takeover cannot be recorded, nothing runs.
func TestProcessNoteEvent_AutoModeApplyTakeover(t *testing.T) {
	for _, markFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "takeover recorded", true: "takeover not recorded"}[markFails], func(t *testing.T) {
			f := newAutoModeFixture(t, true)
			var markErr error
			if markFails {
				markErr = errors.New("nats: timeout")
			}
			fallbacks := f.recordFallbacks(markErr)
			f.expectMR("tags-bot")
			f.expectGates(true, "failed")
			// The comment's own checks read the MR and its approvals again.
			f.client.EXPECT().GetMergeRequest(gomock.Any(), gateMRIID, gateProject).Return(f.mr, nil).AnyTimes()
			approvals := mocks.NewMockMRApproved(f.ctrl)
			approvals.EXPECT().IsApproved().Return(true).AnyTimes()
			f.client.EXPECT().GetMergeRequestApprovals(gomock.Any(), gateMRIID, gateProject).Return(approvals, nil).AnyTimes()
			f.client.EXPECT().CreateMergeRequestComment(gomock.Any(), gateMRIID, gateProject, gomock.Any()).Return(nil).AnyTimes()
			if !markFails {
				f.trigger.EXPECT().TriggerTFCEvents(gomock.Any()).Return(&tfc_trigger.TriggeredTFCWorkspaces{Executed: []string{"svc-a"}}, nil)
			}
			// With markFails, no TriggerTFCEvents expectation: nothing may run.

			_, err := f.worker.processNoteEvent(context.Background(), applyComment(f.ctrl))
			if markFails != (err != nil) {
				t.Fatalf("processNoteEvent() error = %v, want an error only when the takeover is not recorded", err)
			}
			if len(*fallbacks) == 0 {
				t.Fatal("expected the failed gate to take the MR over")
			}
		})
	}
}
