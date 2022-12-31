package gitlab

import (
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/sideband"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/rs/zerolog/log"
	"github.com/xanzy/go-gitlab"
	zgit "github.com/zapier/tfbuddy/pkg/git"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"gopkg.in/errgo.v2/fmt/errors"
)

const GITLAB_CLONE_DEPTH_ENV = "TFBUDDY_GITLAB_CLONE_DEPTH"

// CloneMergeRequest performs a git clone of the target Gitlab project & merge request branch to the `dest` path.
func (c *GitlabClient) CloneMergeRequest(project string, mr vcs.MR, dest string) (vcs.GitRepo, error) {
	proj, _, err := c.client.Projects.GetProject(project, &gitlab.GetProjectOptions{
		License:              gitlab.Bool(false),
		Statistics:           gitlab.Bool(false),
		WithCustomAttributes: gitlab.Bool(false),
	})
	if err != nil {
		return nil, errors.Newf("could not clone MR - unable to read project details from Gitlab API: %v", err)
	}

	ref := plumbing.NewBranchReferenceName(mr.GetSourceBranch())
	auth := &githttp.BasicAuth{
		Username: c.tokenUser,
		Password: c.token,
	}

	var progress sideband.Progress
	if log.Trace().Enabled() {
		progress = os.Stdout
	}
	cloneDepth := zgit.GetCloneDepth(GITLAB_CLONE_DEPTH_ENV)

	repo, err := git.PlainClone(dest, false, &git.CloneOptions{
		Auth:          auth,
		URL:           proj.HTTPURLToRepo,
		ReferenceName: ref,
		SingleBranch:  true,
		Depth:         cloneDepth,
		Progress:      progress,
	})

	if err != nil && err != git.ErrRepositoryAlreadyExists {
		return nil, errors.Newf("could not clone MR: %v", err)
	}

	wt, _ := repo.Worktree()
	err = wt.Pull(&git.PullOptions{
		//RemoteName:        "",
		ReferenceName: ref,
		//SingleBranch:      false,
		Depth: cloneDepth,
		Auth:  auth,
		//RecurseSubmodules: 0,
		Progress: progress,
		Force:    false,
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return nil, errors.Newf("could not pull MR: %v", err)
	}

	if log.Trace().Enabled() {
		// print contents of repo

		//nolint
		filepath.WalkDir(dest, zgit.WalkRepo)
	}
	return zgit.NewRepository(repo, auth, dest), nil

}
