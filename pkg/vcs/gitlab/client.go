package gitlab

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/utils"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.opentelemetry.io/otel"

	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

var glExternalStageName = "external"

type GitlabClient struct {
	client    *gogitlab.Client
	token     string
	tokenUser string
}

const DefaultMaxRetries = 3

func createBackOffWithRetries() backoff.BackOff {
	exp := backoff.NewExponentialBackOff()
	exp.MaxElapsedTime = 30 * time.Second
	return backoff.WithMaxRetries(exp, DefaultMaxRetries)

}
func NewGitlabClient() *GitlabClient {
	token := os.Getenv("GITLAB_TOKEN")
	if token == "" {
		token = os.Getenv("GITLAB_ACCESS_TOKEN")
		if token == "" {
			log.Info().Msg("GITLAB_TOKEN is not set, skipping creation of Gitlab API client")
			return nil
		}
	}
	//TODO: I believe this is legacy and can be removed?
	tokenUser := os.Getenv("GITLAB_TOKEN_USER")
	if token == "" {
		if token == "" {
			log.Fatal().Msg("GITLAB_TOKEN_USER is not set, cannot create Gitlab API client")
		}
	}

	var err error
	glClient, err := gogitlab.NewClient(token)
	if err != nil {
		log.Fatal().Msgf("Failed to create client: %v", err)
	}

	return &GitlabClient{glClient, token, tokenUser}
}
func (c *GitlabClient) ResolveMergeRequestDiscussion(ctx context.Context, projectWithNamespace string, mrIID int, discussionID string) error {
	_, span := otel.Tracer("TFC").Start(ctx, "ResolveMergeRequestDiscussion")
	defer span.End()

	return backoff.Retry(func() error {
		_, resp, err := c.client.Discussions.ResolveMergeRequestDiscussion(projectWithNamespace, mrIID, discussionID, &gogitlab.ResolveMergeRequestDiscussionOptions{Resolved: ptr(true)})
		return utils.CreatePermanentHTTPError(resp.StatusCode, err)
	}, createBackOffWithRetries())
}

type GitlabCommitStatusOptions struct {
	*gogitlab.SetCommitStatusOptions
}

func (gO *GitlabCommitStatusOptions) GetName() string {
	return *gO.Name
}
func (gO *GitlabCommitStatusOptions) GetContext() string {
	return *gO.Context
}
func (gO *GitlabCommitStatusOptions) GetTargetURL() string {
	return *gO.TargetURL
}
func (gO *GitlabCommitStatusOptions) GetDescription() string {
	return *gO.Description
}
func (gO *GitlabCommitStatusOptions) GetState() string {
	return string(gO.State)
}
func (gO *GitlabCommitStatusOptions) GetPipelineID() int {
	return *gO.PipelineID
}

type GitlabCommitStatus struct {
	*gogitlab.CommitStatus
}

func (gS *GitlabCommitStatus) Info() string {
	return fmt.Sprintf("%s %s %s", gS.Author.Username, gS.Name, gS.SHA)
}

func (c *GitlabClient) SetCommitStatus(ctx context.Context, projectWithNS string, commitSHA string, status vcs.CommitStatusOptions) (vcs.CommitStatus, error) {
	_, span := otel.Tracer("TFC").Start(ctx, "SetCommitStatus")
	defer span.End()

	return backoff.RetryWithData(func() (vcs.CommitStatus, error) {
		commitStatus, resp, err := c.client.Commits.SetCommitStatus(projectWithNS, commitSHA, status.(*GitlabCommitStatusOptions).SetCommitStatusOptions)
		return &GitlabCommitStatus{commitStatus}, utils.CreatePermanentHTTPError(resp.StatusCode, err)
	}, createBackOffWithRetries())
}

func (c *GitlabClient) GetCommitStatuses(ctx context.Context, projectID, commitSHA string) []*gogitlab.CommitStatus {
	_, span := otel.Tracer("TFC").Start(ctx, "GetCommitStatuses")
	defer span.End()

	statuses, err := backoff.RetryWithData(func() ([]*gogitlab.CommitStatus, error) {
		statuses, resp, err := c.client.Commits.GetCommitStatuses(projectID, commitSHA, &gogitlab.GetCommitStatusesOptions{Stage: &glExternalStageName})
		return statuses, utils.CreatePermanentHTTPError(resp.StatusCode, err)
	}, createBackOffWithRetries())
	if err != nil {
		log.Fatal().Msgf("could not get commit statuses: %v\n", err)
	}

	return statuses
}
func (c *GitlabClient) MergeMR(ctx context.Context, mrIID int, project string) error {
	_, span := otel.Tracer("TFC").Start(ctx, "MergeMR")
	defer span.End()
	return backoff.Retry(func() error {
		_, resp, err := c.client.MergeRequests.AcceptMergeRequest(project, mrIID, &gogitlab.AcceptMergeRequestOptions{})
		return utils.CreatePermanentHTTPError(resp.StatusCode, err)
	}, createBackOffWithRetries())
}

// Crawl the comments on this MR for tfbuddy comments, grab any TFC urls out of them, and delete them.
func (c *GitlabClient) GetOldRunUrls(ctx context.Context, mrIID int, project string, rootNoteID int) (string, error) {
	_, span := otel.Tracer("TFC").Start(ctx, "GetOldRunURLs")
	defer span.End()

	log.Debug().Str("projectID", project).Int("mrIID", mrIID).Msg("pruning notes")
	notes, err := backoff.RetryWithData(func() ([]*gogitlab.Note, error) {
		notes, resp, err := c.client.Notes.ListMergeRequestNotes(project, mrIID, &gogitlab.ListMergeRequestNotesOptions{})
		return notes, utils.CreatePermanentHTTPError(resp.StatusCode, err)
	}, createBackOffWithRetries())
	if err != nil {
		return "", utils.CreatePermanentError(err)
	}

	currentUser, err := backoff.RetryWithData(func() (*gogitlab.User, error) {
		currentUser, resp, err := c.client.Users.CurrentUser()
		return currentUser, utils.CreatePermanentHTTPError(resp.StatusCode, err)
	}, createBackOffWithRetries())
	if err != nil {
		return "", utils.CreatePermanentError(err)
	}

	var oldRunUrls []string
	var oldRunBlock string
	for _, note := range notes {
		if note.Author.Username == currentUser.Username {
			runUrl := utils.CaptureSubstring(note.Body, utils.URL_RUN_PREFIX, utils.URL_RUN_SUFFIX)
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
			runStatus := utils.CaptureSubstring(note.Body, utils.URL_RUN_STATUS_PREFIX, utils.URL_RUN_SUFFIX)
			if runUrl != "" && runStatus != "" {
				oldRunUrls = append(oldRunUrls, fmt.Sprintf("|[%s](%s)|%s|%s|", runID, runUrlRaw, utils.FormatStatus(runStatus), note.CreatedAt))
			}

			// Gitlab default sort is order by created by, so take the last match on this
			oldRunBlockTest := utils.CaptureSubstring(note.Body, utils.URL_RUN_GROUP_PREFIX, utils.URL_RUN_GROUP_SUFFIX)
			// Add a new line for the first table entry so that markdown tabling can properly begin
			oldRunBlock = "\n"
			if oldRunBlockTest != "" {
				oldRunBlock = oldRunBlockTest
			}
			if os.Getenv("TFBUDDY_DELETE_OLD_COMMENTS") != "" && note.ID != rootNoteID {
				log.Debug().Str("projectID", project).Int("mrIID", mrIID).Msgf("deleting note %d", note.ID)
				err := backoff.Retry(func() error {
					resp, err := c.client.Notes.DeleteMergeRequestNote(project, mrIID, note.ID)
					return utils.CreatePermanentHTTPError(resp.StatusCode, err)
				}, createBackOffWithRetries())
				if err != nil {
					return "", utils.CreatePermanentError(err)
				}
			}
		}
	}

	// Add new urls into block
	if len(oldRunUrls) > 0 {
		return fmt.Sprintf("%s%s%s\n%s", utils.URL_RUN_GROUP_PREFIX, oldRunBlock, strings.Join(oldRunUrls, "\n"), utils.URL_RUN_GROUP_SUFFIX), nil
	}
	return oldRunBlock, nil
}

// CreateMergeRequestComment creates a comment on the merge request.
func (c *GitlabClient) CreateMergeRequestComment(ctx context.Context, mrIID int, projectID, comment string) error {
	_, span := otel.Tracer("TFC").Start(ctx, "CreateMergeRequestComment")
	defer span.End()

	if comment != "" {
		return backoff.Retry(func() error {
			log.Debug().Str("projectID", projectID).Int("mrIID", mrIID).Msg("posting Gitlab comment")
			_, resp, err := c.client.Notes.CreateMergeRequestNote(projectID, mrIID, &gogitlab.CreateMergeRequestNoteOptions{Body: &comment})
			return utils.CreatePermanentHTTPError(resp.StatusCode, err)
		}, createBackOffWithRetries())
	}
	return utils.CreatePermanentError(errors.New("comment is empty"))
}

type GitlabMRDiscussion struct {
	*gogitlab.Discussion
}

func (gd *GitlabMRDiscussion) GetDiscussionID() string {
	return gd.ID
}
func (gd *GitlabMRDiscussion) GetMRNotes() []vcs.MRNote {
	retVal := make([]vcs.MRNote, len(gd.Notes))
	for idx, note := range gd.Notes {
		retVal[idx] = &GitlabMRNote{note}
	}
	return retVal
}

type GitlabMRNote struct {
	*gogitlab.Note
}

func (gn *GitlabMRNote) GetNoteID() int64 {
	return int64(gn.Note.ID)
}

func (c *GitlabClient) CreateMergeRequestDiscussion(ctx context.Context, mrIID int, project, comment string) (vcs.MRDiscussionNotes, error) {
	_, span := otel.Tracer("TFC").Start(ctx, "CreateMergeRequestDiscussion")
	defer span.End()

	if comment == "" {
		return nil, errors.New("comment is empty")
	}

	return backoff.RetryWithData(func() (vcs.MRDiscussionNotes, error) {
		log.Debug().Str("project", project).Int("mrIID", mrIID).Msg("create Gitlab discussion")
		dis, resp, err := c.client.Discussions.CreateMergeRequestDiscussion(project, mrIID, &gogitlab.CreateMergeRequestDiscussionOptions{
			Body: &comment,
		})
		return &GitlabMRDiscussion{dis}, utils.CreatePermanentHTTPError(resp.StatusCode, err)
	}, createBackOffWithRetries())
}

func (c *GitlabClient) UpdateMergeRequestDiscussionNote(ctx context.Context, mrIID, noteID int, project, discussionID, comment string) (vcs.MRNote, error) {
	_, span := otel.Tracer("TFC").Start(ctx, "UpdateMergeRequestDiscussionNote")
	defer span.End()

	if comment == "" {
		return nil, utils.CreatePermanentError(errors.New("comment is empty"))
	}
	return backoff.RetryWithData(func() (vcs.MRNote, error) {
		log.Debug().Str("project", project).Int("mrIID", mrIID).Msg("update Gitlab discussion")
		note, resp, err := c.client.Discussions.UpdateMergeRequestDiscussionNote(
			project,
			mrIID,
			discussionID,
			noteID,
			&gogitlab.UpdateMergeRequestDiscussionNoteOptions{
				Body: &comment,
			})

		return &GitlabMRNote{note}, utils.CreatePermanentHTTPError(resp.StatusCode, err)
	}, createBackOffWithRetries())
}

// AddMergeRequestDiscussionReply creates a comment on the merge request.
func (c *GitlabClient) AddMergeRequestDiscussionReply(ctx context.Context, mrIID int, project, discussionID, comment string) (vcs.MRNote, error) {
	_, span := otel.Tracer("TFC").Start(ctx, "AddMergeRequestDiscussionReply")
	defer span.End()

	if comment != "" {
		return backoff.RetryWithData(func() (vcs.MRNote, error) {
			log.Debug().Str("project", project).Int("mrIID", mrIID).Msg("posting Gitlab discussion reply")
			note, resp, err := c.client.Discussions.AddMergeRequestDiscussionNote(project, mrIID, discussionID, &gogitlab.AddMergeRequestDiscussionNoteOptions{Body: &comment})

			return &GitlabMRNote{note}, utils.CreatePermanentHTTPError(resp.StatusCode, err)
		}, createBackOffWithRetries())
	}
	return nil, utils.CreatePermanentError(errors.New("comment is empty"))
}

// ResolveMergeRequestDiscussionReply marks a discussion thread as resolved /  unresolved.
func (c *GitlabClient) ResolveMergeRequestDiscussionReply(ctx context.Context, mrIID int, project, discussionID string, resolved bool) error {
	_, span := otel.Tracer("TFC").Start(ctx, "ResolveMergeRequestDiscussionReply")
	defer span.End()

	return backoff.Retry(func() error {
		log.Debug().Str("project", project).Int("mrIID", mrIID).Msg("posting Gitlab discussion reply")
		_, resp, err := c.client.Discussions.ResolveMergeRequestDiscussion(project, mrIID, discussionID, &gogitlab.ResolveMergeRequestDiscussionOptions{Resolved: &resolved})
		return utils.CreatePermanentHTTPError(resp.StatusCode, err)
	}, createBackOffWithRetries())
}

// GetRepoFile retrieves a single file from a Gitlab repository using the RepositoryFiles API
func (g *GitlabClient) GetRepoFile(ctx context.Context, project, file, ref string) ([]byte, error) {
	_, span := otel.Tracer("TFC").Start(ctx, "GetRepoFile")
	defer span.End()

	if ref == "" {
		ref = "HEAD"
	}
	return backoff.RetryWithData(func() ([]byte, error) {
		b, resp, err := g.client.RepositoryFiles.GetRawFile(project, file, &gogitlab.GetRawFileOptions{Ref: &ref})
		return b, utils.CreatePermanentHTTPError(resp.StatusCode, err)
	}, createBackOffWithRetries())
}

// GetMergeRequestModifiedFiles returns the names of files that were modified in the merge request
// relative to the repo root, e.g. parent/child/file.txt.
func (g *GitlabClient) GetMergeRequestModifiedFiles(ctx context.Context, mrIID int, projectID string) ([]string, error) {
	_, span := otel.Tracer("TFC").Start(ctx, "GetMergeRequestModifiedFiles")
	defer span.End()

	const maxPerPage = 100
	return backoff.RetryWithData(func() ([]string, error) {
		var files []string
		nextPage := 1

		for {
			opts := gogitlab.ListMergeRequestDiffsOptions{
				ListOptions: gogitlab.ListOptions{
					PerPage: maxPerPage,
					Page:    nextPage,
				},
			}
			diffs, resp, err := g.client.MergeRequests.ListMergeRequestDiffs(
				projectID, mrIID, &opts,
			)
			if err != nil {
				return nil, utils.CreatePermanentHTTPError(resp.StatusCode, err)
			}

			for _, f := range diffs {
				files = append(files, f.NewPath)

				// If the file was renamed, we'll want to run plan in the directory
				// it was moved from as well.
				if f.RenamedFile {
					files = append(files, f.OldPath)
				}
			}
			if resp.NextPage == 0 {
				break
			}
			nextPage = resp.NextPage
		}

		return files, nil
	}, createBackOffWithRetries())
}

type GitlabMR struct {
	*gogitlab.MergeRequest
}

func (gm *GitlabMR) HasConflicts() bool {
	return gm.MergeRequest.HasConflicts
}
func (gm *GitlabMR) GetSourceBranch() string {
	return gm.MergeRequest.SourceBranch
}
func (gm *GitlabMR) GetInternalID() int {
	return gm.MergeRequest.IID
}
func (gm *GitlabMR) GetWebURL() string {
	return gm.MergeRequest.WebURL
}
func (gm *GitlabMR) GetAuthor() vcs.MRAuthor {
	return &GitlabMRAuthor{gm.Author}
}
func (gm *GitlabMR) GetTitle() string {
	return gm.MergeRequest.Title
}
func (gm *GitlabMR) GetTargetBranch() string {
	return gm.MergeRequest.TargetBranch
}

type GitlabMRAuthor struct {
	*gogitlab.BasicUser
}

func (ga *GitlabMRAuthor) GetUsername() string {
	return ga.Username
}
func (g *GitlabClient) GetMergeRequest(ctx context.Context, mrIID int, project string) (vcs.DetailedMR, error) {
	ctx, span := otel.Tracer("hooks").Start(ctx, "GetMergeRequest")
	defer span.End()

	return backoff.RetryWithData(func() (vcs.DetailedMR, error) {
		_, span := otel.Tracer("hooks").Start(ctx, "GetMergeRequest")
		defer span.End()
		mr, resp, err := g.client.MergeRequests.GetMergeRequest(
			project,
			mrIID,
			&gogitlab.GetMergeRequestsOptions{
				RenderHTML:                  ptr(false),
				IncludeDivergedCommitsCount: ptr(true),
				IncludeRebaseInProgress:     ptr(true),
			},
		)
		return &GitlabMR{mr}, utils.CreatePermanentHTTPError(resp.StatusCode, err)
	}, createBackOffWithRetries())
}

type GitlabMRApproval struct {
	*gogitlab.MergeRequestApprovals
}

func (gm *GitlabMRApproval) IsApproved() bool {
	return gm.Approved
}
func (g *GitlabClient) GetMergeRequestApprovals(ctx context.Context, mrIID int, project string) (vcs.MRApproved, error) {
	_, span := otel.Tracer("TFC").Start(ctx, "GetMergeRequestApprovals")
	defer span.End()

	return backoff.RetryWithData(func() (vcs.MRApproved, error) {
		approvals, resp, err := g.client.MergeRequestApprovals.GetConfiguration(
			project,
			mrIID,
		)
		return &GitlabMRApproval{approvals}, utils.CreatePermanentHTTPError(resp.StatusCode, err)
	}, createBackOffWithRetries())
}

type GitlabPipeline struct {
	*gogitlab.PipelineInfo
}

func (gP *GitlabPipeline) GetSource() string {
	return gP.Source
}
func (gP *GitlabPipeline) GetID() int {
	return gP.ID
}
func (g *GitlabClient) GetPipelinesForCommit(ctx context.Context, project, commitSHA string) ([]vcs.ProjectPipeline, error) {
	_, span := otel.Tracer("TFC").Start(ctx, "GetPipelinesForCommit")
	defer span.End()

	return backoff.RetryWithData(func() ([]vcs.ProjectPipeline, error) {
		pipelines, resp, err := g.client.Pipelines.ListProjectPipelines(project, &gogitlab.ListProjectPipelinesOptions{
			SHA: &commitSHA,
		})
		if err != nil {
			return nil, utils.CreatePermanentHTTPError(resp.StatusCode, err)
		}
		output := make([]vcs.ProjectPipeline, len(pipelines))
		for idx, pipeline := range pipelines {
			output[idx] = &GitlabPipeline{pipeline}
		}
		return output, nil
	}, createBackOffWithRetries())
}

type GitlabMergeCommentEvent struct {
	*gogitlab.MergeCommentEvent
}

func (gE *GitlabMergeCommentEvent) GetPathWithNamespace() string {
	return gE.Project.PathWithNamespace
}
func (gE *GitlabMergeCommentEvent) GetProject() vcs.Project {
	return gE
}
func (gE *GitlabMergeCommentEvent) GetMR() vcs.MR {
	return gE
}
func (gE *GitlabMergeCommentEvent) GetAuthor() vcs.MRAuthor {
	return gE
}

func (gE *GitlabMergeCommentEvent) GetSourceBranch() string {
	return gE.MergeRequest.SourceBranch
}
func (gE *GitlabMergeCommentEvent) GetTargetBranch() string {
	return gE.MergeRequest.TargetBranch
}
func (gE *GitlabMergeCommentEvent) GetInternalID() int {
	return gE.MergeRequest.IID
}
func (gE *GitlabMergeCommentEvent) GetUsername() string {
	return gE.MergeRequest.LastCommit.Author.Name
}

func (gE *GitlabMergeCommentEvent) GetNote() string {
	return gE.ObjectAttributes.Note
}
func (gE *GitlabMergeCommentEvent) GetType() string {
	return gE.ObjectAttributes.Type
}
func (gE *GitlabMergeCommentEvent) GetDiscussionID() string {
	return gE.ObjectAttributes.DiscussionID
}
func (gE *GitlabMergeCommentEvent) GetSHA() string {
	return gE.MergeRequest.LastCommit.ID
}
func (gE *GitlabMergeCommentEvent) GetLastCommit() vcs.Commit {
	return gE
}
func (gE *GitlabMergeCommentEvent) GetAttributes() vcs.MRAttributes {
	return gE
}

func ptr[T any](t T) *T {
	return &t
}
