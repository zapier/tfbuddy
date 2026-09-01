package runstream

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	AutoMergeMetadataKvBucket = "AUTO_MERGE_METADATA"
	autoMergeStateTTL         = 30 * 24 * time.Hour
	autoMergeCASRetries       = 20
)

var ErrAutoMergeStateNotFound = errors.New("auto-merge state not found")

// AutoMergeWorkspaceState tracks the newest apply run for one workspace.
// Applied is only true when that exact run completed successfully without targets.
type AutoMergeWorkspaceState struct {
	Generation string `json:"generation,omitempty"`
	Sequence   int64  `json:"sequence,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	Applied    bool   `json:"applied"`
}

// AutoMergeState coordinates all workspaces affected by one MR commit.
type AutoMergeState struct {
	VcsProvider  string                              `json:"vcs_provider"`
	Project      string                              `json:"project"`
	MergeRequest int                                 `json:"merge_request"`
	CommitSHA    string                              `json:"commit_sha"`
	Eligible     bool                                `json:"eligible"`
	Workspaces   map[string]*AutoMergeWorkspaceState `json:"workspaces"`
	MergeClaimed bool                                `json:"merge_claimed"`
}

// AutoMergeRef identifies one workspace apply within an MR commit.
type AutoMergeRef struct {
	VcsProvider  string
	Project      string
	MergeRequest int
	CommitSHA    string
	Organization string
	Workspace    string
	RunID        string
	Generation   string
	Sequence     int64
}

func AutoMergeRefForRun(run RunMetadata) AutoMergeRef {
	return AutoMergeRef{
		VcsProvider:  run.GetVcsProvider(),
		Project:      run.GetMRProjectNameWithNamespace(),
		MergeRequest: run.GetMRInternalID(),
		CommitSHA:    run.GetCommitSHA(),
		Organization: run.GetOrganization(),
		Workspace:    run.GetWorkspace(),
		RunID:        run.GetRunID(),
		Generation:   run.GetAutoMergeGeneration(),
		Sequence:     run.GetAutoMergeSequence(),
	}
}

func NewAutoMergeState(vcsProvider, project string, mr int, commitSHA string, eligible bool, workspaces []string) *AutoMergeState {
	state := &AutoMergeState{
		VcsProvider:  vcsProvider,
		Project:      project,
		MergeRequest: mr,
		CommitSHA:    commitSHA,
		Eligible:     eligible,
		Workspaces:   make(map[string]*AutoMergeWorkspaceState, len(workspaces)),
	}
	for _, workspace := range workspaces {
		state.Workspaces[workspace] = &AutoMergeWorkspaceState{}
	}
	return state
}

func AutoMergeWorkspaceKey(organization, workspace string) string {
	return organization + "/" + workspace
}

func (s *Stream) EnsureAutoMergeState(state *AutoMergeState) error {
	if state == nil {
		return errors.New("auto-merge state is nil")
	}
	if len(state.Workspaces) == 0 {
		return errors.New("auto-merge state has no workspaces")
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = s.autoMergeKV.Create(autoMergeStateKey(state.VcsProvider, state.Project, state.MergeRequest, state.CommitSHA), data)
	if errors.Is(err, nats.ErrKeyExists) {
		return nil
	}
	return err
}

// BeginAutoMergeApply atomically marks every workspace selected by one apply
// command pending before any per-workspace run is dispatched.
func (s *Stream) BeginAutoMergeApply(ref AutoMergeRef, workspaces []string) error {
	_, err := s.updateAutoMergeAggregate(ref, func(state *AutoMergeState) (bool, bool, error) {
		if state.MergeClaimed {
			return false, false, nil
		}
		for _, key := range workspaces {
			if _, ok := state.Workspaces[key]; !ok {
				return false, false, fmt.Errorf("workspace %s is not part of auto-merge state", key)
			}
		}
		changed := false
		for _, key := range workspaces {
			workspace := state.Workspaces[key]
			if ref.Sequence > 0 && workspace.Sequence >= ref.Sequence {
				continue
			}
			if ref.Sequence == 0 && workspace.Generation == ref.Generation {
				continue
			}
			workspace.Generation = ref.Generation
			workspace.Sequence = ref.Sequence
			workspace.RunID = ""
			workspace.Applied = false
			changed = true
		}
		return changed, false, nil
	})
	return err
}

func (s *Stream) RegisterAutoMergeRun(ref AutoMergeRef) error {
	_, err := s.updateAutoMergeState(ref, func(state *AutoMergeState, workspace *AutoMergeWorkspaceState) (bool, bool) {
		// Once a merge is claimed, keep the snapshot immutable so a late or
		// duplicate apply registration cannot cause a second merge decision.
		if state.MergeClaimed || workspace.Generation != ref.Generation ||
			workspace.Sequence != ref.Sequence || workspace.RunID == ref.RunID {
			return false, false
		}
		workspace.RunID = ref.RunID
		workspace.Applied = false
		return true, false
	})
	return err
}

// RecordAutoMergeSuccess marks the current run successful and atomically claims
// the merge when every expected workspace is applied. Duplicate or stale run
// events are harmless and cannot claim the merge.
func (s *Stream) RecordAutoMergeSuccess(ref AutoMergeRef) (bool, error) {
	return s.updateAutoMergeState(ref, func(state *AutoMergeState, workspace *AutoMergeWorkspaceState) (bool, bool) {
		if workspace.Generation != ref.Generation || workspace.Sequence != ref.Sequence ||
			workspace.RunID == "" || workspace.RunID != ref.RunID {
			return false, false
		}
		if !workspace.Applied {
			workspace.Applied = true
		}
		if !state.Eligible || state.MergeClaimed || !allWorkspacesApplied(state) {
			return true, false
		}
		state.MergeClaimed = true
		return true, true
	})
}

func (s *Stream) ReleaseAutoMergeClaim(ref AutoMergeRef) error {
	_, err := s.updateAutoMergeState(ref, func(state *AutoMergeState, _ *AutoMergeWorkspaceState) (bool, bool) {
		if !state.MergeClaimed {
			return false, false
		}
		state.MergeClaimed = false
		return true, false
	})
	return err
}

type autoMergeMutation func(*AutoMergeState, *AutoMergeWorkspaceState) (changed bool, claim bool)
type autoMergeAggregateMutation func(*AutoMergeState) (changed bool, claim bool, err error)

func (s *Stream) updateAutoMergeState(ref AutoMergeRef, mutate autoMergeMutation) (bool, error) {
	return s.updateAutoMergeAggregate(ref, func(state *AutoMergeState) (bool, bool, error) {
		workspace, ok := state.Workspaces[AutoMergeWorkspaceKey(ref.Organization, ref.Workspace)]
		if !ok {
			return false, false, fmt.Errorf("workspace %s is not part of auto-merge state", AutoMergeWorkspaceKey(ref.Organization, ref.Workspace))
		}
		changed, claim := mutate(state, workspace)
		return changed, claim, nil
	})
}

func (s *Stream) updateAutoMergeAggregate(ref AutoMergeRef, mutate autoMergeAggregateMutation) (bool, error) {
	key := autoMergeStateKey(ref.VcsProvider, ref.Project, ref.MergeRequest, ref.CommitSHA)
	for range autoMergeCASRetries {
		entry, err := s.autoMergeKV.Get(key)
		if errors.Is(err, nats.ErrKeyNotFound) {
			return false, ErrAutoMergeStateNotFound
		}
		if err != nil {
			return false, err
		}

		state := &AutoMergeState{}
		if err := json.Unmarshal(entry.Value(), state); err != nil {
			return false, err
		}
		changed, claim, err := mutate(state)
		if err != nil {
			return false, err
		}
		if !changed {
			return claim, nil
		}
		data, err := json.Marshal(state)
		if err != nil {
			return false, err
		}
		if _, err = s.autoMergeKV.Update(key, data, entry.Revision()); err == nil {
			return claim, nil
		}
		if !errors.Is(err, nats.ErrKeyExists) {
			return false, err
		}
	}
	return false, errors.New("auto-merge state update exceeded retry limit")
}

func allWorkspacesApplied(state *AutoMergeState) bool {
	if len(state.Workspaces) == 0 {
		return false
	}
	for _, workspace := range state.Workspaces {
		if !workspace.Applied {
			return false
		}
	}
	return true
}

func autoMergeStateKey(vcsProvider, project string, mr int, commitSHA string) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d\x00%s", vcsProvider, project, mr, commitSHA)))
	return hex.EncodeToString(hash[:])
}

func configureAutoMergeMetadataKVStore(js nats.JetStreamContext) (nats.KeyValue, error) {
	cfg := &nats.KeyValueConfig{
		Bucket:      AutoMergeMetadataKvBucket,
		Description: "MR commit workspace state for safe auto-merge",
		TTL:         autoMergeStateTTL,
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
