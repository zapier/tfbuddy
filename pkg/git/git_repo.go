package git

import (
	"fmt"
	"io/fs"
	"math"
	"os"
	"strconv"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/zapier/tfbuddy/pkg/utils"
)

type Repository struct {
	*git.Repository
	authentication *githttp.BasicAuth
	localDir       string
}

func NewRepository(repo *git.Repository, auth *githttp.BasicAuth, localDir string) *Repository {
	return &Repository{
		authentication: auth,
		localDir:       localDir,
		Repository:     repo,
	}
}

func (gr *Repository) GetLocalDirectory() string {
	return gr.localDir
}

func (gr *Repository) FetchUpstreamBranch(branch string) error {
	ref := fmt.Sprintf("refs/heads/%s:refs/heads/%s", branch, branch)
	err := gr.Fetch(&git.FetchOptions{
		RefSpecs: []config.RefSpec{config.RefSpec(ref)},
		Auth:     gr.authentication,
	})
	if err != nil && err.Error() != git.NoErrAlreadyUpToDate.Error() {
		return utils.CreatePermanentError(err)
	}
	return nil
}

func (gr *Repository) GetMergeBase(oldest, newest string) (string, error) {
	var hashes []*plumbing.Hash
	for _, rev := range []string{oldest, newest} {
		hash, err := gr.ResolveRevision(plumbing.Revision(rev))
		if err != nil {
			return "", utils.CreatePermanentError(err)
		}
		hashes = append(hashes, hash)
	}

	var commits []*object.Commit
	for _, hash := range hashes {
		commit, err := gr.CommitObject(*hash)
		if err != nil {
			return "", utils.CreatePermanentError(err)
		}
		commits = append(commits, commit)
	}

	res, err := commits[0].MergeBase(commits[1])
	if err != nil {
		return "", utils.CreatePermanentError(err)
	}
	if len(res) > 0 {
		println(res)
		return res[0].Hash.String(), nil

	}

	return "", utils.CreatePermanentError(fmt.Errorf("could not find merge base"))
}
func (gr *Repository) GetModifiedFileNamesBetweenCommits(oldest, newest string) ([]string, error) {
	oldestSha, err := gr.ResolveRevision(plumbing.Revision(oldest))
	if err != nil {
		return nil, utils.CreatePermanentError(err)
	}

	newestSha, err := gr.ResolveRevision(plumbing.Revision(newest))
	if err != nil {
		return nil, utils.CreatePermanentError(err)
	}

	oldestCommit, err := gr.CommitObject(*oldestSha)
	if err != nil {
		return nil, utils.CreatePermanentError(err)
	}

	newestCommit, err := gr.CommitObject(*newestSha)
	if err != nil {
		return nil, utils.CreatePermanentError(err)
	}

	patch, err := oldestCommit.Patch(newestCommit)
	if err != nil {
		return nil, utils.CreatePermanentError(err)
	}

	filePatches := patch.FilePatches()
	if len(filePatches) == 0 {
		return []string{}, nil
	}

	output := make([]string, 0, len(filePatches))
	for _, file := range filePatches {
		f, t := file.Files()

		if t != nil && f != nil && f.Path() == t.Path() {
			output = append(output, f.Path())
			continue
		}
		if f != nil {
			output = append(output, f.Path())
		}
		if t != nil {
			output = append(output, t.Path())
		}
	}

	return output, nil
}

func WalkRepo(s string, d fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	if !d.IsDir() {
		println(s)
	}
	return nil
}

// GetCloneDepth reads the provided env var and returns an int to be used as git clone depth. Default is 0.
func GetCloneDepth(envVar string) (int, error) {
	val := os.Getenv(envVar)
	if val == "" {
		return 0, nil
	}

	depth, err := strconv.Atoi(val)
	if err != nil {
		return 0, fmt.Errorf("invalid %s value '%s': must be a valid integer", envVar, val)
	}

	if depth < 0 {
		return 0, fmt.Errorf("invalid %s value %d: clone depth cannot be negative", envVar, depth)
	}

	if depth > math.MaxInt32 {
		return 0, fmt.Errorf("invalid %s value %d: clone depth cannot exceed %d", envVar, depth, math.MaxInt32)
	}

	return depth, nil
}
