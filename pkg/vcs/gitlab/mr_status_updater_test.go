package gitlab

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/go-tfe"
	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/utils"
	"github.com/zapier/tfbuddy/pkg/vcs"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
	"go.uber.org/mock/gomock"
)

const testAutoMergeIntentCommentID int64 = 999

func testAutoMergeRunMetadata() *runstream.TFRunMetadata {
	return &runstream.TFRunMetadata{
		RunID:                                "run-123",
		Organization:                         "zapier",
		Workspace:                            "service-tfbuddy",
		Action:                               runstream.ApplyAction,
		CommitSHA:                            "commit-123",
		MergeRequestProjectNameWithNamespace: "zapier/tfbuddy",
		MergeRequestIID:                      101,
		VcsProvider:                          "gitlab",
		AutoMerge:                            true,
		AutoMergeGeneration:                  "delivery-123",
		AutoMergeSequence:                    123,
	}
}

func expectAutoMergeIntentComment(testSuite *mocks.TestSuite) *gomock.Call {
	return testSuite.MockGitClient.EXPECT().
		CreateMergeRequestCommentWithID(
			gomock.Any(),
			101,
			"zapier/tfbuddy",
			"All expected workspaces have been applied successfully. Auto-merging this MR.",
		).
		Return(testAutoMergeIntentCommentID, nil)
}

func expectAutoMergeFailureComment(testSuite *mocks.TestSuite, reason string) {
	testSuite.MockGitClient.EXPECT().
		GetAuthenticatedAccountName(gomock.Any()).
		Return("tfbuddy-localdev", nil)
	testSuite.MockGitClient.EXPECT().
		UpdateMergeRequestComment(
			gomock.Any(),
			101,
			testAutoMergeIntentCommentID,
			"zapier/tfbuddy",
			autoMergeFailureComment(reason),
		).
		Return(nil)
}

func autoMergeFailureComment(reason string) string {
	return "Failed to auto-merge this MR.\n\nReason: tfbuddy-localdev: " + reason +
		". Please merge manually."
}

// TestUpdateStatusForwardsRunIdentity pins what updateStatus still owns after
// the status building moved into GitlabClient.SetWorkspaceStatus: mapping run
// metadata onto the right workspace, action, state and TFC run URL. Pipeline
// ID attachment is now the client's job and is covered end-to-end against a
// real HTTP request by TestSetWorkspaceStatusPostsSkippedToTheMRPipeline.
func TestUpdateStatusForwardsRunIdentity(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	var got vcs.WorkspaceStatus
	testSuite.MockGitClient.EXPECT().
		SetWorkspaceStatus(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s vcs.WorkspaceStatus) error {
			got = s
			return nil
		}).Times(1)

	testSuite.InitTestSuite()
	r := &RunStatusUpdater{
		cfg:    config.C,
		tfc:    testSuite.MockApiClient,
		client: testSuite.MockGitClient,
		rs:     testSuite.MockStreamClient,
	}

	r.updateStatus(context.Background(), gogitlab.Success, "apply", &runstream.TFRunMetadata{
		Action:                               "apply",
		Workspace:                            "service-tfbuddy",
		Organization:                         "zapier",
		RunID:                                "run-123",
		CommitSHA:                            "commit-123",
		MergeRequestProjectNameWithNamespace: "zapier/tfbuddy",
		MergeRequestIID:                      101,
	})

	if got.Workspace != "service-tfbuddy" {
		t.Errorf("Workspace = %q, want service-tfbuddy", got.Workspace)
	}
	if got.Action != "apply" {
		t.Errorf("Action = %q, want apply", got.Action)
	}
	if got.State != vcs.CommitStateSuccess {
		t.Errorf("State = %q, want success", got.State)
	}
	if got.CommitSHA != "commit-123" || got.Project != "zapier/tfbuddy" || got.MergeRequestIID != 101 {
		t.Errorf("status targets the wrong commit or merge request: %+v", got)
	}
	// The run URL is what makes the pipeline job clickable through to TFC.
	if !strings.Contains(got.TargetURL, "run-123") {
		t.Errorf("TargetURL = %q, want it to point at run-123", got.TargetURL)
	}
}

func TestAutoMergeNoChangesApply(t *testing.T) {

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	testSuite.MockStreamClient.EXPECT().RecordAutoMergeSuccess(gomock.Any()).Return(true, nil)
	expectAutoMergeIntentComment(testSuite)
	testSuite.MockGitClient.EXPECT().MergeMRAtSHA(gomock.Any(), 101, "zapier/tfbuddy", "commit-123")
	testSuite.MockGitClient.EXPECT().GetPipelinesForCommit(gomock.Any(), gomock.Any(), gomock.Any()).Return([]vcs.ProjectPipeline{&GitlabPipeline{&gogitlab.PipelineInfo{ID: 1}}}, nil).AnyTimes()
	testSuite.MockGitClient.EXPECT().SetCommitStatus(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("could not commit status")).AnyTimes()
	testSuite.InitTestSuite()
	r := &RunStatusUpdater{
		cfg:    config.C,
		tfc:    testSuite.MockApiClient,
		client: testSuite.MockGitClient,
		rs:     testSuite.MockStreamClient,
	}
	r.updateCommitStatusForRun(context.Background(), &tfe.Run{
		Status:     tfe.RunPlannedAndFinished,
		HasChanges: false,
	}, testAutoMergeRunMetadata())
}

func TestAutoMergeTargetedNoChangesApply(t *testing.T) {

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	testSuite.MockGitClient.EXPECT().GetPipelinesForCommit(gomock.Any(), gomock.Any(), gomock.Any()).Return([]vcs.ProjectPipeline{&GitlabPipeline{&gogitlab.PipelineInfo{ID: 1}}}, nil).AnyTimes()
	testSuite.MockGitClient.EXPECT().SetCommitStatus(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("could not commit status")).AnyTimes()
	testSuite.InitTestSuite()
	r := &RunStatusUpdater{
		cfg:    config.C,
		tfc:    testSuite.MockApiClient,
		client: testSuite.MockGitClient,
		rs:     testSuite.MockStreamClient,
	}
	r.updateCommitStatusForRun(context.Background(), &tfe.Run{
		Status:      tfe.RunPlannedAndFinished,
		HasChanges:  false,
		TargetAddrs: []string{"module.foo"},
	}, &runstream.TFRunMetadata{
		Action:    "apply",
		AutoMerge: true,
	})
}

func TestAutoMergeApply(t *testing.T) {

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	testSuite.MockStreamClient.EXPECT().RecordAutoMergeSuccess(gomock.Any()).Return(true, nil)
	expectAutoMergeIntentComment(testSuite)
	testSuite.MockGitClient.EXPECT().MergeMRAtSHA(gomock.Any(), 101, "zapier/tfbuddy", "commit-123")
	testSuite.MockGitClient.EXPECT().GetPipelinesForCommit(gomock.Any(), gomock.Any(), gomock.Any()).Return([]vcs.ProjectPipeline{&GitlabPipeline{&gogitlab.PipelineInfo{ID: 1}}}, nil).AnyTimes()
	testSuite.MockGitClient.EXPECT().SetCommitStatus(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("could not commit status")).AnyTimes()
	testSuite.InitTestSuite()
	r := &RunStatusUpdater{
		cfg:    config.C,
		tfc:    testSuite.MockApiClient,
		client: testSuite.MockGitClient,
		rs:     testSuite.MockStreamClient,
	}
	r.updateCommitStatusForRun(context.Background(), &tfe.Run{
		Status:     tfe.RunApplied,
		HasChanges: true,
	}, testAutoMergeRunMetadata())
}

func TestAutoMergeTargetedApply(t *testing.T) {

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	testSuite.MockGitClient.EXPECT().GetPipelinesForCommit(gomock.Any(), gomock.Any(), gomock.Any()).Return([]vcs.ProjectPipeline{&GitlabPipeline{&gogitlab.PipelineInfo{ID: 1}}}, nil).AnyTimes()
	testSuite.MockGitClient.EXPECT().SetCommitStatus(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("could not commit status")).AnyTimes()
	testSuite.InitTestSuite()
	r := &RunStatusUpdater{
		cfg:    config.C,
		tfc:    testSuite.MockApiClient,
		client: testSuite.MockGitClient,
		rs:     testSuite.MockStreamClient,
	}
	r.updateCommitStatusForRun(context.Background(), &tfe.Run{
		Status:      tfe.RunApplied,
		HasChanges:  true,
		TargetAddrs: []string{"module.foo"},
	}, &runstream.TFRunMetadata{
		Action:    "apply",
		AutoMerge: true,
	})
}

func TestAutoMergeWaitsForAggregateClaim(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	gomock.InOrder(
		testSuite.MockStreamClient.EXPECT().RecordAutoMergeSuccess(gomock.Any()).Return(false, nil),
		testSuite.MockStreamClient.EXPECT().RecordAutoMergeSuccess(gomock.Any()).Return(true, nil),
		expectAutoMergeIntentComment(testSuite),
		testSuite.MockGitClient.EXPECT().MergeMRAtSHA(gomock.Any(), 101, "zapier/tfbuddy", "commit-123").Return(nil),
		testSuite.MockStreamClient.EXPECT().RecordAutoMergeRequested(gomock.Any()).Return(nil),
	)

	r := &RunStatusUpdater{cfg: config.Config{AllowAutoMerge: true}, client: testSuite.MockGitClient, rs: testSuite.MockStreamClient}
	r.mergeMRIfPossible(context.Background(), testAutoMergeRunMetadata())
	r.mergeMRIfPossible(context.Background(), testAutoMergeRunMetadata())
}

func TestAutoMergeReleasesClaimWhenGitLabMergeFails(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)
	ref := runstream.AutoMergeRefForRun(testAutoMergeRunMetadata())

	testSuite.MockStreamClient.EXPECT().RecordAutoMergeSuccess(ref).Return(true, nil)
	expectAutoMergeIntentComment(testSuite)
	testSuite.MockGitClient.EXPECT().
		MergeMRAtSHA(gomock.Any(), 101, "zapier/tfbuddy", "commit-123").
		Return(errors.New("merge failed"))
	testSuite.MockStreamClient.EXPECT().ReleaseAutoMergeClaim(ref).Return(nil)

	r := &RunStatusUpdater{cfg: config.Config{AllowAutoMerge: true}, client: testSuite.MockGitClient, rs: testSuite.MockStreamClient}
	r.mergeMRIfPossible(context.Background(), testAutoMergeRunMetadata())
}

func TestAutoMergePermanentGitLabFailureIsNotRedelivered(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	testSuite.MockStreamClient.EXPECT().RecordAutoMergeSuccess(gomock.Any()).Return(true, nil)
	expectAutoMergeIntentComment(testSuite)
	testSuite.MockGitClient.EXPECT().
		MergeMRAtSHA(gomock.Any(), 101, "zapier/tfbuddy", "commit-123").
		Return(utils.CreatePermanentError(errors.New(
			"PUT https://gitlab.com/api/v4/projects/zapier%2Ftfbuddy/merge_requests/101/merge: " +
				"401 {message: 401 Unauthorized}",
		)))
	expectAutoMergeFailureComment(testSuite, "401 {message: 401 Unauthorized}")

	r := &RunStatusUpdater{cfg: config.Config{AllowAutoMerge: true}, client: testSuite.MockGitClient, rs: testSuite.MockStreamClient}
	if err := r.mergeMRIfPossible(context.Background(), testAutoMergeRunMetadata()); err != nil {
		t.Fatalf("permanent merge rejection should be acknowledged, got %v", err)
	}
}

func TestAutoMergeReleaseFailureDoesNotStormGitLab(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	testSuite.MockStreamClient.EXPECT().RecordAutoMergeSuccess(gomock.Any()).Return(true, nil)
	expectAutoMergeIntentComment(testSuite)
	testSuite.MockGitClient.EXPECT().
		MergeMRAtSHA(gomock.Any(), 101, "zapier/tfbuddy", "commit-123").
		Return(errors.New("GitLab unavailable"))
	testSuite.MockStreamClient.EXPECT().
		ReleaseAutoMergeClaim(gomock.Any()).
		Return(errors.New("NATS unavailable"))
	expectAutoMergeFailureComment(testSuite, "GitLab unavailable")

	r := &RunStatusUpdater{cfg: config.Config{AllowAutoMerge: true}, client: testSuite.MockGitClient, rs: testSuite.MockStreamClient}
	if err := r.mergeMRIfPossible(context.Background(), testAutoMergeRunMetadata()); err != nil {
		t.Fatalf("unreleasable claim should be acknowledged to avoid a retry storm, got %v", err)
	}
}

func TestAutoMergeFailureCreatesFallbackWhenIntentUpdateFails(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	testSuite.MockStreamClient.EXPECT().RecordAutoMergeSuccess(gomock.Any()).Return(true, nil)
	expectAutoMergeIntentComment(testSuite)
	testSuite.MockGitClient.EXPECT().
		MergeMRAtSHA(gomock.Any(), 101, "zapier/tfbuddy", "commit-123").
		Return(utils.CreatePermanentError(errors.New("merge rejected")))
	testSuite.MockGitClient.EXPECT().
		GetAuthenticatedAccountName(gomock.Any()).
		Return("tfbuddy-localdev", nil)
	testSuite.MockGitClient.EXPECT().
		UpdateMergeRequestComment(
			gomock.Any(),
			101,
			testAutoMergeIntentCommentID,
			"zapier/tfbuddy",
			autoMergeFailureComment("merge rejected"),
		).
		Return(errors.New("update failed"))
	testSuite.MockGitClient.EXPECT().
		CreateMergeRequestComment(
			gomock.Any(),
			101,
			"zapier/tfbuddy",
			autoMergeFailureComment("merge rejected"),
		).
		Return(nil)

	r := &RunStatusUpdater{cfg: config.Config{AllowAutoMerge: true}, client: testSuite.MockGitClient, rs: testSuite.MockStreamClient}
	if err := r.mergeMRIfPossible(context.Background(), testAutoMergeRunMetadata()); err != nil {
		t.Fatalf("permanent merge rejection should be acknowledged, got %v", err)
	}
}

func TestAutoMergeFailureCreatesCommentWhenIntentCommentFailed(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	testSuite.MockStreamClient.EXPECT().RecordAutoMergeSuccess(gomock.Any()).Return(true, nil)
	testSuite.MockGitClient.EXPECT().
		CreateMergeRequestCommentWithID(
			gomock.Any(),
			101,
			"zapier/tfbuddy",
			"All expected workspaces have been applied successfully. Auto-merging this MR.",
		).
		Return(int64(0), errors.New("intent comment failed"))
	testSuite.MockGitClient.EXPECT().
		MergeMRAtSHA(gomock.Any(), 101, "zapier/tfbuddy", "commit-123").
		Return(utils.CreatePermanentError(errors.New("merge rejected")))
	testSuite.MockGitClient.EXPECT().
		GetAuthenticatedAccountName(gomock.Any()).
		Return("tfbuddy-localdev", nil)
	testSuite.MockGitClient.EXPECT().
		CreateMergeRequestComment(
			gomock.Any(),
			101,
			"zapier/tfbuddy",
			autoMergeFailureComment("merge rejected"),
		).
		Return(nil)

	r := &RunStatusUpdater{cfg: config.Config{AllowAutoMerge: true}, client: testSuite.MockGitClient, rs: testSuite.MockStreamClient}
	if err := r.mergeMRIfPossible(context.Background(), testAutoMergeRunMetadata()); err != nil {
		t.Fatalf("permanent merge rejection should be acknowledged, got %v", err)
	}
}

func TestAutoMergeFailureReason(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "GitLab structured HTTP error",
			err: utils.CreatePermanentError(errors.New(
				"PUT https://gitlab.com/api/v4/projects/zapier%2Ftfbuddy/merge_requests/101/merge: " +
					"401 {message: 401 Unauthorized}",
			)),
			want: "401 {message: 401 Unauthorized}",
		},
		{
			name: "GitLab unstructured HTTP error",
			err: errors.New(
				"PUT https://gitlab.com/api/v4/projects/zapier%2Ftfbuddy/merge_requests/101/merge: " +
					"401 Unauthorized",
			),
			want: "401 Unauthorized",
		},
		{
			name: "non-HTTP error",
			err:  errors.New("GitLab unavailable"),
			want: "GitLab unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := autoMergeFailureReason(tt.err); got != tt.want {
				t.Fatalf("autoMergeFailureReason() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAutoMergeWithoutAggregateStateDoesNotMerge(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	testSuite.MockStreamClient.EXPECT().
		RecordAutoMergeSuccess(gomock.Any()).
		Return(false, runstream.ErrAutoMergeStateNotFound)

	r := &RunStatusUpdater{cfg: config.Config{AllowAutoMerge: true}, client: testSuite.MockGitClient, rs: testSuite.MockStreamClient}
	r.mergeMRIfPossible(context.Background(), testAutoMergeRunMetadata())
}

func TestAutoMergeHonorsCurrentGlobalKillSwitch(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	r := &RunStatusUpdater{cfg: config.Config{AllowAutoMerge: false}, client: testSuite.MockGitClient, rs: testSuite.MockStreamClient}
	if err := r.mergeMRIfPossible(context.Background(), testAutoMergeRunMetadata()); err != nil {
		t.Fatal(err)
	}
}

func TestAutoMergeStateErrorIsReturnedForRedelivery(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)
	wantErr := errors.New("NATS unavailable")

	testSuite.MockStreamClient.EXPECT().
		RecordAutoMergeSuccess(gomock.Any()).
		Return(false, wantErr)

	r := &RunStatusUpdater{cfg: config.Config{AllowAutoMerge: true}, client: testSuite.MockGitClient, rs: testSuite.MockStreamClient}
	if err := r.mergeMRIfPossible(context.Background(), testAutoMergeRunMetadata()); !errors.Is(err, wantErr) {
		t.Fatalf("expected state error to be returned, got %v", err)
	}
}

func TestPolicySoftFailPlanFailsPipelineWhenEnvTrue(t *testing.T) {
	t.Setenv("TFBUDDY_FAIL_CI_ON_SENTINEL_SOFT_FAIL", "true")
	config.Reload()

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	// Expect a failed plan status to be set due to policy soft fail
	var got vcs.WorkspaceStatus
	testSuite.MockGitClient.EXPECT().
		SetWorkspaceStatus(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s vcs.WorkspaceStatus) error {
			got = s
			return errors.New("could not commit status")
		}).Times(1)
	t.Cleanup(func() {
		if got.State != vcs.CommitStateFailed {
			t.Errorf("State = %q, want failed", got.State)
		}
		if got.Action != "plan" {
			t.Errorf("Action = %q, want plan", got.Action)
		}
	})

	r := &RunStatusUpdater{
		cfg:    config.C,
		tfc:    testSuite.MockApiClient,
		client: testSuite.MockGitClient,
		rs:     testSuite.MockStreamClient,
	}

	r.updateCommitStatusForRun(context.Background(), &tfe.Run{
		Status: tfe.RunPolicySoftFailed,
	}, &runstream.TFRunMetadata{
		Action: "plan",
		// Set minimal metadata; not strictly required for assertion
		Workspace: "service-tfbuddy",
		RunID:     "run-123",
	})

	// Clean up env var for safety (though t.Setenv handles this)
	os.Unsetenv("TFBUDDY_FAIL_CI_ON_SENTINEL_SOFT_FAIL")
	config.Reload()
}
