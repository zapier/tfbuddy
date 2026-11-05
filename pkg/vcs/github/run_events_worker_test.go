package github

import (
	"context"
	"testing"

	"github.com/hashicorp/go-tfe"
	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"go.uber.org/mock/gomock"
)

// An event for an earlier status that arrives after the run has finished
// must not post anything to the pull request.
func TestEventStreamCallbackSkipsStaleEvent(t *testing.T) {
	ctrl := gomock.NewController(t)
	tfc := mocks.NewMockApiClient(ctrl)
	tfc.EXPECT().GetRun(gomock.Any(), "run-123").Return(&tfe.Run{
		ID:        "run-123",
		Status:    tfe.RunPlannedAndFinished,
		Workspace: &tfe.Workspace{Name: "service-tfbuddy", Organization: &tfe.Organization{Name: "zapier"}},
	}, nil)
	// No expectations on the Git client: any comment fails the test.
	w := &RunEventsWorker{cfg: config.C, tfc: tfc, client: mocks.NewMockGitClient(ctrl)}

	re := &runstream.TFRunEvent{
		RunID:     "run-123",
		NewStatus: string(tfe.RunPlanning),
		Metadata: &runstream.TFRunMetadata{
			RunID:                                "run-123",
			Action:                               runstream.PlanAction,
			MergeRequestProjectNameWithNamespace: "zapier/tfbuddy",
			MergeRequestIID:                      101,
			VcsProvider:                          "github",
		},
	}
	re.SetContext(context.Background())

	if !w.eventStreamCallback(re) {
		t.Fatal("a stale event must be acked, not retried")
	}
}
