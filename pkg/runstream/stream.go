package runstream

import (
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/rs/zerolog/log"
)

const RunMetadataKvBucket = "RUN_METADATA"

type Stream struct {
	//nc         *nats.Conn
	js          nats.JetStreamContext
	jsc         jetstream.JetStream // reads the run event and polling queues
	metadataKV  nats.KeyValue
	pollingKV   nats.KeyValue
	autoMergeKV nats.KeyValue
	dispatchKV  nats.KeyValue
	// completionKV records the runs already counted as finished.
	completionKV nats.KeyValue
}

// NewStream wires up the runstream JetStream client. dedupWindow is the
// JetStream Duplicates window applied to the RUN_EVENTS stream so that
// Nats-Msg-Id-based dedup actually fires server-side. Operators tune it via
// TFBUDDY_JETSTREAM_DEDUP_WINDOW. jsc, created from the same connection,
// reads the run event and polling queues.
func NewStream(js nats.JetStreamContext, jsc jetstream.JetStream, dedupWindow time.Duration) StreamClient {

	// Each step waits for NATS if it is briefly unavailable, for example while
	// a NATS server restarts during a rollout, and all of them share one
	// window. Without the streams and the metadata, polling and auto-merge
	// buckets TFBuddy can't work, so it exits if they still can't be set up.
	deadline := newSetupDeadline()
	mustSetUp(deadline, RunEventsStreamName+" stream", noValue(func() error { return configureTFRunEventsStream(js, dedupWindow) }))
	mustSetUp(deadline, RunPollingStreamNameV0+" stream", noValue(func() error { return configureTFRunPollingTaskStream(js) }))
	kv := mustSetUp(deadline, RunMetadataKvBucket+" bucket", func() (nats.KeyValue, error) { return configureTFRunMetadataKVStore(js) })
	pollingKV := mustSetUp(deadline, RunPollingKvName+" bucket", func() (nats.KeyValue, error) { return configureRunPollingKVStore(js) })
	autoMergeKV := mustSetUp(deadline, AutoMergeMetadataKvBucket+" bucket", func() (nats.KeyValue, error) { return configureAutoMergeMetadataKVStore(js) })
	// The dispatch claims only guard against duplicate runs, so TFBuddy runs
	// without them rather than not at all.
	dispatchKV, err := setUpWithRetry(deadline, WorkspaceDispatchKvBucket+" bucket", func() (nats.KeyValue, error) { return configureWorkspaceDispatchKVStore(js) })
	if err != nil {
		log.Error().Err(err).Msg("could not configure workspace dispatch KV store; redelivered triggers may start duplicate runs")
	}
	// The completion records only keep a run from being counted as finished
	// twice, so TFBuddy runs without them too.
	completionKV, err := setUpWithRetry(deadline, RunCompletionKvBucket+" bucket", func() (nats.KeyValue, error) { return configureRunCompletionKVStore(js) })
	if err != nil {
		log.Error().Err(err).Msg("could not configure run completion KV store; a redelivered final run event may be counted twice")
	}

	s := &Stream{
		js:           js,
		jsc:          jsc,
		metadataKV:   kv,
		pollingKV:    pollingKV,
		autoMergeKV:  autoMergeKV,
		dispatchKV:   dispatchKV,
		completionKV: completionKV,
	}

	s.startPollingTaskDispatcher()

	return s
}

func (s *Stream) HealthCheck() error {
	for s := range s.js.Streams() {
		switch s.Config.Name {
		case RunPollingKvName:
			fallthrough
		case RunPollingStreamNameV0:

			if s.State.Consumers < 1 {
				log.Warn().Str("stream", s.Config.Name).
					Int("consumers", s.State.Consumers).
					Uint64("msgs", s.State.Msgs).
					Str("cluster_leader", s.Cluster.Leader).
					Msg("Healthcheck status.")
				return fmt.Errorf("%s stream has no consumers", s.Config.Name)
			}
		default:
			log.Trace().Str("stream", s.Config.Name).
				Int("consumers", s.State.Consumers).
				Uint64("msgs", s.State.Msgs).
				Str("cluster_leader", s.Cluster.Leader).
				Msg("Healthcheck status.")
		}

	}

	return nil
}

func addOrUpdateStream(js nats.JetStreamContext, sCfg *nats.StreamConfig) error {
	strInfo, err := js.StreamInfo(sCfg.Name)
	if err != nil && err != nats.ErrStreamNotFound {
		log.Error().Err(err).Msgf("error reading %s stream info", sCfg.Name)
		return err
	}

	if strInfo == nil {
		_, err = js.AddStream(sCfg)
		if err != nil {
			log.Error().Err(err).Msgf("could not create %s stream", sCfg.Name)
			return err
		}

	} else if needsMigration(&strInfo.Config, sCfg) {
		err = migrateStream(js, sCfg.Name, sCfg)
		if err != nil {
			log.Error().Err(err).Msg("stream migration failed")
		}

	} else {
		_, err = js.UpdateStream(sCfg)
		if err != nil {
			log.Error().Err(err).Msgf("error updating %s stream", sCfg.Name)
			return err
		}

	}

	return err
}

func needsMigration(oCfg, tCfg *nats.StreamConfig) bool {
	return oCfg.Retention != tCfg.Retention
}

func migrateStream(js nats.JetStreamContext, name string, targetCfg *nats.StreamConfig) error {
	log.Info().Str("stream", name).Msg("starting migration for NATS stream")
	strInfo, err := js.StreamInfo(name)
	if err != nil {
		return err
	}

	// create intermediary stream
	now := time.Now()
	migName := fmt.Sprintf("%s_MIGRATION_%d", name, now.Unix())
	migCfg := strInfo.Config
	migCfg.Name = migName
	migCfg.Subjects = nil
	migCfg.Sources = []*nats.StreamSource{
		{
			Name:         name,
			OptStartTime: &now,
		},
	}
	err = addOrUpdateStream(js, &migCfg)
	if err != nil {
		return err
	}
	defer js.DeleteStream(migName)

	// wait for migration stream to catch up
	err = waitForStreamSources(js, migName)
	if err != nil {
		return fmt.Errorf("unexpected error while checking stream sync status (%s): %v", migName, err)
	}

	// delete OG stream
	err = js.DeleteStream(name)
	if err != nil {
		return fmt.Errorf("could not delete original stream (%s) for stream migration: %v", name, err)
	}
	// remove mirror
	migCfg.Sources = nil
	_, err = js.UpdateStream(&migCfg)
	if err != nil {
		return fmt.Errorf("could not remove sources (%s) for stream migration: %v", name, err)
	}

	// create replacement stream
	targetCfg.Sources = []*nats.StreamSource{{Name: migName}}
	_, err = js.AddStream(targetCfg)
	if err != nil {
		return fmt.Errorf("could not create replacement stream (%s) for stream migration: %v", name, err)
	}
	defer func() {
		targetCfg.Sources = nil
		_, err = js.UpdateStream(targetCfg)
		if err != nil {
			log.Error().Err(err).Msgf("could not cleanup migration sources for stream (%s)", name)
		}
	}()

	err = waitForStreamSources(js, name)
	if err != nil {
		return fmt.Errorf("unexpected error while checking stream sync status (%s): %v", name, err)
	}

	log.Info().Str("stream", name).Msg("migration complete")

	return nil
}

func waitForStreamSources(js nats.JetStreamContext, stream string) error {
	strInfo, err := js.StreamInfo(stream)
	if err != nil {
		return fmt.Errorf("could not read stream info stream (%s): %v", stream, err)
	}

	if len(strInfo.Sources) > 0 {
		for {
			strInfo, err := js.StreamInfo(stream)
			if err != nil {
				return fmt.Errorf("could not read stream info stream (%s): %v", stream, err)
			}

			// assume we only care about a single source
			srcInfo := strInfo.Sources[0]
			if srcInfo.Lag > 0 {
				log.Debug().Str("stream", stream).Str("source", srcInfo.Name).Uint64("lag", srcInfo.Lag).Msg("waiting for stream to sync from source")
				time.Sleep(1 * time.Second)
			} else {
				return nil
			}
		}
	}
	return nil
}
