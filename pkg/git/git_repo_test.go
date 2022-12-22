package git

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zapier/tfbuddy/pkg/mocks"
)

func TestGetMergeBase(t *testing.T) {
	mrBranch := "test"
	gitRepo, initialCommit := mocks.InitGitTestRepo(t)
	err := gitRepo.SwitchToBranch(mrBranch)
	assert.Equal(t, nil, err)
	_, err = gitRepo.CreateCommitFileOnCurrentBranch("main2.tf", "test commit")
	assert.Equal(t, nil, err)

	client := Repository{
		Repository: gitRepo.Repo,
	}
	common, err := client.GetMergeBase(mrBranch, "master")
	assert.Equal(t, nil, err)
	assert.Equal(t, common, initialCommit)

}

func TestGetModifiedFileNamesBetweenCommits(t *testing.T) {
	mrBranch := "test"
	gitRepo, _ := mocks.InitGitTestRepo(t)
	err := gitRepo.SwitchToBranch(mrBranch)
	assert.Equal(t, nil, err)
	_, err = gitRepo.CreateCommitFileOnCurrentBranch("main2.tf", "test commit")
	assert.Equal(t, nil, err)

	client := Repository{
		Repository: gitRepo.Repo,
	}
	commonCommit, err := client.GetMergeBase(mrBranch, "master")
	assert.Equal(t, nil, err)
	err = gitRepo.SwitchToBranch("master")
	assert.Equal(t, nil, err)
	_, err = gitRepo.CreateCommitFileOnCurrentBranch("some.tf", "commit on target branch")
	assert.Equal(t, nil, err)

	modifiedFiles, err := client.GetModifiedFileNamesBetweenCommits(commonCommit, "master")
	assert.Equal(t, nil, err)
	assert.Equal(t, len(modifiedFiles), 1, "expected a single file to be modified between master & test")
	assert.Equal(t, modifiedFiles[0], "some.tf")
}

func TestGetModifiedFileNamesBetweenCommitsNewDir(t *testing.T) {
	mrBranch := "test"
	gitRepo, _ := mocks.InitGitTestRepo(t)
	err := gitRepo.SwitchToBranch(mrBranch)
	assert.Equal(t, nil, err)
	_, err = gitRepo.CreateCommitFileOnCurrentBranch("staging/main2.tf", "test commit")
	assert.Equal(t, nil, err)

	client := Repository{
		Repository: gitRepo.Repo,
	}
	commonCommit, err := client.GetMergeBase(mrBranch, "master")
	assert.Equal(t, nil, err)
	err = gitRepo.SwitchToBranch("master")
	assert.Equal(t, nil, err)
	_, err = gitRepo.CreateCommitFileOnCurrentBranch("some.tf", "commit on target branch")
	assert.Equal(t, nil, err)

	modifiedFiles, err := client.GetModifiedFileNamesBetweenCommits(commonCommit, "master")
	assert.Equal(t, nil, err)
	assert.Equal(t, len(modifiedFiles), 1, "expected a single file to be modified between master & test")
	assert.Equal(t, modifiedFiles[0], "some.tf")
}

func TestGetModifiedFileNamesBetweenCommitsNoResults(t *testing.T) {
	mrBranch := "test"
	gitRepo, _ := mocks.InitGitTestRepo(t)
	err := gitRepo.SwitchToBranch(mrBranch)
	assert.Equal(t, nil, err)
	_, err = gitRepo.CreateCommitFileOnCurrentBranch("main2.tf", "test commit")
	assert.Equal(t, nil, err)

	client := Repository{
		Repository: gitRepo.Repo,
	}
	commonCommit, err := client.GetMergeBase(mrBranch, "master")
	assert.Equal(t, nil, err)
	err = gitRepo.SwitchToBranch("master")
	assert.Equal(t, nil, err)

	modifiedFiles, err := client.GetModifiedFileNamesBetweenCommits(commonCommit, "master")
	assert.Equal(t, nil, err)
	assert.Equal(t, len(modifiedFiles), 0, "expected no files modified between master and test")
}
