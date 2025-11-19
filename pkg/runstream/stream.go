package runstream

import (
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
)

const RunMetadataKvBucket = "RUN_METADATA"

type Stream struct {
	//nc         *nats.Conn
	js          nats.JetStreamContext
	metadataKV  nats.KeyValue
	pollingKV   nats.KeyValue
	workspaceKV nats.KeyValue
}

func NewStream(js nats.JetStreamContext) StreamClient {

	configureTFRunEventsStream(js)
	configureTFRunPollingTaskStream(js)
	kv, _ := configureTFRunMetadataKVStore(js)
	pollingKV, _ := configureRunPollingKVStore(js)
	workspaceKV, _ := configureWorkspaceMetadataKVStore(js)

	s := &Stream{
		js,
		kv,
		pollingKV,
		workspaceKV,
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
