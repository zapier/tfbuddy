package gitlab

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/hashicorp/go-tfe"
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

func TestAutoMergeNoChangesApply(t *testing.T) {

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{}, t)

	testSuite.MockGitClient.EXPECT().MergeMR(gomock.Any(), gomock.Any(), gomock.Any())
	testSuite.MockGitClient.EXPECT().GetPipelinesForCommit(gomock.Any(), gomock.Any(), gomock.Any()).Return([]vcs.ProjectPipeline{&GitlabPipeline{&gogitlab.PipelineInfo{ID: 1}}}, nil).AnyTimes()
	testSuite.MockGitClient.EXPECT().SetCommitStatus(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("could not commit status")).AnyTimes()
	testSuite.InitTestSuite()
	r := &RunStatusUpdater{
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
		Action:    "apply",
		AutoMerge: true,
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
}
