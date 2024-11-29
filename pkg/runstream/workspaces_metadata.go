package runstream

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

const WorkspaceMetadataKvBucket = "WORKSPACE_METADATA"

func configureWorkspaceMetadataKVStore(js nats.JetStreamContext) (nats.KeyValue, error) {
	cfg := &nats.KeyValueConfig{
		Bucket:      WorkspaceMetadataKvBucket,
		Description: "KV store for Workspace Metadata",
		TTL:         time.Hour * 720,
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

type WorkspaceMetadata interface {
	GetCountExecutedWorkspaces() int
	GetCountTotalWorkspaces() int
}
type TFCWorkspacesMetadata struct {
	CountExecutedWorkspaces int `json:"count_executed_workspaces"`
	CountTotalWorkspaces    int `json:"count_total_workspaces"`
}

func (t *TFCWorkspacesMetadata) GetCountExecutedWorkspaces() int {
	return t.CountExecutedWorkspaces
}
func (t *TFCWorkspacesMetadata) GetCountTotalWorkspaces() int {
	return t.CountTotalWorkspaces
}
func encodeWorkspaceMetadata(run WorkspaceMetadata) ([]byte, error) {
	return json.Marshal(run)
}

func decodeWorkspaceMetadata(b []byte) (*TFCWorkspacesMetadata, error) {
	rmd := &TFCWorkspacesMetadata{}
	err := json.Unmarshal(b, &rmd)

	return rmd, err
}
func (s *Stream) AddWorkspaceMeta(rmd WorkspaceMetadata, mrID, workspace string) error {
	b, err := encodeWorkspaceMetadata(rmd)
	if err != nil {
		return err
	}
	_, err = s.metadataKV.Put(getKey(mrID, workspace), b)
	return err
}
func getKey(mrID, workspace string) string {
	return fmt.Sprintf("%s-%s", mrID, workspace)
}
func (s *Stream) GetWorkspaceMeta(mrID, workspace string) (*TFCWorkspacesMetadata, error) {
	entry, err := s.metadataKV.Get(getKey(mrID, workspace))
	if err != nil {
		return nil, err
	}
	return decodeWorkspaceMetadata(entry.Value())
}
