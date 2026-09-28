package gitlab_hooks

import (
	"testing"

	"github.com/rs/zerolog/log"
	"github.com/rzajac/zltest"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
)

// The merge request event path discards its result, so without this log an
// operator has no record of a workspace that did not run.
func TestLogErroredWorkspacesRecordsEachFailure(t *testing.T) {
	testLogger := zltest.New(t)
	original := log.Logger
	log.Logger = log.Logger.Output(testLogger)
	t.Cleanup(func() { log.Logger = original })

	logErroredWorkspaces("zapier/tfbuddy", 101, &tfc_trigger.TriggeredTFCWorkspaces{
		Errored: []*tfc_trigger.ErroredWorkspace{
			{Name: "svc-a", Error: "excluded by allow/deny list"},
		},
	})

	entry := testLogger.LastEntry()
	if entry == nil {
		t.Fatal("expected a log entry for the errored workspace")
	}
	entry.ExpStr("workspace", "svc-a")
	entry.ExpStr("reason", "excluded by allow/deny list")
}

func TestLogErroredWorkspacesToleratesNil(t *testing.T) {
	logErroredWorkspaces("zapier/tfbuddy", 101, nil)
}
