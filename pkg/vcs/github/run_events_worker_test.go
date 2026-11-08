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

func githubRunEvent(status tfe.RunStatus) *runstream.TFRunEvent {
	re := &runstream.TFRunEvent{
		RunID:     "run-123",
		NewStatus: string(status),
		Metadata: &runstream.TFRunMetadata{
			RunID:                                "run-123",
			Organization:                         "zapier",
			Workspace:                            "service-tfbuddy",
			Action:                               runstream.PlanAction,
			MergeRequestProjectNameWithNamespace: "zapier/tfbuddy",
			MergeRequestIID:                      101,
			VcsProvider:                          "github",
		},
	}
	re.SetContext(context.Background())
	return re
}

func githubRun(status tfe.RunStatus) *tfe.Run {
	return &tfe.Run{
		ID:        "run-123",
		Status:    status,
		Workspace: &tfe.Workspace{Name: "service-tfbuddy", Organization: &tfe.Organization{Name: "zapier"}},
	}
}

// The GitHub worker records a finished run once it has handled the final
// status, and nothing for a status in progress.
func TestEventStreamCallbackRecordsAFinishedRun(t *testing.T) {
	ctrl := gomock.NewController(t)
	tfc := mocks.NewMockApiClient(ctrl)
	tfc.EXPECT().GetRun(gomock.Any(), "run-123").Return(githubRun(tfe.RunErrored), nil)
	client := mocks.NewMockGitClient(ctrl)
	client.EXPECT().GetOldRunUrls(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	client.EXPECT().CreateMergeRequestComment(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	rs := mocks.NewMockStreamClient(ctrl)
	re := githubRunEvent(tfe.RunErrored)
	rs.EXPECT().RecordRunFinished("github", re).Times(1)
	w := &RunEventsWorker{cfg: config.C, tfc: tfc, client: client, rs: rs}

	if !w.eventStreamCallback(re) {
		t.Fatal("event was not acked")
	}
}

func TestEventStreamCallbackDoesNotRecordARunInProgress(t *testing.T) {
	ctrl := gomock.NewController(t)
	tfc := mocks.NewMockApiClient(ctrl)
	tfc.EXPECT().GetRun(gomock.Any(), "run-123").Return(githubRun(tfe.RunPlanning), nil)
	client := mocks.NewMockGitClient(ctrl)
	client.EXPECT().GetOldRunUrls(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	client.EXPECT().CreateMergeRequestComment(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	// No expectations on the stream: recording a finished run fails the test.
	w := &RunEventsWorker{cfg: config.C, tfc: tfc, client: client, rs: mocks.NewMockStreamClient(ctrl)}

	if !w.eventStreamCallback(githubRunEvent(tfe.RunPlanning)) {
		t.Fatal("event was not acked")
	}
}
