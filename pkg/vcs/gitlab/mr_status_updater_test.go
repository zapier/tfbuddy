package gitlab

import (
	"context"
	"errors"
	"testing"

	"github.com/hashicorp/go-tfe"
	gogitlab "github.com/xanzy/go-gitlab"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.uber.org/mock/gomock"
)

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

	testSuite.MockStreamClient.EXPECT().GetWorkspaceMeta(gomock.Any(), gomock.Any()).Return(&runstream.TFCWorkspacesMetadata{
		CountExecutedWorkspaces: 0,
		CountTotalWorkspaces:    1,
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
