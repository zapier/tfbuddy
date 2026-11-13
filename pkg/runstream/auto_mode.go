package runstream

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	// AutoModeMRKvBucket stores what TFBuddy remembers about an Auto Mode
	// author's merge request across its commits.
	AutoModeMRKvBucket = "AUTO_MODE_MERGE_REQUESTS"
	// autoModeCASRetries limits retries when concurrent events update one
	// merge request's state.
	autoModeCASRetries = 20
)

// AutoModeMRRef identifies a merge request.
type AutoModeMRRef struct {
	VcsProvider  string
	Project      string
	MergeRequest int
}

// AutoModeMRState is what TFBuddy remembers about a merge request whose author
// runs in Auto Mode. It lives in a bucket without a TTL: a Manual Mode mark
// that expired would put a long-open MR back in Auto Mode.
type AutoModeMRState struct {
	// Manual records that the MR runs in Manual Mode until it merges. Nothing
	// clears it while the MR is open, so no later push, force-push or comment
	// can bring Auto Mode back.
	Manual bool `json:"manual"`
	// ManualReason is why the MR fell back to Manual Mode.
	ManualReason string `json:"manual_reason,omitempty"`
	// ManualSince is when it fell back.
	ManualSince time.Time `json:"manual_since,omitempty"`
	// SummaryNoteID is the MR comment that summarizes the class plans of its
	// latest Auto Mode commit.
	SummaryNoteID int64 `json:"summary_note_id,omitempty"`
}

// MarkAutoModeManual records that ref runs in Manual Mode until it merges. It
// reports whether this call made the record, so that exactly one of several
// concurrent callers acts on the fallback (re-plans, comments).
func (s *Stream) MarkAutoModeManual(ref AutoModeMRRef, reason string) (bool, error) {
	return s.updateAutoModeMRState(ref, func(state *AutoModeMRState) bool {
		if state.Manual {
			return false
		}
		state.Manual = true
		state.ManualReason = reason
		state.ManualSince = time.Now().UTC()
		return true
	})
}

// IsAutoModeManual reports whether ref has fallen back to Manual Mode, and
// why.
func (s *Stream) IsAutoModeManual(ref AutoModeMRRef) (bool, string, error) {
	state, _, err := s.getAutoModeMRState(ref)
	if err != nil || state == nil || !state.Manual {
		return false, "", err
	}
	return true, state.ManualReason, nil
}

// DeleteAutoModeMRState forgets ref. TFBuddy calls it once the MR has merged,
// since a merged MR cannot be reopened.
func (s *Stream) DeleteAutoModeMRState(ref AutoModeMRRef) error {
	if s.autoModeKV == nil {
		return errors.New("auto mode state store is not configured")
	}
	err := s.autoModeKV.Delete(autoModeMRKey(ref))
	if errors.Is(err, nats.ErrKeyNotFound) {
		return nil
	}
	return err
}

func (s *Stream) getAutoModeMRState(ref AutoModeMRRef) (*AutoModeMRState, nats.KeyValueEntry, error) {
	if s.autoModeKV == nil {
		return nil, nil, errors.New("auto mode state store is not configured")
	}
	entry, err := s.autoModeKV.Get(autoModeMRKey(ref))
	if errors.Is(err, nats.ErrKeyNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	state := &AutoModeMRState{}
	if err := json.Unmarshal(entry.Value(), state); err != nil {
		return nil, nil, err
	}
	return state, entry, nil
}

// updateAutoModeMRState applies mutate to ref's state with compare-and-swap,
// creating the state when there is none. It reports mutate's result for the
// write that landed.
func (s *Stream) updateAutoModeMRState(ref AutoModeMRRef, mutate func(*AutoModeMRState) bool) (bool, error) {
	key := autoModeMRKey(ref)
	for range autoModeCASRetries {
		state, entry, err := s.getAutoModeMRState(ref)
		if err != nil {
			return false, err
		}
		exists := state != nil
		if !exists {
			state = &AutoModeMRState{}
		}
		if !mutate(state) {
			return false, nil
		}
		data, err := json.Marshal(state)
		if err != nil {
			return false, err
		}
		if exists {
			_, err = s.autoModeKV.Update(key, data, entry.Revision())
		} else {
			_, err = s.autoModeKV.Create(key, data)
		}
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, nats.ErrKeyExists) {
			return false, err
		}
	}
	return false, errors.New("auto mode state update exceeded retry limit")
}

func autoModeMRKey(ref AutoModeMRRef) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("mr\x00%s\x00%s\x00%d", ref.VcsProvider, ref.Project, ref.MergeRequest)))
	return hex.EncodeToString(hash[:])
}

func configureAutoModeMRKVStore(js nats.JetStreamContext) (nats.KeyValue, error) {
	cfg := &nats.KeyValueConfig{
		Bucket:      AutoModeMRKvBucket,
		Description: "Per merge request Auto Mode state, such as a fallback to Manual Mode",
		// No TTL: the state must outlive any open MR. TFBuddy deletes it once
		// the MR merges.
		Storage:  nats.FileStorage,
		Replicas: 1,
	}

	for store := range js.KeyValueStores() {
		if store.Bucket() == cfg.Bucket {
			return js.KeyValue(cfg.Bucket)
		}
	}
	return js.CreateKeyValue(cfg)
}

const (
	// AutoModeClassPlansKvBucket stores the outcome of every class plan of
	// one Auto Mode commit, which decides its TFC/class status.
	AutoModeClassPlansKvBucket = "AUTO_MODE_CLASS_PLANS"
	// autoModeClassPlansTTL bounds storage for commits nobody merges; a
	// commit's class is decided within minutes of its plans.
	autoModeClassPlansTTL = 30 * 24 * time.Hour
)

// ErrClassPlansNotFound is returned for a commit TFBuddy never began class
// plans for.
var ErrClassPlansNotFound = errors.New("class plan state not found")

// ErrClassPlansSuperseded is returned for a run of class plans that another
// trigger has since begun again for the same commit. The run no longer counts.
var ErrClassPlansSuperseded = errors.New("the class plans were begun again by another trigger")

// ClassPlanOutcome is how one workspace's class plan ended.
type ClassPlanOutcome string

const (
	ClassPlanPending ClassPlanOutcome = ""
	ClassPlanPassed  ClassPlanOutcome = "passed"
	ClassPlanFailed  ClassPlanOutcome = "failed"
)

// ClassPlanRef identifies the class plans of one MR commit.
type ClassPlanRef struct {
	VcsProvider  string
	Project      string
	MergeRequest int
	CommitSHA    string
}

// MR returns the merge request the commit belongs to.
func (r ClassPlanRef) MR() AutoModeMRRef {
	return AutoModeMRRef{VcsProvider: r.VcsProvider, Project: r.Project, MergeRequest: r.MergeRequest}
}

// ClassPlanWorkspace is one workspace's class plan.
type ClassPlanWorkspace struct {
	// RunID is the plan run that counts for the workspace; a run with any
	// other ID, such as a `tfc plan -w` run or a late event from an earlier
	// plan of the commit, does not change the outcome.
	RunID   string           `json:"run_id,omitempty"`
	Outcome ClassPlanOutcome `json:"outcome,omitempty"`
	// Detail is the run's final status, or why the workspace did not plan.
	Detail string `json:"detail,omitempty"`
}

// ClassPlanState is the class plans of one MR commit. Every update goes
// through compare-and-swap, so of the plans that finish together exactly one
// sees every outcome, and the TFC/class result never depends on reading
// commit statuses back from GitLab.
type ClassPlanState struct {
	VcsProvider  string                         `json:"vcs_provider"`
	Project      string                         `json:"project"`
	MergeRequest int                            `json:"merge_request"`
	CommitSHA    string                         `json:"commit_sha"`
	Class        string                         `json:"class"`
	Workspaces   map[string]*ClassPlanWorkspace `json:"workspaces"`
	// Finished is set by the one update that gave the last workspace its
	// outcome.
	Finished bool `json:"finished"`
	// Reported is set once TFC/class has been written for the finished
	// plans. A finished state that is not reported is reported again by the
	// next event for its commit, such as the redelivery of the event whose
	// report failed.
	Reported bool `json:"reported,omitempty"`
	// Trigger is the webhook delivery or comment that began these plans. A
	// redelivery of the same trigger keeps them, since its workspace dispatch
	// claims start no new run.
	Trigger string `json:"trigger,omitempty"`
	// SupersededTriggers are the latest triggers whose plans of the commit
	// these replaced. A late redelivery of one of them keeps these plans.
	SupersededTriggers []string `json:"superseded_triggers,omitempty"`
}

// maxSupersededTriggers bounds ClassPlanState.SupersededTriggers. A commit is
// rarely planned again more than a few times.
const maxSupersededTriggers = 20

// Ref returns the reference of the commit s belongs to.
func (s *ClassPlanState) Ref() ClassPlanRef {
	return ClassPlanRef{VcsProvider: s.VcsProvider, Project: s.Project, MergeRequest: s.MergeRequest, CommitSHA: s.CommitSHA}
}

// FailedWorkspaces lists, sorted, the workspaces whose class plan failed.
func (s *ClassPlanState) FailedWorkspaces() []string {
	var failed []string
	for name, ws := range s.Workspaces {
		if ws.Outcome == ClassPlanFailed {
			failed = append(failed, name)
		}
	}
	slices.Sort(failed)
	return failed
}

// Passed reports whether every workspace's class plan passed. A state without
// workspaces never passes.
func (s *ClassPlanState) Passed() bool {
	if len(s.Workspaces) == 0 {
		return false
	}
	for _, ws := range s.Workspaces {
		if ws.Outcome != ClassPlanPassed {
			return false
		}
	}
	return true
}

func (s *ClassPlanState) allFinished() bool {
	for _, ws := range s.Workspaces {
		if ws.Outcome == ClassPlanPending {
			return false
		}
	}
	return true
}

// BeginClassPlans starts the class plans of ref's commit over: every one of
// workspaces is pending, and runs registered for an earlier plan of the
// commit no longer count. trigger identifies the webhook delivery or comment
// that plans the commit. When the state already belongs to that trigger, this
// is a redelivery: its workspace dispatch claims start no new run, so the
// state, with the runs and outcomes it already has, is kept, and started is
// false. The same holds for a late redelivery of a trigger whose plans another
// trigger has replaced since: the newer plans stand. An empty trigger always
// starts over, because without one no claim is taken and every workspace is
// dispatched again.
func (s *Stream) BeginClassPlans(ref ClassPlanRef, class string, workspaces []string, trigger string) (started bool, err error) {
	if s.classPlansKV == nil {
		return false, errors.New("class plan store is not configured")
	}
	if len(workspaces) == 0 {
		return false, errors.New("class plans need at least one workspace")
	}
	key := classPlansKey(ref)
	for range autoModeCASRetries {
		state := &ClassPlanState{
			VcsProvider: ref.VcsProvider, Project: ref.Project, MergeRequest: ref.MergeRequest, CommitSHA: ref.CommitSHA,
			Class:      class,
			Workspaces: make(map[string]*ClassPlanWorkspace, len(workspaces)),
			Trigger:    trigger,
		}
		for _, ws := range workspaces {
			state.Workspaces[ws] = &ClassPlanWorkspace{}
		}
		entry, err := s.classPlansKV.Get(key)
		switch {
		case errors.Is(err, nats.ErrKeyNotFound):
			data, merr := json.Marshal(state)
			if merr != nil {
				return false, merr
			}
			_, err = s.classPlansKV.Create(key, data)
		case err != nil:
			return false, err
		default:
			existing := &ClassPlanState{}
			if jerr := json.Unmarshal(entry.Value(), existing); jerr == nil {
				if trigger != "" && (existing.Trigger == trigger || slices.Contains(existing.SupersededTriggers, trigger)) {
					return false, nil
				}
				state.SupersededTriggers = existing.SupersededTriggers
				if existing.Trigger != "" {
					state.SupersededTriggers = append(state.SupersededTriggers, existing.Trigger)
				}
				if n := len(state.SupersededTriggers); n > maxSupersededTriggers {
					state.SupersededTriggers = state.SupersededTriggers[n-maxSupersededTriggers:]
				}
			}
			data, merr := json.Marshal(state)
			if merr != nil {
				return false, merr
			}
			_, err = s.classPlansKV.Update(key, data, entry.Revision())
		}
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, nats.ErrKeyExists) {
			return false, err
		}
	}
	return false, errors.New("class plan state update exceeded retry limit")
}

// GetClassPlans returns the class plans of ref's commit, or
// ErrClassPlansNotFound when TFBuddy never began any.
func (s *Stream) GetClassPlans(ref ClassPlanRef) (*ClassPlanState, error) {
	if s.classPlansKV == nil {
		return nil, errors.New("class plan store is not configured")
	}
	entry, err := s.classPlansKV.Get(classPlansKey(ref))
	if errors.Is(err, nats.ErrKeyNotFound) {
		return nil, ErrClassPlansNotFound
	}
	if err != nil {
		return nil, err
	}
	state := &ClassPlanState{}
	if err := json.Unmarshal(entry.Value(), state); err != nil {
		return nil, err
	}
	return state, nil
}

// RegisterClassPlanRun makes runID the plan run that counts for workspace.
// trigger is the one that dispatched the run: when another trigger has begun
// the commit's class plans again since, the run does not count, and it
// returns ErrClassPlansSuperseded.
func (s *Stream) RegisterClassPlanRun(ref ClassPlanRef, workspace, runID, trigger string) error {
	if runID == "" {
		return errors.New("a class plan run needs a run ID")
	}
	_, _, err := s.updateClassPlans(ref, func(state *ClassPlanState) (bool, error) {
		if state.Trigger != trigger {
			return false, ErrClassPlansSuperseded
		}
		ws, ok := state.Workspaces[workspace]
		if !ok {
			return false, fmt.Errorf("workspace %s is not part of the class plans", workspace)
		}
		if ws.Outcome != ClassPlanPending || ws.RunID == runID {
			return false, nil
		}
		ws.RunID = runID
		return true, nil
	})
	return err
}

// RecordClassPlanResult records how workspace's class plan ended and returns
// the commit's state after the update. runID is the run that ended; an empty
// runID records a workspace that did not plan at all, which never passes.
// trigger is the one whose plans the result belongs to. A result for plans
// another trigger has begun again since, or for a run that does not count for
// the workspace, changes nothing. A failed workspace stays failed.
//
// finished is true for exactly one call: the one that gave the last workspace
// its outcome.
func (s *Stream) RecordClassPlanResult(ref ClassPlanRef, workspace, runID, trigger string, passed bool, detail string) (*ClassPlanState, bool, error) {
	return s.updateClassPlans(ref, func(state *ClassPlanState) (bool, error) {
		if state.Trigger != trigger {
			return false, nil
		}
		ws, ok := state.Workspaces[workspace]
		if !ok || (runID != "" && ws.RunID != runID) || ws.Outcome == ClassPlanFailed {
			return false, nil
		}
		outcome := ClassPlanFailed
		if passed && runID != "" {
			outcome = ClassPlanPassed
		}
		if ws.Outcome == outcome {
			return false, nil
		}
		ws.Outcome = outcome
		ws.Detail = detail
		return true, nil
	})
}

// MarkClassPlansReported records that TFC/class has been written for ref's
// finished class plans, begun by trigger, so that later events for the commit
// do not report them again. Plans another trigger has begun since are left
// alone.
func (s *Stream) MarkClassPlansReported(ref ClassPlanRef, trigger string) error {
	_, _, err := s.updateClassPlans(ref, func(state *ClassPlanState) (bool, error) {
		if state.Reported || state.Trigger != trigger {
			return false, nil
		}
		state.Reported = true
		return true, nil
	})
	return err
}

// updateClassPlans applies mutate to ref's state with compare-and-swap. It
// returns the state as written, and whether this write finished it.
func (s *Stream) updateClassPlans(ref ClassPlanRef, mutate func(*ClassPlanState) (bool, error)) (*ClassPlanState, bool, error) {
	if s.classPlansKV == nil {
		return nil, false, errors.New("class plan store is not configured")
	}
	key := classPlansKey(ref)
	for range autoModeCASRetries {
		entry, err := s.classPlansKV.Get(key)
		if errors.Is(err, nats.ErrKeyNotFound) {
			return nil, false, ErrClassPlansNotFound
		}
		if err != nil {
			return nil, false, err
		}
		state := &ClassPlanState{}
		if err := json.Unmarshal(entry.Value(), state); err != nil {
			return nil, false, err
		}
		changed, err := mutate(state)
		if err != nil {
			return nil, false, err
		}
		if !changed {
			return state, false, nil
		}
		finished := false
		if !state.Finished && state.allFinished() {
			state.Finished = true
			finished = true
		}
		data, err := json.Marshal(state)
		if err != nil {
			return nil, false, err
		}
		if _, err = s.classPlansKV.Update(key, data, entry.Revision()); err == nil {
			return state, finished, nil
		}
		if !errors.Is(err, nats.ErrKeyExists) {
			return nil, false, err
		}
	}
	return nil, false, errors.New("class plan state update exceeded retry limit")
}

// GetAutoModeSummaryNote returns the ID of the MR comment that summarizes its
// class plans, or 0 when TFBuddy has posted none.
func (s *Stream) GetAutoModeSummaryNote(ref AutoModeMRRef) (int64, error) {
	state, _, err := s.getAutoModeMRState(ref)
	if err != nil || state == nil {
		return 0, err
	}
	return state.SummaryNoteID, nil
}

// SetAutoModeSummaryNote records the ID of the MR comment that summarizes its
// class plans, so that later commits update it rather than add another.
func (s *Stream) SetAutoModeSummaryNote(ref AutoModeMRRef, noteID int64) error {
	_, err := s.updateAutoModeMRState(ref, func(state *AutoModeMRState) bool {
		if state.SummaryNoteID == noteID {
			return false
		}
		state.SummaryNoteID = noteID
		return true
	})
	return err
}

func classPlansKey(ref ClassPlanRef) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("class\x00%s\x00%s\x00%d\x00%s", ref.VcsProvider, ref.Project, ref.MergeRequest, ref.CommitSHA)))
	return hex.EncodeToString(hash[:])
}

func configureAutoModeClassPlansKVStore(js nats.JetStreamContext) (nats.KeyValue, error) {
	cfg := &nats.KeyValueConfig{
		Bucket:      AutoModeClassPlansKvBucket,
		Description: "Outcome of each class plan of an Auto Mode commit",
		TTL:         autoModeClassPlansTTL,
		Storage:     nats.FileStorage,
		Replicas:    1,
	}

	for store := range js.KeyValueStores() {
		if store.Bucket() == cfg.Bucket {
			return js.KeyValue(cfg.Bucket)
		}
	}
	return js.CreateKeyValue(cfg)
}
