package runstream

import (
	"sync"
	"sync/atomic"
	"testing"
)

func newTestAutoMergeStream(t *testing.T) *Stream {
	t.Helper()
	_, url := startTestNATS(t)
	nc := testConnect(t, url)
	t.Cleanup(nc.Close)
	js := testGetJetstreamContext(t, nc)
	return NewStream(js, testDedupWindow).(*Stream)
}

func autoMergeTestRef(workspace, runID, commitSHA string) AutoMergeRef {
	return AutoMergeRef{
		VcsProvider:  "gitlab",
		Project:      "zapier/example",
		MergeRequest: 42,
		CommitSHA:    commitSHA,
		Organization: "zapier",
		Workspace:    workspace,
		RunID:        runID,
	}
}

func TestAutoMergeWaitsForEveryWorkspaceAndDeduplicates(t *testing.T) {
	stream := newTestAutoMergeStream(t)
	const commitSHA = "wait-for-all"
	state := NewAutoMergeState("gitlab", "zapier/example", 42, commitSHA, true, []string{
		AutoMergeWorkspaceKey("zapier", "one"),
		AutoMergeWorkspaceKey("zapier", "two"),
	})
	if err := stream.EnsureAutoMergeState(state); err != nil {
		t.Fatal(err)
	}

	one := autoMergeTestRef("one", "run-one", commitSHA)
	two := autoMergeTestRef("two", "run-two", commitSHA)
	if err := stream.RegisterAutoMergeRun(one); err != nil {
		t.Fatal(err)
	}
	if err := stream.RegisterAutoMergeRun(two); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		claim, err := stream.RecordAutoMergeSuccess(one)
		if err != nil {
			t.Fatal(err)
		}
		if claim {
			t.Fatal("first workspace must not claim merge")
		}
	}
	claim, err := stream.RecordAutoMergeSuccess(two)
	if err != nil {
		t.Fatal(err)
	}
	if !claim {
		t.Fatal("last workspace should claim merge")
	}
	claim, err = stream.RecordAutoMergeSuccess(two)
	if err != nil {
		t.Fatal(err)
	}
	if claim {
		t.Fatal("duplicate success must not claim merge twice")
	}
}

func TestAutoMergeConcurrentSuccessClaimsOnce(t *testing.T) {
	stream := newTestAutoMergeStream(t)
	const workspaceCount = 12
	const commitSHA = "concurrent"
	keys := make([]string, 0, workspaceCount)
	refs := make([]AutoMergeRef, 0, workspaceCount)
	for i := 0; i < workspaceCount; i++ {
		name := string(rune('a' + i))
		keys = append(keys, AutoMergeWorkspaceKey("zapier", name))
		refs = append(refs, autoMergeTestRef(name, "run-"+name, commitSHA))
	}
	if err := stream.EnsureAutoMergeState(NewAutoMergeState("gitlab", "zapier/example", 42, commitSHA, true, keys)); err != nil {
		t.Fatal(err)
	}
	for _, ref := range refs {
		if err := stream.RegisterAutoMergeRun(ref); err != nil {
			t.Fatal(err)
		}
	}

	var claims atomic.Int32
	errs := make(chan error, workspaceCount)
	var wg sync.WaitGroup
	for _, ref := range refs {
		wg.Add(1)
		go func(ref AutoMergeRef) {
			defer wg.Done()
			claim, err := stream.RecordAutoMergeSuccess(ref)
			if err != nil {
				errs <- err
				return
			}
			if claim {
				claims.Add(1)
			}
		}(ref)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got := claims.Load(); got != 1 {
		t.Fatalf("expected exactly one merge claim, got %d", got)
	}
}

func TestAutoMergeRejectsStaleRunAndCommit(t *testing.T) {
	stream := newTestAutoMergeStream(t)
	key := AutoMergeWorkspaceKey("zapier", "one")
	const commitSHA = "stale-run"
	if err := stream.EnsureAutoMergeState(NewAutoMergeState("gitlab", "zapier/example", 42, commitSHA, true, []string{key})); err != nil {
		t.Fatal(err)
	}
	current := autoMergeTestRef("one", "run-current", commitSHA)
	if err := stream.RegisterAutoMergeRun(current); err != nil {
		t.Fatal(err)
	}

	staleRun := current
	staleRun.RunID = "run-stale"
	claim, err := stream.RecordAutoMergeSuccess(staleRun)
	if err != nil {
		t.Fatal(err)
	}
	if claim {
		t.Fatal("stale run claimed merge")
	}

	staleCommit := current
	staleCommit.CommitSHA = "old-sha"
	if _, err := stream.RecordAutoMergeSuccess(staleCommit); err != ErrAutoMergeStateNotFound {
		t.Fatalf("expected stale commit to miss state, got %v", err)
	}
}

func TestApplyGenerationInvalidatesWholeSelectedSetAtomically(t *testing.T) {
	stream := newTestAutoMergeStream(t)
	const commitSHA = "generation"
	keys := []string{
		AutoMergeWorkspaceKey("zapier", "one"),
		AutoMergeWorkspaceKey("zapier", "two"),
	}
	if err := stream.EnsureAutoMergeState(NewAutoMergeState("gitlab", "zapier/example", 42, commitSHA, true, keys)); err != nil {
		t.Fatal(err)
	}

	generationOne := autoMergeTestRef("", "", commitSHA)
	generationOne.Generation = "generation-one"
	generationOne.Sequence = 10
	if err := stream.BeginAutoMergeApply(generationOne, keys); err != nil {
		t.Fatal(err)
	}
	oneOld := autoMergeTestRef("one", "run-one-old", commitSHA)
	oneOld.Generation = generationOne.Generation
	oneOld.Sequence = generationOne.Sequence
	twoOld := autoMergeTestRef("two", "run-two-old", commitSHA)
	twoOld.Generation = generationOne.Generation
	twoOld.Sequence = generationOne.Sequence
	if err := stream.RegisterAutoMergeRun(oneOld); err != nil {
		t.Fatal(err)
	}
	if err := stream.RegisterAutoMergeRun(twoOld); err != nil {
		t.Fatal(err)
	}
	if claim, err := stream.RecordAutoMergeSuccess(oneOld); err != nil || claim {
		t.Fatalf("first generation claimed merge early, claim=%v err=%v", claim, err)
	}

	generationTwo := generationOne
	generationTwo.Generation = "generation-two"
	generationTwo.Sequence = 20
	if err := stream.BeginAutoMergeApply(generationTwo, keys); err != nil {
		t.Fatal(err)
	}
	// Delayed messages from the old generation must not overwrite the new set.
	if err := stream.RegisterAutoMergeRun(twoOld); err != nil {
		t.Fatal(err)
	}
	if claim, err := stream.RecordAutoMergeSuccess(oneOld); err != nil || claim {
		t.Fatalf("stale generation changed state, claim=%v err=%v", claim, err)
	}

	oneNew := autoMergeTestRef("one", "run-one-new", commitSHA)
	oneNew.Generation = generationTwo.Generation
	oneNew.Sequence = generationTwo.Sequence
	twoNew := autoMergeTestRef("two", "run-two-new", commitSHA)
	twoNew.Generation = generationTwo.Generation
	twoNew.Sequence = generationTwo.Sequence
	if err := stream.RegisterAutoMergeRun(oneNew); err != nil {
		t.Fatal(err)
	}
	if err := stream.RegisterAutoMergeRun(twoNew); err != nil {
		t.Fatal(err)
	}
	if claim, err := stream.RecordAutoMergeSuccess(oneNew); err != nil || claim {
		t.Fatalf("new generation claimed merge early, claim=%v err=%v", claim, err)
	}
	if claim, err := stream.RecordAutoMergeSuccess(twoNew); err != nil || !claim {
		t.Fatalf("complete new generation did not claim merge, claim=%v err=%v", claim, err)
	}
}

func TestApplyGenerationUpdatesOnlyWorkspacesWithoutNewerCommands(t *testing.T) {
	stream := newTestAutoMergeStream(t)
	const commitSHA = "mixed-selection-order"
	keys := []string{
		AutoMergeWorkspaceKey("zapier", "one"),
		AutoMergeWorkspaceKey("zapier", "two"),
	}
	if err := stream.EnsureAutoMergeState(NewAutoMergeState("gitlab", "zapier/example", 42, commitSHA, true, keys)); err != nil {
		t.Fatal(err)
	}

	oneOld := autoMergeTestRef("one", "run-one-100", commitSHA)
	oneOld.Generation, oneOld.Sequence = "generation-100", 100
	if err := stream.BeginAutoMergeApply(oneOld, keys[:1]); err != nil {
		t.Fatal(err)
	}
	if err := stream.RegisterAutoMergeRun(oneOld); err != nil {
		t.Fatal(err)
	}
	if claim, err := stream.RecordAutoMergeSuccess(oneOld); err != nil || claim {
		t.Fatalf("single workspace success claimed merge early, claim=%v err=%v", claim, err)
	}

	twoNew := autoMergeTestRef("two", "run-two-200", commitSHA)
	twoNew.Generation, twoNew.Sequence = "generation-200", 200
	if err := stream.BeginAutoMergeApply(twoNew, keys[1:]); err != nil {
		t.Fatal(err)
	}
	if err := stream.RegisterAutoMergeRun(twoNew); err != nil {
		t.Fatal(err)
	}

	fullDelayed := autoMergeTestRef("", "", commitSHA)
	fullDelayed.Generation, fullDelayed.Sequence = "generation-150", 150
	if err := stream.BeginAutoMergeApply(fullDelayed, keys); err != nil {
		t.Fatal(err)
	}
	oneDelayed := autoMergeTestRef("one", "run-one-150", commitSHA)
	oneDelayed.Generation, oneDelayed.Sequence = fullDelayed.Generation, fullDelayed.Sequence
	if err := stream.RegisterAutoMergeRun(oneDelayed); err != nil {
		t.Fatal(err)
	}

	if claim, err := stream.RecordAutoMergeSuccess(twoNew); err != nil || claim {
		t.Fatalf("newer scoped apply bypassed pending delayed workspace, claim=%v err=%v", claim, err)
	}
	if claim, err := stream.RecordAutoMergeSuccess(oneOld); err != nil || claim {
		t.Fatalf("stale workspace success changed state, claim=%v err=%v", claim, err)
	}
	if claim, err := stream.RecordAutoMergeSuccess(oneDelayed); err != nil || !claim {
		t.Fatalf("latest applicable workspace run did not claim merge, claim=%v err=%v", claim, err)
	}
}

func TestAutoMergeDisabledAndClaimRelease(t *testing.T) {
	stream := newTestAutoMergeStream(t)
	key := AutoMergeWorkspaceKey("zapier", "one")
	const disabledSHA = "disabled"
	disabled := NewAutoMergeState("gitlab", "zapier/example", 42, disabledSHA, false, []string{key})
	if err := stream.EnsureAutoMergeState(disabled); err != nil {
		t.Fatal(err)
	}
	ref := autoMergeTestRef("one", "run-one", disabledSHA)
	if err := stream.RegisterAutoMergeRun(ref); err != nil {
		t.Fatal(err)
	}
	claim, err := stream.RecordAutoMergeSuccess(ref)
	if err != nil {
		t.Fatal(err)
	}
	if claim {
		t.Fatal("ineligible state claimed merge")
	}

	enabledRef := ref
	enabledRef.CommitSHA = "def456"
	enabled := NewAutoMergeState("gitlab", "zapier/example", 42, enabledRef.CommitSHA, true, []string{key})
	if err := stream.EnsureAutoMergeState(enabled); err != nil {
		t.Fatal(err)
	}
	if err := stream.RegisterAutoMergeRun(enabledRef); err != nil {
		t.Fatal(err)
	}
	claim, err = stream.RecordAutoMergeSuccess(enabledRef)
	if err != nil || !claim {
		t.Fatalf("expected initial claim, got claim=%v err=%v", claim, err)
	}
	if err := stream.ReleaseAutoMergeClaim(enabledRef); err != nil {
		t.Fatal(err)
	}
	claim, err = stream.RecordAutoMergeSuccess(enabledRef)
	if err != nil || !claim {
		t.Fatalf("expected released claim to be retryable, got claim=%v err=%v", claim, err)
	}
}

func TestRegisterNewApplyInvalidatesEarlierSuccess(t *testing.T) {
	stream := newTestAutoMergeStream(t)
	keys := []string{
		AutoMergeWorkspaceKey("zapier", "one"),
		AutoMergeWorkspaceKey("zapier", "two"),
	}
	const commitSHA = "replacement"
	if err := stream.EnsureAutoMergeState(NewAutoMergeState("gitlab", "zapier/example", 42, commitSHA, true, keys)); err != nil {
		t.Fatal(err)
	}
	first := autoMergeTestRef("one", "run-one", commitSHA)
	other := autoMergeTestRef("two", "run-other", commitSHA)
	if err := stream.RegisterAutoMergeRun(first); err != nil {
		t.Fatal(err)
	}
	if err := stream.RegisterAutoMergeRun(other); err != nil {
		t.Fatal(err)
	}
	if claim, err := stream.RecordAutoMergeSuccess(first); err != nil || claim {
		t.Fatalf("first workspace claimed merge early, got claim=%v err=%v", claim, err)
	}

	second := autoMergeTestRef("one", "run-two", commitSHA)
	if err := stream.RegisterAutoMergeRun(second); err != nil {
		t.Fatal(err)
	}
	if claim, err := stream.RecordAutoMergeSuccess(other); err != nil || claim {
		t.Fatalf("other workspace claimed merge while replacement is pending, got claim=%v err=%v", claim, err)
	}
	if claim, err := stream.RecordAutoMergeSuccess(first); err != nil || claim {
		t.Fatalf("stale success changed replacement run, got claim=%v err=%v", claim, err)
	}
	if claim, err := stream.RecordAutoMergeSuccess(second); err != nil || !claim {
		t.Fatalf("replacement run should claim merge, got claim=%v err=%v", claim, err)
	}
}
