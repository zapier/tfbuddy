package gitlab

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/hashicorp/go-tfe"
	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/vcs"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
	"go.uber.org/mock/gomock"
)

type commitStatusStateMatcher struct {
	expectedState string
}

func (m *commitStatusStateMatcher) Matches(x interface{}) bool {
	opts, ok := x.(vcs.CommitStatusOptions)
	if !ok {
		return false
	}
	return opts.GetState() == m.expectedState
}

func (m *commitStatusStateMatcher) String() string {
	return "matches commit status with state=" + m.expectedState
}

// pipelineIDMatcher asserts that the commit status options carry the expected pipeline ID.
type pipelineIDMatcher struct {
	expectedID int
}

func (m *pipelineIDMatcher) Matches(x interface{}) bool {
	opts, ok := x.(*GitlabCommitStatusOptions)
	if !ok {
		return false
	}
	if opts.PipelineID == nil {
		return false
	}
	return *opts.PipelineID == m.expectedID
}

func (m *pipelineIDMatcher) String() string {
	return "matches commit status whose PipelineID is set to the expected value"
}

// TestUpdateStatusAttachesPipelineID guards against a regression of the closure
// shadowing bug where the resolved pipeline ID was discarded and SetCommitStatus
// was called with PipelineID == nil. Without an attached pipeline ID, GitLab
// can't associate the status with the current MR pipeline, leaving the "apply"
// check stuck and pipeline-status links pointing at stale runs.
func TestUpdateStatusAttachesPipelineID(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	const expectedPipelineID = 42
	testSuite.MockGitClient.EXPECT().
		GetPipelinesForCommit(gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]vcs.ProjectPipeline{&GitlabPipeline{&gogitlab.PipelineInfo{ID: expectedPipelineID, Source: "merge_request_event"}}}, nil).
		AnyTimes()

	testSuite.MockGitClient.EXPECT().
		SetCommitStatus(
			gomock.Any(),
			gomock.Any(),
			gomock.Any(),
			&pipelineIDMatcher{expectedID: expectedPipelineID},
		).
		Return(&GitlabCommitStatus{&gogitlab.CommitStatus{}}, nil).
		Times(1)

	testSuite.InitTestSuite()
	r := &RunStatusUpdater{
		cfg:    config.C,
		tfc:    testSuite.MockApiClient,
		client: testSuite.MockGitClient,
		rs:     testSuite.MockStreamClient,
	}

	r.updateStatus(context.Background(), gogitlab.Success, "apply", &runstream.TFRunMetadata{
		Action:    "apply",
		Workspace: "service-tfbuddy",
		RunID:     "run-123",
	})
}

func TestAutoMergeNoChangesApply(t *testing.T) {

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	testSuite.MockGitClient.EXPECT().MergeMR(gomock.Any(), gomock.Any(), gomock.Any())
	testSuite.MockGitClient.EXPECT().GetPipelinesForCommit(gomock.Any(), gomock.Any(), gomock.Any()).Return([]vcs.ProjectPipeline{&GitlabPipeline{&gogitlab.PipelineInfo{ID: 1}}}, nil).AnyTimes()
	testSuite.MockGitClient.EXPECT().SetCommitStatus(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("could not commit status")).AnyTimes()
	testSuite.MockStreamClient.EXPECT().GetWorkspaceMeta(gomock.Any(), gomock.Any()).Return(&runstream.TFCWorkspacesMetadata{
		CountExecutedWorkspaces: 0,
		CountTotalWorkspaces:    1,
	}, nil)
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
	}, &runstream.TFRunMetadata{
		Action:    "apply",
		AutoMerge: true,
	})
}
func TestAutoMergeTargetedNoChangesApply(t *testing.T) {

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	testSuite.MockGitClient.EXPECT().MergeMR(gomock.Any(), gomock.Any(), gomock.Any()).MaxTimes(0)

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

	testSuite.MockGitClient.EXPECT().MergeMR(gomock.Any(), gomock.Any(), gomock.Any())
	testSuite.MockGitClient.EXPECT().GetPipelinesForCommit(gomock.Any(), gomock.Any(), gomock.Any()).Return([]vcs.ProjectPipeline{&GitlabPipeline{&gogitlab.PipelineInfo{ID: 1}}}, nil).AnyTimes()
	testSuite.MockGitClient.EXPECT().SetCommitStatus(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("could not commit status")).AnyTimes()

	testSuite.MockStreamClient.EXPECT().GetWorkspaceMeta(gomock.Any(), gomock.Any()).Return(&runstream.TFCWorkspacesMetadata{
		CountExecutedWorkspaces: 0,
		CountTotalWorkspaces:    1,
	}, nil)

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
	}, &runstream.TFRunMetadata{
		Action:    "apply",
		AutoMerge: true,
	})
}

func TestAutoMergeApplyMultiWorkspace(t *testing.T) {

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	testSuite.MockGitClient.EXPECT().MergeMR(gomock.Any(), gomock.Any(), gomock.Any())
	testSuite.MockGitClient.EXPECT().GetPipelinesForCommit(gomock.Any(), gomock.Any(), gomock.Any()).Return([]vcs.ProjectPipeline{&GitlabPipeline{&gogitlab.PipelineInfo{ID: 1}}}, nil).AnyTimes()
	testSuite.MockGitClient.EXPECT().SetCommitStatus(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("could not commit status")).AnyTimes()

	//workspace 1 in same mr
	testSuite.MockStreamClient.EXPECT().GetWorkspaceMeta("101", "zapier/test").Return(&runstream.TFCWorkspacesMetadata{
		CountExecutedWorkspaces: 0,
		CountTotalWorkspaces:    2,
	}, nil)
	//workspace 2 in same mr
	testSuite.MockStreamClient.EXPECT().GetWorkspaceMeta("101", "zapier/test").Return(&runstream.TFCWorkspacesMetadata{
		CountExecutedWorkspaces: 1,
		CountTotalWorkspaces:    2,
	}, nil)

	testSuite.InitTestSuite()
	r := &RunStatusUpdater{
		tfc:    testSuite.MockApiClient,
		client: testSuite.MockGitClient,
		rs:     testSuite.MockStreamClient,
	}
	r.updateCommitStatusForRun(context.Background(), &tfe.Run{
		Status:     tfe.RunApplied,
		HasChanges: true,
	}, &runstream.TFRunMetadata{
		Action:                               "apply",
		AutoMerge:                            true,
		MergeRequestIID:                      101,
		MergeRequestProjectNameWithNamespace: "zapier/test",
	})

	r.updateCommitStatusForRun(context.Background(), &tfe.Run{
		Status:     tfe.RunApplied,
		HasChanges: true,
	}, &runstream.TFRunMetadata{
		Action:                               "apply",
		AutoMerge:                            true,
		MergeRequestIID:                      101,
		MergeRequestProjectNameWithNamespace: "zapier/test",
	})
}
func TestAutoMergeTargetedApply(t *testing.T) {

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	testSuite.MockGitClient.EXPECT().MergeMR(gomock.Any(), gomock.Any(), gomock.Any()).MaxTimes(0)

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

func TestPolicySoftFailPlanFailsPipelineWhenEnvTrue(t *testing.T) {
	t.Setenv("TFBUDDY_FAIL_CI_ON_SENTINEL_SOFT_FAIL", "true")
	config.Reload()

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	// Ensure we can fetch a pipeline ID without backoff delay
	testSuite.MockGitClient.EXPECT().
		GetPipelinesForCommit(gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]vcs.ProjectPipeline{&GitlabPipeline{&gogitlab.PipelineInfo{ID: 1}}}, nil).
		AnyTimes()

	// Expect a failed plan status to be set due to policy soft fail
	testSuite.MockGitClient.EXPECT().
		SetCommitStatus(
			gomock.Any(),
			gomock.Any(),
			gomock.Any(),
			&commitStatusStateMatcher{expectedState: string(gogitlab.Failed)},
		).
		Return(nil, errors.New("could not commit status")).
		Times(1)

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
