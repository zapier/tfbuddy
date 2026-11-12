package runstream

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
)

func TestAutoModeManualIsStickyAndSetOnce(t *testing.T) {
	stream := newTestAutoMergeStream(t)
	ref := AutoModeMRRef{VcsProvider: "gitlab", Project: "zapier/example", MergeRequest: 42}
	other := AutoModeMRRef{VcsProvider: "gitlab", Project: "zapier/example", MergeRequest: 43}

	manual, _, err := stream.IsAutoModeManual(ref)
	if err != nil || manual {
		t.Fatalf("a new MR is not in Manual Mode, got %v, %v", manual, err)
	}

	// Concurrent fallbacks: exactly one of them makes the record.
	var firsts atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			first, err := stream.MarkAutoModeManual(ref, "a commit by someone else")
			if err != nil {
				t.Error(err)
			}
			if first {
				firsts.Add(1)
			}
		}()
	}
	wg.Wait()
	if firsts.Load() != 1 {
		t.Fatalf("expected exactly one caller to set the mark, got %d", firsts.Load())
	}

	manual, reason, err := stream.IsAutoModeManual(ref)
	if err != nil || !manual || reason != "a commit by someone else" {
		t.Fatalf("expected the MR to be in Manual Mode with its reason, got %v, %q, %v", manual, reason, err)
	}
	if manual, _, _ := stream.IsAutoModeManual(other); manual {
		t.Fatal("another MR must not share the mark")
	}
	state, _, err := stream.getAutoModeMRState(ref)
	if err != nil || state.ManualReason != "a commit by someone else" || state.ManualSince.IsZero() {
		t.Fatalf("unexpected state %+v, %v", state, err)
	}

	if err := stream.DeleteAutoModeMRState(ref); err != nil {
		t.Fatal(err)
	}
	if manual, _, _ := stream.IsAutoModeManual(ref); manual {
		t.Fatal("a deleted state is not in Manual Mode")
	}
	if err := stream.DeleteAutoModeMRState(other); err != nil {
		t.Fatalf("deleting a state that does not exist is not an error, got %v", err)
	}
}

// The race this store exists for: when the last plans finish together, exactly
// one of them sees every outcome and finishes the class.
func TestClassPlansFinishOnceWhenPlansEndTogether(t *testing.T) {
	stream := newTestAutoMergeStream(t)
	ref := ClassPlanRef{VcsProvider: "gitlab", Project: "zapier/example", MergeRequest: 42, CommitSHA: "abc"}
	const n = 10
	workspaces := make([]string, n)
	for i := range workspaces {
		workspaces[i] = fmt.Sprintf("ws-%02d", i)
	}
	if err := stream.BeginClassPlans(ref, "tags", workspaces); err != nil {
		t.Fatal(err)
	}
	for i, ws := range workspaces {
		if err := stream.RegisterClassPlanRun(ref, ws, fmt.Sprintf("run-%d", i)); err != nil {
			t.Fatal(err)
		}
	}

	var finishers atomic.Int32
	var last atomic.Pointer[ClassPlanState]
	var wg sync.WaitGroup
	for i, ws := range workspaces {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state, finished, err := stream.RecordClassPlanResult(ref, ws, fmt.Sprintf("run-%d", i), true, "planned_and_finished")
			if err != nil {
				t.Error(err)
				return
			}
			if finished {
				finishers.Add(1)
				last.Store(state)
			}
		}()
	}
	wg.Wait()
	if finishers.Load() != 1 {
		t.Fatalf("expected exactly one update to finish the class, got %d", finishers.Load())
	}
	if state := last.Load(); !state.Passed() || len(state.FailedWorkspaces()) != 0 || state.Class != "tags" {
		t.Fatalf("expected every plan to have passed, got %+v", state)
	}
}

func TestClassPlansOutcomes(t *testing.T) {
	stream := newTestAutoMergeStream(t)
	ref := ClassPlanRef{VcsProvider: "gitlab", Project: "zapier/example", MergeRequest: 42, CommitSHA: "def"}

	if _, _, err := stream.RecordClassPlanResult(ref, "a", "run-a", true, "planned_and_finished"); !errors.Is(err, ErrClassPlansNotFound) {
		t.Fatalf("expected ErrClassPlansNotFound for a commit with no class plans, got %v", err)
	}
	if err := stream.BeginClassPlans(ref, "tags", []string{"a", "b", "c"}); err != nil {
		t.Fatal(err)
	}
	if err := stream.RegisterClassPlanRun(ref, "a", "run-a"); err != nil {
		t.Fatal(err)
	}
	if err := stream.RegisterClassPlanRun(ref, "x", "run-x"); err == nil {
		t.Fatal("a workspace outside the class plans cannot register a run")
	}

	// A run that does not count, such as a `tfc plan -w a` run, changes nothing.
	state, finished, err := stream.RecordClassPlanResult(ref, "a", "run-other", false, "errored")
	if err != nil || finished || state.Workspaces["a"].Outcome != ClassPlanPending {
		t.Fatalf("a run that does not count must not change the outcome, got %+v, %v, %v", state.Workspaces["a"], finished, err)
	}
	if _, finished, _ = stream.RecordClassPlanResult(ref, "a", "run-a", true, "planned_and_finished"); finished {
		t.Fatal("b and c have not finished")
	}
	// A workspace that never planned fails.
	if _, finished, _ = stream.RecordClassPlanResult(ref, "b", "", true, "did not plan: TFC is down"); finished {
		t.Fatal("c has not finished")
	}
	state, finished, err = stream.RecordClassPlanResult(ref, "c", "", false, "not planned")
	if err != nil || !finished {
		t.Fatalf("expected c to finish the class, got %v, %v", finished, err)
	}
	if state.Passed() || !slices.Equal(state.FailedWorkspaces(), []string{"b", "c"}) || state.Workspaces["b"].Detail != "did not plan: TFC is down" {
		t.Fatalf("unexpected state %+v", state)
	}
	// A failed workspace stays failed, and a finished class finishes once.
	state, finished, _ = stream.RecordClassPlanResult(ref, "b", "", true, "late")
	if finished || state.Workspaces["b"].Outcome != ClassPlanFailed {
		t.Fatalf("a failed workspace must stay failed, got %+v", state.Workspaces["b"])
	}

	// Planning the commit again starts over.
	if err := stream.BeginClassPlans(ref, "tags", []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := stream.RecordClassPlanResult(ref, "a", "run-a", true, "planned_and_finished"); err != nil {
		t.Fatal(err)
	}
	state, _, _ = stream.RecordClassPlanResult(ref, "a", "run-a", true, "planned_and_finished")
	if state.Workspaces["a"].Outcome != ClassPlanPending {
		t.Fatalf("a run registered before the commit was planned again must not count, got %+v", state.Workspaces["a"])
	}
}

func TestAutoModeSummaryNote(t *testing.T) {
	stream := newTestAutoMergeStream(t)
	ref := AutoModeMRRef{VcsProvider: "gitlab", Project: "zapier/example", MergeRequest: 42}
	if id, err := stream.GetAutoModeSummaryNote(ref); err != nil || id != 0 {
		t.Fatalf("expected no summary note, got %d, %v", id, err)
	}
	if err := stream.SetAutoModeSummaryNote(ref, 77); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.MarkAutoModeManual(ref, "reason"); err != nil {
		t.Fatal(err)
	}
	if id, err := stream.GetAutoModeSummaryNote(ref); err != nil || id != 77 {
		t.Fatalf("expected summary note 77, got %d, %v", id, err)
	}
	if manual, _, _ := stream.IsAutoModeManual(ref); !manual {
		t.Fatal("recording the summary note must keep the Manual Mode mark")
	}
}
