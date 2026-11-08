package runstream

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
)

// runsFinished is the completion side of tfbuddy_tfc_runs_started: a run
// TFBuddy started is counted here once a run event worker has handled its
// final status and updated the merge request. A run that is started but
// never counted here never got its final status reported.
var runsFinished = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "tfbuddy_tfc_runs_finished",
	Help: "Count of TFC runs whose final status TFBuddy reported back to the merge request, by VCS provider, run type and final status",
}, []string{"provider", "runType", "status"})

func init() {
	prometheus.MustRegister(runsFinished)
}

type runFinishedRecord struct {
	Provider   string    `json:"provider"`
	RunID      string    `json:"runID"`
	Status     string    `json:"status"`
	Host       string    `json:"host"`
	FinishedAt time.Time `json:"finishedAt"`
}

// runFinishedKey names a run's completion claim in the WORKSPACE_DISPATCHES
// bucket. The prefix keeps it apart from workspace dispatch claims, whose
// keys are bare hashes.
func runFinishedKey(provider, runID string) string {
	hash := sha256.Sum256([]byte(provider + "\x00" + runID))
	return "run-finished." + hex.EncodeToString(hash[:])
}

// RecordRunFinished counts re's run as finished, once per run. Run events are
// deduplicated when they are published, but JetStream delivers the final
// event again when its ack is lost, for example while a NATS pod restarts.
// So the first worker to handle it claims the run in the WORKSPACE_DISPATCHES
// bucket, and a later delivery of the same run is not counted. If the claim
// cannot be recorded the run is counted anyway: its status was reported, and
// a double count would also need the ack to be lost.
func (s *Stream) RecordRunFinished(provider string, re RunEvent) {
	first, err := s.claimRunFinished(provider, re)
	switch {
	case err != nil:
		log.Warn().Err(err).Str("runID", re.GetRunID()).Msg("could not record the finished run; counting it without deduplication")
	case !first:
		log.Info().Str("runID", re.GetRunID()).Msg("run already counted as finished; not counting a redelivered final event")
		return
	}
	runsFinished.WithLabelValues(provider, re.GetMetadata().GetAction(), re.GetNewStatus()).Inc()
}

func (s *Stream) claimRunFinished(provider string, re RunEvent) (bool, error) {
	if s.dispatchKV == nil {
		return false, errWorkspaceDispatchStoreUnavailable
	}
	host, _ := os.Hostname()
	value, err := json.Marshal(runFinishedRecord{
		Provider:   provider,
		RunID:      re.GetRunID(),
		Status:     re.GetNewStatus(),
		Host:       host,
		FinishedAt: time.Now().UTC(),
	})
	if err != nil {
		return false, fmt.Errorf("could not encode finished run: %w", err)
	}
	if _, err := s.dispatchKV.Create(runFinishedKey(provider, re.GetRunID()), value); err != nil {
		if errors.Is(err, nats.ErrKeyExists) {
			return false, nil
		}
		return false, fmt.Errorf("could not record finished run: %w", err)
	}
	return true, nil
}
