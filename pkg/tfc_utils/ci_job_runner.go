package tfc_utils

import (
	"context"
	"fmt"
	"strings"

	tfe "github.com/hashicorp/go-tfe"
	"github.com/rs/zerolog/log"
)

const RUN_MESSAGE_PREFIX = "GitLab CI Scheduled"

func StartScheduledRun(token, workspace string) {
	ctx := context.Background()

	log.Info().Str("workspace", workspace).Msgf("StartScheduledRun for workspace.")

	config := &tfe.Config{
		Token: token,
	}

	client, err := tfe.NewClient(config)
	if err != nil {
		log.Fatal().Err(err)
	}

	// read details of the Current run (if any)
	current := getCurrentRun(ctx, client, workspace)
	printRunInfo(current, "Current Run", workspace)

	if ok, cancel := shouldScheduleNewRun(current); ok {
		if cancel {
			client.Runs.Cancel(ctx, current.ID, tfe.RunCancelOptions{Comment: tfe.String("auto cancel hung run")})
		}
		// queue another run
		ws := getWorkspace(ctx, client, workspace)
		createOptions := tfe.RunCreateOptions{
			Type:      "runs",
			IsDestroy: tfe.Bool(false),
			Message:   tfe.String(RUN_MESSAGE_PREFIX),
			Workspace: ws,
		}
		run, err := client.Runs.Create(ctx, createOptions)
		if err != nil {
			log.Fatal().Msgf("failed to queue new run: %v", err)
		}

		printRunInfo(run, "New Run", workspace)
	}
}

func shouldScheduleNewRun(current *tfe.Run) (ok bool, cancel bool) {
	if isUnfinishedRun(current) {
		// There is a current run that's not in a terminal state
		// is this is a hung schedule run?
		if strings.HasPrefix(current.Message, RUN_MESSAGE_PREFIX) {
			if canCancelRun(current) {
				return true, true
			}
			return false, false

		} else {
			// some other run is in progress, this could be an MR / PR merge or manual run, let's not queue another
			log.Info().Msg("Run still in progress, not queueing a scheduled run.")
			return false, false
		}
	}

	return true, false
}

func getWorkspace(ctx context.Context, client *tfe.Client, wsName string) *tfe.Workspace {
	ws, err := client.Workspaces.Read(ctx, ORG_NAME, wsName)
	if err != nil {
		log.Fatal().Err(err)
	}
	if ws == nil {
		log.Fatal().Msgf("Workspace (%s) not found", wsName)
	}

	return ws
}

func getCurrentRun(ctx context.Context, client *tfe.Client, wsName string) *tfe.Run {
	ws := getWorkspace(ctx, client, wsName)
	if ws.CurrentRun == nil {
		return nil
	}

	run, err := client.Runs.Read(ctx, ws.CurrentRun.ID)
	if err != nil {
		log.Fatal().Err(err)
	}

	return run
}

func isUnfinishedRun(run *tfe.Run) bool {
	if run == nil {
		return false
	}

	switch run.Status {
	case "apply_queued":
		fallthrough
	case "applying":
		fallthrough
	case "confirmed":
		fallthrough
	case "cost_estimated":
		fallthrough
	case "cost_estimating":
		fallthrough
	case "plan_queued":
		fallthrough
	case "policy_checked":
		fallthrough
	case "policy_checking":
		fallthrough
	case "policy_soft_failed":
		fallthrough
	case "policy_override":
		fallthrough
	case "planned":
		fallthrough
	case "planning":
		fallthrough
	case "pending":
		return true
	}

	return false
}

func canCancelRun(run *tfe.Run) bool {
	if run == nil {
		return false
	}

	switch run.Status {
	case "policy_checked":
		fallthrough
	case "policy_override":
		return false
	}

	return true
}

func printRunInfo(run *tfe.Run, title, wsName string) {
	fmt.Printf(RunInfo, title, run.ID, run.Status, run.Source, run.Message, wsName, run.ID)
}
