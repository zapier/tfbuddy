package tfc_trigger_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/hashicorp/go-tfe"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
)

// claimRecorder stands in for the dispatch claim store, so a test can decide
// which workspaces an earlier delivery already claimed.
type claimRecorder struct {
	runstream.StreamClient
	claims []runstream.WorkspaceDispatchClaim
	answer func(runstream.WorkspaceDispatchClaim) (bool, error)
}

func (r *claimRecorder) ClaimWorkspaceDispatch(c runstream.WorkspaceDispatchClaim) (bool, error) {
	r.claims = append(r.claims, c)
	return r.answer(c)
}

// newPlanTestSuite sets up a plan of the given workspaces, each in its own
// directory with a modified file, and expects a run to start for each
// workspace in started.
func newPlanTestSuite(t *testing.T, workspaces []string, started ...string) *mocks.TestSuite {
	t.Helper()
	cfg := &tfc_trigger.ProjectConfig{}
	var modified []string
	for _, ws := range workspaces {
		cfg.Workspaces = append(cfg.Workspaces, &tfc_trigger.TFCWorkspace{Name: ws, Organization: "zapier-test", Mode: "apply-before-merge", Dir: ws + "/"})
		modified = append(modified, ws+"/main.tf")
	}
	mockCtrl := gomock.NewController(t)
	ts := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{ProjectConfig: cfg}, t)
	ts.MockGitClient.EXPECT().GetMergeRequestModifiedFiles(gomock.Any(), ts.MetaData.MRIID, ts.MetaData.ProjectNameNS).Return(modified, nil).AnyTimes()

	for _, ws := range started {
		ts.MockGitClient.EXPECT().CreateMergeRequestDiscussion(gomock.Any(), ts.MetaData.MRIID, ts.MetaData.ProjectNameNS,
			fmt.Sprintf("Starting TFC plan for Workspace: `zapier-test/%s`.\n<!-- tfbuddy:ws=%s:action=plan -->", ws, ws)).
			Return(ts.MockGitDisc, nil)
	}
	runs := map[string]bool{}
	ts.MockApiClient.EXPECT().CreateRunFromSource(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, opts *tfc_api.ApiRunOptions) (*tfe.Run, error) {
			require.False(t, runs[opts.Workspace], "second run created for %s", opts.Workspace)
			runs[opts.Workspace] = true
			return &tfe.Run{
				ID:                   "run-" + opts.Workspace,
				Workspace:            &tfe.Workspace{Name: opts.Workspace, Organization: &tfe.Organization{Name: "zapier-test"}},
				ConfigurationVersion: &tfe.ConfigurationVersion{Speculative: true},
			}, nil
		}).Times(len(started))
	polling := mocks.NewMockRunPollingTask(mockCtrl)
	polling.EXPECT().Schedule(gomock.Any()).Return(nil).Times(len(started))
	ts.MockStreamClient.EXPECT().NewTFRunPollingTask(gomock.Any(), time.Second).Return(polling).Times(len(started))

	ts.InitTestSuite()
	return ts
}

func triggerPlan(t *testing.T, ts *mocks.TestSuite, rs runstream.StreamClient, opts tfc_trigger.TFCTriggerOptions) *tfc_trigger.TriggeredTFCWorkspaces {
	t.Helper()
	opts.Action = tfc_trigger.PlanAction
	opts.Branch = ts.MetaData.SourceBranch
	opts.CommitSHA = "abcd12233"
	opts.ProjectNameWithNamespace = ts.MetaData.ProjectNameNS
	opts.MergeRequestIID = ts.MetaData.MRIID
	opts.VcsProvider = "gitlab"
	cfg, err := tfc_trigger.NewTFCTriggerConfig(&opts)
	require.NoError(t, err)
	got, err := tfc_trigger.NewTFCTrigger(config.C, ts.MockGitClient, ts.MockApiClient, rs, cfg).TriggerTFCEvents(context.Background())
	require.NoError(t, err)
	require.Empty(t, got.Errored)
	return got
}

// A redelivered merge request event must not start a second run for the
// workspaces its first delivery already dispatched.
func TestTFCEvents_RedeliveredTriggerSkipsWorkspacesItAlreadyDispatched(t *testing.T) {
	ts := newPlanTestSuite(t, []string{"ws-a", "ws-b"}, "ws-a")
	rs := &claimRecorder{StreamClient: ts.MockStreamClient, answer: func(c runstream.WorkspaceDispatchClaim) (bool, error) {
		return c.Workspace != "ws-b", nil // ws-b was dispatched by the first delivery
	}}

	got := triggerPlan(t, ts, rs, tfc_trigger.TFCTriggerOptions{
		TriggerSource: tfc_trigger.MergeRequestEventTrigger,
		DeliveryID:    "delivery-123",
	})

	require.ElementsMatch(t, []string{"ws-a", "ws-b"}, got.Executed)
	// Workspaces are not dispatched in a fixed order, so find ws-a's claim.
	require.Len(t, rs.claims, 2)
	byWorkspace := map[string]runstream.WorkspaceDispatchClaim{}
	for _, c := range rs.claims {
		byWorkspace[c.Workspace] = c
	}
	require.Equal(t, runstream.WorkspaceDispatchClaim{
		VcsProvider:  "gitlab",
		Project:      ts.MetaData.ProjectNameNS,
		MergeRequest: ts.MetaData.MRIID,
		CommitSHA:    "abcd12233",
		Action:       "plan",
		Trigger:      "delivery:delivery-123",
		Organization: "zapier-test",
		Workspace:    "ws-a",
	}, byWorkspace["ws-a"])
	require.Contains(t, byWorkspace, "ws-b")
}

func TestTFCEvents_CommentWithoutDeliveryIDClaimsByNoteID(t *testing.T) {
	ts := newPlanTestSuite(t, []string{"ws-a"}, "ws-a")
	rs := &claimRecorder{StreamClient: ts.MockStreamClient, answer: func(runstream.WorkspaceDispatchClaim) (bool, error) { return true, nil }}

	triggerPlan(t, ts, rs, tfc_trigger.TFCTriggerOptions{
		TriggerSource:     tfc_trigger.CommentTrigger,
		AutoMergeSequence: 42,
	})

	require.Len(t, rs.claims, 1)
	require.Equal(t, "note:42", rs.claims[0].Trigger)
}

// Without a delivery or note ID there is nothing to tell a redelivery from a
// new request, so the workspace is dispatched unclaimed, as before.
func TestTFCEvents_TriggerWithoutIdentityIsNotClaimed(t *testing.T) {
	ts := newPlanTestSuite(t, []string{"ws-a"}, "ws-a")
	rs := &claimRecorder{StreamClient: ts.MockStreamClient, answer: func(runstream.WorkspaceDispatchClaim) (bool, error) { return false, nil }}

	got := triggerPlan(t, ts, rs, tfc_trigger.TFCTriggerOptions{TriggerSource: tfc_trigger.MergeRequestEventTrigger})

	require.Empty(t, rs.claims)
	require.Equal(t, []string{"ws-a"}, got.Executed)
}

func TestTFCEvents_UnavailableClaimStoreStillDispatches(t *testing.T) {
	ts := newPlanTestSuite(t, []string{"ws-a"}, "ws-a")
	rs := &claimRecorder{StreamClient: ts.MockStreamClient, answer: func(runstream.WorkspaceDispatchClaim) (bool, error) {
		return false, errors.New("nats: timeout")
	}}

	got := triggerPlan(t, ts, rs, tfc_trigger.TFCTriggerOptions{
		TriggerSource: tfc_trigger.MergeRequestEventTrigger,
		DeliveryID:    "delivery-123",
	})

	require.Len(t, rs.claims, 1)
	require.Equal(t, []string{"ws-a"}, got.Executed)
}
