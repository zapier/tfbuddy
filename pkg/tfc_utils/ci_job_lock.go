package tfc_utils

import (
	"context"

	tfe "github.com/hashicorp/go-tfe"
	"github.com/rs/zerolog/log"
)

func LockUnlockWorkspace(token, workspace string, lock bool, lockReason string) {
	ctx := context.Background()

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
