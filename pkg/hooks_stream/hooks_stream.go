package hooks_stream

import (
	"fmt"
	"time"

	nats "github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
)

const HooksStreamName = "HOOKS"

type HooksStream struct {
	nc *nats.Conn
	js nats.JetStreamContext
}

func NewHooksStream(nc *nats.Conn) *HooksStream {
	js, err := nc.JetStream(nats.PublishAsyncMaxPending(256))
	if err != nil {
		log.Fatal().Err(err).Msg("could not create Jetstream context for hooks stream")
	}

	configureHooksStream(js)

	s := &HooksStream{
		nc,
		js,
	}

	return s
}

func configureHooksStream(js nats.JetStreamContext) {
	var err error
	sCfg := &nats.StreamConfig{
		Name:        HooksStreamName,
		Description: "Gitlab/Github Hooks Stream",
		Subjects:    []string{fmt.Sprintf("%s.>", HooksStreamName)},
		Retention:   nats.WorkQueuePolicy,
		MaxMsgs:     10240,
		MaxAge:      time.Hour * 1,
		Replicas:    1,
	}

	strInfo, err := js.StreamInfo(sCfg.Name)
	if err != nil && err != nats.ErrStreamNotFound {
		log.Error().Err(err).Msg("error reading hook stream info")
	}

	if strInfo == nil {
		_, err = js.AddStream(sCfg)
		if err != nil {
			log.Error().Err(err).Msg("could not create hook stream")
		}
	} else {
		_, err = js.UpdateStream(sCfg)
		if err != nil {
			log.Fatal().Err(err).Msg("error updating hooks stream")
		}
	}

	if err != nil {
		log.Fatal().Msg("could not setup hook streams, good bye.")
	}
}

func (s *HooksStream) HealthCheck() error {
	for s := range s.js.Streams() {
		switch s.Config.Name {
		case HooksStreamName:

			if s.State.Consumers < 1 {
				clusterLeader := ""
				if s.Cluster != nil {
					clusterLeader = s.Cluster.Leader
				}
				log.Warn().Str("stream", s.Config.Name).
					Int("consumers", s.State.Consumers).
					Uint64("msgs", s.State.Msgs).
					Str("cluster_leader", clusterLeader).
					Msg("Healthcheck status.")
				return fmt.Errorf("%s stream has no consumers", s.Config.Name)
			}
		default:
			clusterLeader := ""
			if s.Cluster != nil {
				clusterLeader = s.Cluster.Leader
			}
			log.Trace().Str("stream", s.Config.Name).
				Int("consumers", s.State.Consumers).
				Uint64("msgs", s.State.Msgs).
				Str("cluster_leader", clusterLeader).
				Msg("Healthcheck status.")
		}

	}

	return nil
}
