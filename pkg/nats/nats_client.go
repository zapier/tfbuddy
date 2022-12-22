package nats

import (
	"os"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
)

func Connect() *nats.Conn {
	natsURL := os.Getenv("TFBUDDY_NATS_SERVICE_URL")
	if natsURL == "" {
		// try default
		natsURL = nats.DefaultURL
		log.Warn().Msg("TFBUDDY_NATS_SERVICE_URL is not set, trying default NATS URL")
	}

	// Connect to a server
	nc, err := nats.Connect(natsURL)

	if err != nil {
		log.Fatal().Err(err).Msg("could not connect to Nats")
	}

	nc.SetDisconnectErrHandler(func(nc *nats.Conn, err error) {
		log.Warn().Err(err).Msg("NATS Connection Error")
	})

	nc.SetDiscoveredServersHandler(func(conn *nats.Conn) {
		log.Info().Str("conn", conn.ConnectedAddr()).Msg("NATS Server discovered")
	})

	nc.SetErrorHandler(func(nc *nats.Conn, sub *nats.Subscription, err error) {
		log.Warn().Err(err).Str("queue", sub.Queue).Str("subject", sub.Subject).Msg("NATS Subscription Error")
	})

	nc.SetReconnectHandler(func(conn *nats.Conn) {
		log.Info().Str("conn", conn.ConnectedAddr()).Msg("NATS Server reconnect")
	})

	return nc
}

func HealthcheckFn(nc *nats.Conn) func() error {
	return func() error {
		status := nc.Status()
		if status != nats.CONNECTED {
			log.Warn().Msg("Not Connected to NATS")
			return nats.ErrConnectionClosed
		}

		return nil
	}
}
