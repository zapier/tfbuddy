package tfc_utils

import (
	"context"

	tfe "github.com/hashicorp/go-tfe"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
)

func LockUnlockWorkspace(ctx context.Context, token, workspace string, lock bool, lockReason string) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "LockUnlockWorkspace")
	defer span.End()

	log.Info().Str("workspace", workspace).Msgf("LockUnlockWorkspace for workspace.")

	config := &tfe.Config{
		Token: token,
	}

	LockOptions := tfe.WorkspaceLockOptions{Reason: &lockReason}

	client, err := tfe.NewClient(config)
	if err != nil {
		log.Fatal().Err(err)
	}

	if lock {
		_, err := client.Workspaces.Lock(
			ctx,
			workspace,
			LockOptions,
		)
		log.Info().Msgf("locking")
		if err != nil {
			log.Fatal().Err(err)
		}
	} else {
		_, err := client.Workspaces.Unlock(
			ctx,
			workspace,
		)
		if err != nil {
			log.Fatal().Err(err)
		}

	}

}
