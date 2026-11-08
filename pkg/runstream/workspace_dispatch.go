package runstream

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	// WorkspaceDispatchKvBucket records which workspaces each trigger has
	// already dispatched, and which runs have been counted as finished.
	WorkspaceDispatchKvBucket = "WORKSPACE_DISPATCHES"
	// workspaceDispatchTTL outlives every redelivery of a trigger: the HOOKS
	// and workspace fan-out streams both drop messages after an hour.
	workspaceDispatchTTL = 2 * time.Hour
)

var errWorkspaceDispatchStoreUnavailable = errors.New("workspace dispatch store is not configured")

// WorkspaceDispatchClaim names one workspace run requested by one trigger: a
// webhook delivery, or a merge request comment.
type WorkspaceDispatchClaim struct {
	VcsProvider  string `json:"vcsProvider"`
	Project      string `json:"project"`
	MergeRequest int    `json:"mergeRequest"`
	// CommitSHA is recorded for debugging but is not part of the claim's
	// identity: the GitHub comment path reads it from the live pull request
	// on every delivery, so a redelivery after the target branch moved would
	// otherwise claim the workspace a second time. Trigger already tells new
	// triggers apart.
	CommitSHA    string `json:"commitSHA"`
	Action       string `json:"action"`
	Trigger      string `json:"trigger"`
	Organization string `json:"organization"`
	Workspace    string `json:"workspace"`
}

func (c WorkspaceDispatchClaim) key() string {
	hash := sha256.Sum256([]byte(strings.Join([]string{
		c.VcsProvider, c.Project, fmt.Sprintf("%d", c.MergeRequest),
		c.Action, c.Trigger, c.Organization, c.Workspace,
	}, "\x00")))
	return hex.EncodeToString(hash[:])
}

type workspaceDispatchRecord struct {
	WorkspaceDispatchClaim
	Host      string    `json:"host"`
	ClaimedAt time.Time `json:"claimedAt"`
}

// ClaimWorkspaceDispatch records that claim's trigger is dispatching its
// workspace. It returns false when that trigger already claimed the
// workspace, so a redelivered trigger skips the workspaces it already ran.
func (s *Stream) ClaimWorkspaceDispatch(claim WorkspaceDispatchClaim) (bool, error) {
	if s.dispatchKV == nil {
		return false, errWorkspaceDispatchStoreUnavailable
	}
	host, _ := os.Hostname()
	value, err := json.Marshal(workspaceDispatchRecord{
		WorkspaceDispatchClaim: claim,
		Host:                   host,
		ClaimedAt:              time.Now().UTC(),
	})
	if err != nil {
		return false, fmt.Errorf("could not encode workspace dispatch claim: %w", err)
	}
	if _, err := s.dispatchKV.Create(claim.key(), value); err != nil {
		if errors.Is(err, nats.ErrKeyExists) {
			return false, nil
		}
		return false, fmt.Errorf("could not record workspace dispatch: %w", err)
	}
	return true, nil
}

func configureWorkspaceDispatchKVStore(js nats.JetStreamContext) (nats.KeyValue, error) {
	cfg := &nats.KeyValueConfig{
		Bucket:      WorkspaceDispatchKvBucket,
		Description: "Workspaces each TFBuddy trigger has dispatched, so a redelivered trigger does not start them again",
		TTL:         workspaceDispatchTTL,
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
