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

	"github.com/zapier/tfbuddy/pkg/tfc_api"
)

const (
	// RunCompletionKvBucket records the runs already counted in
	// tfbuddy_tfc_runs_finished.
	RunCompletionKvBucket = "RUN_COMPLETIONS"
	// runCompletionTTL outlives every message in RUN_EVENTS, so a final event
	// that is redelivered late still finds its run's record.
	runCompletionTTL = runEventsMaxAge + time.Hour
)

// The completion SLI compares these two counters. Both have only bounded
// labels, and every combination is created at zero when TFBuddy starts:
// rate() and increase() ignore the value a series starts with, so a series
// that first appears at 1 would hide its first run. tfbuddy_tfc_runs_started
// has a workspace label and cannot be set up that way.
var (
	runsCreated = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tfbuddy_tfc_runs_created",
		Help: "Count of TFC runs TFBuddy created, by VCS provider and run type",
	}, []string{"provider", "runType"})
	runsFinished = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tfbuddy_tfc_runs_finished",
		Help: "Count of TFC runs whose final status event a TFBuddy run event worker handled, by VCS provider, run type and final status",
	}, []string{"provider", "runType", "status"})
)

var (
	metricProviders = []string{"gitlab", "github"}
	metricRunTypes  = []string{PlanAction, ApplyAction}
)

func init() {
	prometheus.MustRegister(runsCreated, runsFinished)
	for _, provider := range metricProviders {
		for _, runType := range metricRunTypes {
			runsCreated.WithLabelValues(provider, runType)
			for _, status := range tfc_api.FinalRunStatuses() {
				runsFinished.WithLabelValues(provider, runType, string(status))
			}
		}
	}
}

// RecordRunCreated counts a TFC run TFBuddy created. runType is the
// trigger's action, plan or apply.
func RecordRunCreated(provider, runType string) {
	runsCreated.WithLabelValues(provider, runType).Inc()
}

type runFinishedRecord struct {
	Provider   string    `json:"provider"`
	RunID      string    `json:"runID"`
	Status     string    `json:"status"`
	Host       string    `json:"host"`
	FinishedAt time.Time `json:"finishedAt"`
}

func runFinishedKey(provider, runID string) string {
	hash := sha256.Sum256([]byte(provider + "\x00" + runID))
	return hex.EncodeToString(hash[:])
}

// RecordRunFinished counts re's run once its final status event has been
// handled. That means the handler ran to the end, not that every comment and
// commit status reached the merge request: those failures are logged.
//
// Run events are deduplicated when they are published, but JetStream
// delivers the final event again when its ack is lost, for example while a
// NATS pod restarts. So the first worker to handle it records the run in the
// RUN_COMPLETIONS bucket, and a later delivery of the same run is not
// counted. If the record cannot be written the run is counted anyway, since
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
	if s.completionKV == nil {
		return false, errors.New("run completion store is not configured")
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
	if _, err := s.completionKV.Create(runFinishedKey(provider, re.GetRunID()), value); err != nil {
		if errors.Is(err, nats.ErrKeyExists) {
			return false, nil
		}
		return false, fmt.Errorf("could not record finished run: %w", err)
	}
	return true, nil
}

func configureRunCompletionKVStore(js nats.JetStreamContext) (nats.KeyValue, error) {
	cfg := &nats.KeyValueConfig{
		Bucket:      RunCompletionKvBucket,
		Description: "TFC runs TFBuddy has counted as finished, so a redelivered final run event is not counted again",
		TTL:         runCompletionTTL,
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
