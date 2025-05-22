package git

import (
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"testing"

	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
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

func TestGetCloneDepth(t *testing.T) {
	const testEnvVar = "TEST_GIT_CLONE_DEPTH"

	tests := []struct {
		name        string
		envVal      string
		wantDepth   int
		wantErr     bool
		errContains string
	}{
		{
			name:      "no value",
			envVal:    "",
			wantDepth: 0,
			wantErr:   false,
		},
		{
			name:      "valid depth 500",
			envVal:    "500",
			wantDepth: 500,
			wantErr:   false,
		},
		{
			name:        "invalid string",
			envVal:      "somestuff",
			wantDepth:   0,
			wantErr:     true,
			errContains: "must be a valid integer",
		},
		{
			name:      "zero value",
			envVal:    "0",
			wantDepth: 0,
			wantErr:   false,
		},
		{
			name:        "negative value",
			envVal:      "-10",
			wantDepth:   0,
			wantErr:     true,
			errContains: "cannot be negative",
		},
		{
			name:      "large valid value",
			envVal:    "999999",
			wantDepth: 999999,
			wantErr:   false,
		},
		{
			name:      "max int32 value",
			envVal:    "2147483647",
			wantDepth: math.MaxInt32,
			wantErr:   false,
		},
		{
			name:        "over max value",
			envVal:      "2147483648",
			wantDepth:   0,
			wantErr:     true,
			errContains: "cannot exceed",
		},
		{
			name:        "very large value",
			envVal:      "999999999999",
			wantDepth:   0,
			wantErr:     true,
			errContains: "cannot exceed",
		},
		{
			name:      "minimum shallow clone",
			envVal:    "1",
			wantDepth: 1,
			wantErr:   false,
		},
		{
			name:        "float value",
			envVal:      "10.5",
			wantDepth:   0,
			wantErr:     true,
			errContains: "must be a valid integer",
		},
		{
			name:        "hex value",
			envVal:      "0x10",
			wantDepth:   0,
			wantErr:     true,
			errContains: "must be a valid integer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(testEnvVar, tt.envVal)

			gotDepth, gotErr := GetCloneDepth(testEnvVar)

			if tt.wantErr {
				if gotErr == nil {
					t.Errorf("GetCloneDepth() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if tt.errContains != "" {
					assert.Contains(t, gotErr.Error(), tt.errContains)
				}
			} else {
				if gotErr != nil {
					t.Errorf("GetCloneDepth() error = %v, wantErr %v", gotErr, tt.wantErr)
					return
				}
			}

			if gotDepth != tt.wantDepth {
				t.Errorf("GetCloneDepth() = %v, want %v", gotDepth, tt.wantDepth)
			}
		})
	}
}

func TestNewRepository(t *testing.T) {
	gitRepo, _ := mocks.InitGitTestRepo(t)
	auth := &githttp.BasicAuth{
		Username: "test",
		Password: "token",
	}
	localDir := "/tmp/test"

	repo := NewRepository(gitRepo.Repo, auth, localDir)

	assert.NotNil(t, repo)
	assert.Equal(t, auth, repo.authentication)
	assert.Equal(t, localDir, repo.localDir)
	assert.Equal(t, gitRepo.Repo, repo.Repository)
}

func TestGetLocalDirectory(t *testing.T) {
	gitRepo, _ := mocks.InitGitTestRepo(t)
	localDir := "/tmp/test"
	repo := NewRepository(gitRepo.Repo, nil, localDir)

	assert.Equal(t, localDir, repo.GetLocalDirectory())
}

func TestFetchUpstreamBranch(t *testing.T) {
	gitRepo, _ := mocks.InitGitTestRepo(t)
	repo := NewRepository(gitRepo.Repo, nil, "")

	err := repo.FetchUpstreamBranch("master")
	assert.NotNil(t, err, "should fail when no remote is configured")
}

func TestGetMergeBase_ErrorCases(t *testing.T) {
	gitRepo, _ := mocks.InitGitTestRepo(t)
	client := Repository{
		Repository: gitRepo.Repo,
	}

	t.Run("invalid_oldest_branch", func(t *testing.T) {
		_, err := client.GetMergeBase("nonexistent", "master")
		assert.NotNil(t, err)
	})

	t.Run("invalid_newest_branch", func(t *testing.T) {
		_, err := client.GetMergeBase("master", "nonexistent")
		assert.NotNil(t, err)
	})
}

func TestGetModifiedFileNamesBetweenCommits_ErrorCases(t *testing.T) {
	gitRepo, _ := mocks.InitGitTestRepo(t)
	client := Repository{
		Repository: gitRepo.Repo,
	}

	t.Run("invalid_oldest_commit", func(t *testing.T) {
		_, err := client.GetModifiedFileNamesBetweenCommits("invalidhash", "master")
		assert.NotNil(t, err)
	})

	t.Run("invalid_newest_commit", func(t *testing.T) {
		_, err := client.GetModifiedFileNamesBetweenCommits("master", "invalidhash")
		assert.NotNil(t, err)
	})
}

func TestGetModifiedFileNamesBetweenCommits_FileOperations(t *testing.T) {
	mrBranch := "test"
	gitRepo, _ := mocks.InitGitTestRepo(t)
	err := gitRepo.SwitchToBranch(mrBranch)
	assert.Equal(t, nil, err)

	commit1Hash, err := gitRepo.CreateCommitFileOnCurrentBranch("file1.tf", "content1")
	assert.Equal(t, nil, err)

	commit2Hash, err := gitRepo.CreateCommitFileOnCurrentBranch("file2.tf", "content2")
	assert.Equal(t, nil, err)

	commit3Hash, err := gitRepo.CreateCommitFileOnCurrentBranch("file3.tf", "content3")
	assert.Equal(t, nil, err)

	client := Repository{
		Repository: gitRepo.Repo,
	}

	t.Run("added_files", func(t *testing.T) {
		modifiedFiles, err := client.GetModifiedFileNamesBetweenCommits(commit1Hash, commit2Hash)
		assert.Equal(t, nil, err)
		assert.Contains(t, modifiedFiles, "file2.tf")
	})

	t.Run("multiple_files", func(t *testing.T) {
		modifiedFiles, err := client.GetModifiedFileNamesBetweenCommits(commit2Hash, commit3Hash)
		assert.Equal(t, nil, err)
		assert.Contains(t, modifiedFiles, "file3.tf")
	})
}

func TestWalkRepo(t *testing.T) {
	tempDir := t.TempDir()

	testFile := filepath.Join(tempDir, "test.txt")
	err := os.WriteFile(testFile, []byte("test content"), 0644)
	assert.NoError(t, err)

	subDir := filepath.Join(tempDir, "subdir")
	err = os.Mkdir(subDir, 0755)
	assert.NoError(t, err)

	subFile := filepath.Join(subDir, "subfile.txt")
	err = os.WriteFile(subFile, []byte("sub content"), 0644)
	assert.NoError(t, err)

	t.Run("walk_file", func(t *testing.T) {
		info, err := os.Stat(testFile)
		assert.NoError(t, err)
		dirEntry := fs.FileInfoToDirEntry(info)

		err = WalkRepo(testFile, dirEntry, nil)
		assert.NoError(t, err)
	})

	t.Run("walk_directory", func(t *testing.T) {
		info, err := os.Stat(subDir)
		assert.NoError(t, err)
		dirEntry := fs.FileInfoToDirEntry(info)

		err = WalkRepo(subDir, dirEntry, nil)
		assert.NoError(t, err)
	})

	t.Run("walk_with_error", func(t *testing.T) {
		info, err := os.Stat(testFile)
		assert.NoError(t, err)
		dirEntry := fs.FileInfoToDirEntry(info)

		testErr := assert.AnError
		err = WalkRepo(testFile, dirEntry, testErr)
		assert.Equal(t, testErr, err)
	})
}
