package github

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/sideband"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gogithub "github.com/google/go-github/v69/github"
	"github.com/rs/zerolog/log"
	zgit "github.com/zapier/tfbuddy/pkg/git"
	"github.com/zapier/tfbuddy/pkg/utils"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.opentelemetry.io/otel"
	"golang.org/x/oauth2"
)

// ensure type complies with interface
var _ vcs.GitClient = (*Client)(nil)

type Client struct {
	client *gogithub.Client
	ctx    context.Context
	token  string
}

const DefaultMaxRetries = 3

func createBackOffWithRetries() backoff.BackOff {
	exp := backoff.NewExponentialBackOff()
	exp.MaxElapsedTime = 30 * time.Second
	return backoff.WithMaxRetries(exp, DefaultMaxRetries)

}
func NewGithubClient() *Client {
	token := os.Getenv("GITHUB_TOKEN")
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)

	return &Client{
		client: gogithub.NewClient(tc),
		ctx:    ctx,
		token:  token,
	}
}

func (c *Client) GetMergeRequestApprovals(ctx context.Context, id int, project string) (vcs.MRApproved, error) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "GetMergeRequestApprovals")
	defer span.End()

	pr, err := c.GetPullRequest(ctx, project, id)
	if err != nil {
		return nil, err
	}
	return pr, nil
}
func (c *Client) MergeMR(ctx context.Context, mrIID int, project string) error {
	projectParts, err := splitFullName(project)
	if err != nil {
		return utils.CreatePermanentError(err)
	}
	return backoff.Retry(func() error {
		_, resp, err := c.client.PullRequests.Merge(c.ctx, projectParts[0], projectParts[1], mrIID, "", nil)
		return utils.CreatePermanentHTTPError(resp.StatusCode, err)
	}, createBackOffWithRetries())
}

// Go over all comments on a PR, trying to grab any old TFC run urls and deleting the bodies
func (c *Client) GetOldRunUrls(ctx context.Context, prID int, fullName string, rootCommentID int) (string, error) {
	_, span := otel.Tracer("TFC").Start(ctx, "GetOldRunURLs")
	defer span.End()

	log.Debug().Msg("pruneComments")
	projectParts, err := splitFullName(fullName)
	if err != nil {
		return "", utils.CreatePermanentError(err)
	}
	comments, err := backoff.RetryWithData(func() ([]*gogithub.IssueComment, error) {
		comments, resp, err := c.client.Issues.ListComments(c.ctx, projectParts[0], projectParts[1], prID, &gogithub.IssueListCommentsOptions{})
		return comments, utils.CreatePermanentHTTPError(resp.StatusCode, err)
	}, createBackOffWithRetries())
	if err != nil {
		return "", err
	}

	currentUser, err := backoff.RetryWithData(func() (*gogithub.User, error) {
		u, resp, err := c.client.Users.Get(context.Background(), "")
		return u, utils.CreatePermanentHTTPError(resp.StatusCode, err)
	}, createBackOffWithRetries())
	if err != nil {
		return "", err
	}

	var oldRunUrls []string
	var oldRunBlock string
	for _, comment := range comments {
		// If this token user made the comment, and we're making a new comment, pick the TFC url out of the body and delete the comment
		if comment.GetUser().GetLogin() == currentUser.GetLogin() {
			runUrl := utils.CaptureSubstring(comment.GetBody(), utils.URL_RUN_PREFIX, utils.URL_RUN_SUFFIX)
			// We scrape the run URLs from the previous MR comments.
			// Since they are hyperlinked in markdown format, we need to extract the URL
			// without the markdown artifacts.
			runUrlRaw := utils.CaptureSubstring(runUrl, "[", "]")
			runUrlSplit := strings.Split(runUrlRaw, "/")
			// The run ID is the last part of the run URL, and it looks like run-abcd12345...
			runID := ""
			if len(runUrlSplit) > 0 {
				runID = runUrlSplit[len(runUrlSplit)-1]
			} else {
				// If the URL split slice doesn't contain anything for any reason
				// We set the ID and URL to the run URL as a fallback (as it was originally scraped)
				// It'll appear like this in markdown
				// [https://app.terraform.io/...](https://app.terraform.io/...)
				log.Warn().Msg("Unable to obtain Terraform cloud run ID. The run URL(s) on the previous comments may be malformed.")
				runID = runUrl
				runUrlRaw = runUrl
			}
			runStatus := utils.CaptureSubstring(comment.GetBody(), utils.URL_RUN_STATUS_PREFIX, utils.URL_RUN_SUFFIX)
			if runUrl != "" && runStatus != "" {
				// Example: |[<tfc runID>](<tfc url>)|✅ Applied|2023-08-02 15:41:48.82 +0000 UTC|
				oldRunUrls = append(oldRunUrls, fmt.Sprintf("|[%s](%s)|%s|%s|", runID, runUrlRaw, runStatus, comment.CreatedAt))
			}

			// Github orders comments from earliest -> latest via ID, so we check each comment and take the last match on an "old url" block
			oldRunBlockTest := utils.CaptureSubstring(comment.GetBody(), utils.URL_RUN_GROUP_PREFIX, utils.URL_RUN_GROUP_SUFFIX)
			// Add a new line for the first table entry so that markdown tabling can properly begin
			oldRunBlock = "\n"
			if oldRunBlockTest != "" {
				oldRunBlock = oldRunBlockTest
			}

			if os.Getenv("TFBUDDY_DELETE_OLD_COMMENTS") != "" && comment.GetID() != int64(rootCommentID) {
				log.Debug().Msgf("Deleting comment %d", comment.GetID())
				if err := backoff.Retry(func() error {
					resp, err := c.client.Issues.DeleteComment(c.ctx, projectParts[0], projectParts[1], comment.GetID())
					return utils.CreatePermanentHTTPError(resp.StatusCode, err)
				}, createBackOffWithRetries()); err != nil {
					return "", err
				}
			}
		}
	}

	// If we found any old run urls, return them formatted
	if len(oldRunUrls) > 0 {
		// Try and find any exisitng groupings of old urls, else make a new one
		return fmt.Sprintf("%s%s%s\n%s", utils.URL_RUN_GROUP_PREFIX, oldRunBlock, strings.Join(oldRunUrls, "\n"), utils.URL_RUN_GROUP_SUFFIX), nil
	}
	return oldRunBlock, nil
}

func (c *Client) CreateMergeRequestComment(ctx context.Context, prID int, fullName string, comment string) error {
	ctx, span := otel.Tracer("TFC").Start(ctx, "CreateMergeRequestComment")
	defer span.End()

	_, err := c.PostIssueComment(ctx, prID, fullName, comment)
	return err
}

func (c *Client) CreateMergeRequestDiscussion(ctx context.Context, prID int, fullName string, comment string) (vcs.MRDiscussionNotes, error) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "CreateMergeRequestDiscussion")
	defer span.End()

	// GitHub doesn't support discussion threads AFAICT.
	iss, err := c.PostIssueComment(ctx, prID, fullName, comment)
	return &GithubPRIssueComment{iss}, err
}

func (c *Client) GetMergeRequest(ctx context.Context, prID int, fullName string) (vcs.DetailedMR, error) {
	ctx, span := otel.Tracer("hooks").Start(ctx, "GetMergeRequest")
	defer span.End()

	pr, err := c.GetPullRequest(ctx, fullName, prID)
	if err != nil {
		return nil, err
	}
	return pr, nil
}

func (c *Client) GetRepoFile(ctx context.Context, fullName string, file string, ref string) ([]byte, error) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "GetRepoFile")
	defer span.End()

	if ref == "" {
		ref = "HEAD"
	}
	parts, err := splitFullName(fullName)
	if err != nil {
		return nil, err
	}
	return backoff.RetryWithData(func() ([]byte, error) {
		fileContent, _, resp, err := c.client.Repositories.GetContents(c.ctx, parts[0], parts[1], file, &gogithub.RepositoryContentGetOptions{Ref: ref})
		if err != nil {
			return nil, utils.CreatePermanentHTTPError(resp.StatusCode, err)
		}

		contents, err := fileContent.GetContent()
		if err != nil {
			return nil, utils.CreatePermanentError(err)
		}

		return []byte(contents), nil
	}, createBackOffWithRetries())
}

func (c *Client) GetMergeRequestModifiedFiles(ctx context.Context, prID int, fullName string) ([]string, error) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "GetMergeRequestModifiedFiles")
	defer span.End()

	pr, err := c.GetPullRequest(ctx, fullName, prID)
	if err != nil {
		return nil, err
	}

	opts := gogithub.ListOptions{
		PerPage: 300,
	}

	if pr.GetChangedFiles() > 0 {
		return backoff.RetryWithData(func() ([]string, error) {
			parts, err := splitFullName(fullName)
			if err != nil {
				return nil, utils.CreatePermanentError(err)
			}
			files, resp, err := c.client.PullRequests.ListFiles(c.ctx, parts[0], parts[1], prID, &opts)
			if err != nil {
				return nil, utils.CreatePermanentHTTPError(resp.StatusCode, err)
			}
			modifiedFiles := make([]string, len(files))
			for i, file := range files {
				modifiedFiles[i] = file.GetFilename()
			}
			return modifiedFiles, nil
		}, createBackOffWithRetries())
	}
	return []string{}, nil
}

const GITHUB_CLONE_DEPTH_ENV = "TFBUDDY_GITHUB_CLONE_DEPTH"

func (c *Client) CloneMergeRequest(ctx context.Context, project string, mr vcs.MR, dest string) (vcs.GitRepo, error) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "CloneMergeRequest")
	defer span.End()

	parts, err := splitFullName(project)
	if err != nil {
		return nil, err
	}

	repo, err := backoff.RetryWithData(func() (*gogithub.Repository, error) {
		r, resp, err := c.client.Repositories.Get(ctx, parts[0], parts[1])
		return r, utils.CreatePermanentHTTPError(resp.StatusCode, err)
	}, createBackOffWithRetries())
	if err != nil {
		return nil, err
	}
	log.Debug().Msg(*repo.CloneURL)
	ref := plumbing.NewBranchReferenceName(mr.GetSourceBranch())
	auth := &githttp.BasicAuth{
		Username: parts[0],
		Password: c.token,
	}

	var progress sideband.Progress
	if log.Trace().Enabled() {
		progress = os.Stdout
	}
	cloneDepth := zgit.GetCloneDepth(GITHUB_CLONE_DEPTH_ENV)
	gitRepo, err := git.PlainClone(dest, false, &git.CloneOptions{
		Auth:          auth,
		URL:           *repo.CloneURL,
		ReferenceName: ref,
		SingleBranch:  true,
		Depth:         cloneDepth,
	})

	if err != nil && err != git.ErrRepositoryAlreadyExists {
		return nil, fmt.Errorf("could not clone MR: %v", err)
	}

	wt, _ := gitRepo.Worktree()
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
		return nil, fmt.Errorf("could not pull MR: %v", err)
	}
	if log.Trace().Enabled() {
		// print contents of repo

		//nolint
		filepath.WalkDir(dest, zgit.WalkRepo)
	}
	return zgit.NewRepository(gitRepo, auth, dest), nil
}

func (c *Client) UpdateMergeRequestDiscussionNote(ctx context.Context, mrIID, noteID int, project, discussionID, comment string) (vcs.MRNote, error) {
	//TODO implement me
	//panic("implement me")
	return nil, nil
}

func (c *Client) ResolveMergeRequestDiscussion(ctx context.Context, s string, i int, s2 string) error {
	// This is a NoOp on GitHub
	return nil
}

func (c *Client) AddMergeRequestDiscussionReply(ctx context.Context, prID int, fullName, discussionID, comment string) (vcs.MRNote, error) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "AddMergeRequestDiscussionReply")
	defer span.End()

	// GitHub doesn't support discussion threads AFAICT.
	iss, err := c.PostIssueComment(ctx, prID, fullName, comment)
	return &IssueComment{iss}, err
}

func (c *Client) SetCommitStatus(ctx context.Context, projectWithNS string, commitSHA string, status vcs.CommitStatusOptions) (vcs.CommitStatus, error) {
	//TODO implement me
	return nil, nil
}

func (c *Client) GetPipelinesForCommit(ctx context.Context, projectWithNS string, commitSHA string) ([]vcs.ProjectPipeline, error) {
	//TODO implement me
	return nil, nil
}

func (c *Client) GetIssue(ctx context.Context, owner *gogithub.User, repo string, issueId int) (*gogithub.Issue, error) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "GetIssue")
	defer span.End()

	owName, err := ResolveOwnerName(owner)
	if err != nil {
		return nil, utils.CreatePermanentError(err)
	}
	return backoff.RetryWithData(func() (*gogithub.Issue, error) {
		iss, resp, err := c.client.Issues.Get(ctx, owName, repo, issueId)
		return iss, utils.CreatePermanentHTTPError(resp.StatusCode, err)
	}, createBackOffWithRetries())
}

func (c *Client) GetPullRequest(ctx context.Context, fullName string, prID int) (*GithubPR, error) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "GetPullRequest")
	defer span.End()

	parts, err := splitFullName(fullName)
	if err != nil {
		return nil, err
	}
	return backoff.RetryWithData(func() (*GithubPR, error) {
		pr, resp, err := c.client.PullRequests.Get(ctx, parts[0], parts[1], prID)
		return &GithubPR{pr}, utils.CreatePermanentHTTPError(resp.StatusCode, err)
	}, createBackOffWithRetries())
}

// PostIssueComment adds a comment to an existing Pull Request
func (c *Client) PostIssueComment(ctx context.Context, prId int, fullName string, body string) (*gogithub.IssueComment, error) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "PostIssueComment")
	defer span.End()

	projectParts, err := splitFullName(fullName)
	if err != nil {
		return nil, utils.CreatePermanentError(err)
	}
	return backoff.RetryWithData(func() (*gogithub.IssueComment, error) {
		comment := &gogithub.IssueComment{
			Body: String(body),
		}
		iss, resp, err := c.client.Issues.CreateComment(ctx, projectParts[0], projectParts[1], prId, comment)
		if err != nil {
			log.Error().Err(err).Msg("github client: could not post issue comment")
		}

		return iss, utils.CreatePermanentHTTPError(resp.StatusCode, err)
	}, createBackOffWithRetries())
}

// PostPullRequestComment adds a review comment to an existing PullRequest
func (c *Client) PostPullRequestComment(ctx context.Context, owner, repo string, prId int, body string) error {
	ctx, span := otel.Tracer("TFC").Start(ctx, "PostPullRequestComment")
	defer span.End()

	// TODO: this is broken
	return backoff.Retry(func() error {
		comment := &gogithub.PullRequestComment{
			//InReplyTo:           nil,
			Body: String(body),
		}
		_, resp, err := c.client.PullRequests.CreateComment(c.ctx, owner, repo, int(prId), comment)
		if err != nil {
			log.Error().Err(err).Msg("could not post pull request comment")
		}
		return utils.CreatePermanentHTTPError(resp.StatusCode, err)
	}, createBackOffWithRetries())
}

func String(str string) *string {
	return &str
}

// ResolveOwnerName is a helper func to find a name for the repo owner, which
// could be in the `Name` field or `Login.
func ResolveOwnerName(owner *gogithub.User) (string, error) {
	name := owner.Name
	if name == nil {
		name = owner.Login
		if name == nil {
			log.Error().Msg("owner name/login is nil")
			return "", fmt.Errorf("owner name/login is nil. %w", utils.ErrPermanent)
		}
	}
	return *name, nil
}

func splitFullName(fullName string) ([]string, error) {
	parts := strings.Split(fullName, "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("github client: invalid repo format. %w", utils.ErrPermanent)
	}
	return parts, nil
}

// ensure type complies with interface
//var _ vcs.MRNote = (*PRComment)(nil)
//type PRComment struct {
//	*gogithub.IssueComment
//}
