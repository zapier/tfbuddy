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
	"github.com/zapier/tfbuddy/internal/config"
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
	cfg       config.Config
}

func (c *GitlabClient) SupportsAggregateAutoMerge() bool {
	return true
}

func (c *GitlabClient) GetAuthenticatedAccountName(ctx context.Context) (string, error) {
	user, err := backoff.RetryWithData(func() (*gogitlab.User, error) {
		user, resp, err := c.client.Users.CurrentUser(gogitlab.WithContext(ctx))
		return user, permanentError(resp, err)
	}, createBackOffWithRetries())
	if err != nil {
		return "", err
	}
	if user.Name != "" {
		return user.Name, nil
	}
	return user.Username, nil
}

const DefaultMaxRetries = 3

func createBackOffWithRetries() backoff.BackOff {
	exp := backoff.NewExponentialBackOff()
	exp.MaxElapsedTime = 30 * time.Second
	return backoff.WithMaxRetries(exp, DefaultMaxRetries)

}
func NewGitlabClient(cfg config.Config) *GitlabClient {
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

	return &GitlabClient{client: glClient, token: token, tokenUser: tokenUser, cfg: cfg}
}
func (c *GitlabClient) ResolveMergeRequestDiscussion(ctx context.Context, projectWithNamespace string, mrIID int, discussionID string) error {
	_, span := otel.Tracer("TFC").Start(ctx, "ResolveMergeRequestDiscussion")
	defer span.End()

	return backoff.Retry(func() error {
		_, resp, err := c.client.Discussions.ResolveMergeRequestDiscussion(projectWithNamespace, mrIID, discussionID, &gogitlab.ResolveMergeRequestDiscussionOptions{Resolved: ptr(true)})
		return permanentError(resp, err)
	}, createBackOffWithRetries())
}

func (c *GitlabClient) ResolveMergeRequestDiscussions(
	ctx context.Context,
	project string,
	mrIID int,
	workspace string,
	action string,
) error {
	discussions, err := backoff.RetryWithData(func() ([]*gogitlab.Discussion, error) {
		discussions, resp, err := c.client.Discussions.ListMergeRequestDiscussions(
			project,
			mrIID,
			&gogitlab.ListMergeRequestDiscussionsOptions{},
		)
		return discussions, permanentError(resp, err)
	}, createBackOffWithRetries())
	if err != nil {
		return err
	}

	currentUser, err := backoff.RetryWithData(func() (*gogitlab.User, error) {
		currentUser, resp, err := c.client.Users.CurrentUser()
		return currentUser, permanentError(resp, err)
	}, createBackOffWithRetries())
	if err != nil {
		return err
	}

	var resolveErr error
	for _, discussion := range discussions {
		if len(discussion.Notes) == 0 {
			continue
		}
		rootNote := discussion.Notes[0]
		noteWorkspace, noteAction, found := utils.ParseTFBuddyMarker(rootNote.Body)
		if rootNote.Author.Username != currentUser.Username || !found ||
			noteWorkspace != workspace || noteAction != action ||
			!rootNote.Resolvable || rootNote.Resolved {
			continue
		}
		if err := c.ResolveMergeRequestDiscussion(ctx, project, mrIID, discussion.ID); err != nil {
			resolveErr = errors.Join(resolveErr, err)
		}
	}
	return resolveErr
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
		return &GitlabCommitStatus{commitStatus}, permanentError(resp, err)
	}, createBackOffWithRetries())
}

func (c *GitlabClient) GetCommitStatuses(ctx context.Context, projectID, commitSHA string) []*gogitlab.CommitStatus {
	_, span := otel.Tracer("TFC").Start(ctx, "GetCommitStatuses")
	defer span.End()

	statuses, err := backoff.RetryWithData(func() ([]*gogitlab.CommitStatus, error) {
		statuses, resp, err := c.client.Commits.GetCommitStatuses(projectID, commitSHA, &gogitlab.GetCommitStatusesOptions{Stage: &glExternalStageName})
		return statuses, permanentError(resp, err)
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
		return permanentError(resp, err)
	}, createBackOffWithRetries())
}

func (c *GitlabClient) MergeMRAtSHA(ctx context.Context, mrIID int, project, expectedSHA string) error {
	_, span := otel.Tracer("TFC").Start(ctx, "MergeMRAtSHA")
	defer span.End()
	return backoff.Retry(func() error {
		_, resp, err := c.client.MergeRequests.AcceptMergeRequest(project, mrIID, &gogitlab.AcceptMergeRequestOptions{
			MergeWhenPipelineSucceeds: ptr(true),
			SHA:                       &expectedSHA,
		})
		if resp == nil {
			if err == nil {
				return errors.New("GitLab merge response was nil")
			}
			return err
		}
		return utils.CreatePermanentHTTPError(resp.StatusCode, err)
	}, createBackOffWithRetries())
}

// GetOldRunUrls crawls MR discussion threads authored by the bot, collects
// previous TFC run URLs into a collapsible block, and (when
// TFBUDDY_DELETE_OLD_COMMENTS is set) deletes entire discussion threads that
// belong to the same workspace+action combination.  Threads for other
// workspaces or actions are left untouched.
func (c *GitlabClient) GetOldRunUrls(ctx context.Context, mrIID int, project string, rootNoteID int, workspace string, action string) (string, error) {
	_, span := otel.Tracer("TFC").Start(ctx, "GetOldRunURLs")
	defer span.End()

	log.Debug().Str("projectID", project).Int("mrIID", mrIID).Str("workspace", workspace).Str("action", action).Msg("pruning notes")

	discussions, err := backoff.RetryWithData(func() ([]*gogitlab.Discussion, error) {
		discussions, resp, err := c.client.Discussions.ListMergeRequestDiscussions(project, mrIID, &gogitlab.ListMergeRequestDiscussionsOptions{})
		return discussions, permanentError(resp, err)
	}, createBackOffWithRetries())
	if err != nil {
		return "", utils.CreatePermanentError(err)
	}

	currentUser, err := backoff.RetryWithData(func() (*gogitlab.User, error) {
		currentUser, resp, err := c.client.Users.CurrentUser()
		return currentUser, permanentError(resp, err)
	}, createBackOffWithRetries())
	if err != nil {
		return "", utils.CreatePermanentError(err)
	}

	var oldRunUrls []string
	var oldRunBlock string

	type discussionToDelete struct {
		noteIDs []int
	}
	var toDelete []discussionToDelete

	for _, disc := range discussions {
		if len(disc.Notes) == 0 {
			continue
		}
		rootNote := disc.Notes[0]
		if rootNote.Author.Username != currentUser.Username {
			continue
		}

		noteWS, noteAction, found := utils.ParseTFBuddyMarker(rootNote.Body)
		if !found || noteWS != workspace || noteAction != action {
			continue
		}

		// Only collect run URL info from discussions that match workspace+action.
		for _, note := range disc.Notes {
			if note.Author.Username != currentUser.Username {
				continue
			}
			runUrl := utils.CaptureSubstring(note.Body, utils.URL_RUN_PREFIX, utils.URL_RUN_SUFFIX)
			runUrlRaw := utils.CaptureSubstring(runUrl, "[", "]")
			runUrlSplit := strings.Split(runUrlRaw, "/")
			runID := ""
			if len(runUrlSplit) > 0 {
				runID = runUrlSplit[len(runUrlSplit)-1]
			} else {
				log.Warn().Msg("Unable to obtain Terraform cloud run ID. The run URL(s) on the previous comments may be malformed.")
				runID = runUrl
				runUrlRaw = runUrl
			}
			runStatus := utils.CaptureSubstring(note.Body, utils.URL_RUN_STATUS_PREFIX, utils.URL_RUN_SUFFIX)
			if runUrl != "" && runStatus != "" {
				oldRunUrls = append(oldRunUrls, fmt.Sprintf("|[%s](%s)|%s|%s|", runID, runUrlRaw, utils.FormatStatus(runStatus), note.CreatedAt))
			}

			oldRunBlockTest := utils.CaptureSubstring(note.Body, utils.URL_RUN_GROUP_PREFIX, utils.URL_RUN_GROUP_SUFFIX)
			if oldRunBlockTest != "" {
				oldRunBlock = oldRunBlockTest
			}
		}

		if rootNote.ID == rootNoteID {
			continue
		}

		var noteIDs []int
		for i := len(disc.Notes) - 1; i >= 0; i-- {
			noteIDs = append(noteIDs, disc.Notes[i].ID)
		}
		toDelete = append(toDelete, discussionToDelete{noteIDs: noteIDs})
	}

	if c.cfg.DeleteOldComments {
		for _, d := range toDelete {
			for _, noteID := range d.noteIDs {
				log.Debug().Str("projectID", project).Int("mrIID", mrIID).Str("workspace", workspace).Str("action", action).Msgf("deleting note %d", noteID)
				err := backoff.Retry(func() error {
					resp, err := c.client.Notes.DeleteMergeRequestNote(project, mrIID, noteID)
					return permanentError(resp, err)
				}, createBackOffWithRetries())
				if err != nil {
					log.Warn().Err(err).Int("noteID", noteID).Msg("could not delete note, skipping")
				}
			}
		}
	}

	if len(oldRunUrls) > 0 {
		if oldRunBlock == "" {
			oldRunBlock = "\n"
		}
		return fmt.Sprintf("%s%s%s\n%s", utils.URL_RUN_GROUP_PREFIX, oldRunBlock, strings.Join(oldRunUrls, "\n"), utils.URL_RUN_GROUP_SUFFIX), nil
	}
	if strings.TrimSpace(oldRunBlock) == "" {
		return "", nil
	}
	return oldRunBlock, nil
}

// CreateMergeRequestComment creates a comment on the merge request.
func (c *GitlabClient) CreateMergeRequestComment(ctx context.Context, mrIID int, projectID, comment string) error {
	_, err := c.CreateMergeRequestCommentWithID(ctx, mrIID, projectID, comment)
	return err
}

// CreateMergeRequestCommentWithID creates a comment and returns its note ID.
func (c *GitlabClient) CreateMergeRequestCommentWithID(
	ctx context.Context,
	mrIID int,
	projectID,
	comment string,
) (int64, error) {
	_, span := otel.Tracer("TFC").Start(ctx, "CreateMergeRequestComment")
	defer span.End()

	if comment != "" {
		return backoff.RetryWithData(func() (int64, error) {
			log.Debug().Str("projectID", projectID).Int("mrIID", mrIID).Msg("posting Gitlab comment")
			note, resp, err := c.client.Notes.CreateMergeRequestNote(
				projectID,
				mrIID,
				&gogitlab.CreateMergeRequestNoteOptions{Body: &comment},
			)
			if err := permanentError(resp, err); err != nil {
				return 0, err
			}
			return int64(note.ID), nil
		}, createBackOffWithRetries())
	}
	return 0, utils.CreatePermanentError(errors.New("comment is empty"))
}

// UpdateMergeRequestComment replaces an existing merge request comment.
func (c *GitlabClient) UpdateMergeRequestComment(
	ctx context.Context,
	mrIID int,
	noteID int64,
	projectID,
	comment string,
) error {
	_, span := otel.Tracer("TFC").Start(ctx, "UpdateMergeRequestComment")
	defer span.End()

	if comment == "" {
		return utils.CreatePermanentError(errors.New("comment is empty"))
	}
	return backoff.Retry(func() error {
		log.Debug().
			Str("projectID", projectID).
			Int("mrIID", mrIID).
			Int64("noteID", noteID).
			Msg("updating Gitlab comment")
		_, resp, err := c.client.Notes.UpdateMergeRequestNote(
			projectID,
			mrIID,
			int(noteID),
			&gogitlab.UpdateMergeRequestNoteOptions{Body: &comment},
		)
		return permanentError(resp, err)
	}, createBackOffWithRetries())
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
		return &GitlabMRDiscussion{dis}, permanentError(resp, err)
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

		return &GitlabMRNote{note}, permanentError(resp, err)
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

			return &GitlabMRNote{note}, permanentError(resp, err)
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
		return permanentError(resp, err)
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
		return b, permanentError(resp, err)
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
				return nil, permanentError(resp, err)
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
func (gm *GitlabMR) GetState() string {
	return gm.MergeRequest.State
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
		return &GitlabMR{mr}, permanentError(resp, err)
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
		return &GitlabMRApproval{approvals}, permanentError(resp, err)
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
func (gP *GitlabPipeline) GetStatus() string {
	return gP.Status
}
func (gP *GitlabPipeline) GetWebURL() string {
	return gP.WebURL
}
func (g *GitlabClient) GetPipelinesForCommit(ctx context.Context, project, commitSHA string) ([]vcs.ProjectPipeline, error) {
	_, span := otel.Tracer("TFC").Start(ctx, "GetPipelinesForCommit")
	defer span.End()

	return backoff.RetryWithData(func() ([]vcs.ProjectPipeline, error) {
		pipelines, resp, err := g.client.Pipelines.ListProjectPipelines(project, &gogitlab.ListProjectPipelinesOptions{
			SHA: &commitSHA,
		})
		if err != nil {
			return nil, permanentError(resp, err)
		}
		output := make([]vcs.ProjectPipeline, len(pipelines))
		for idx, pipeline := range pipelines {
			output[idx] = &GitlabPipeline{pipeline}
		}
		return output, nil
	}, createBackOffWithRetries())
}

// commitStatusesPerPage is GitLab's maximum page size, keeping the number of
// round trips down for pipelines with many jobs.
const commitStatusesPerPage = 100

// permanentError classifies a GitLab API error by the response it came with.
// The response may be nil, which happens when a request fails before any
// response is received, so the status code is read defensively here rather
// than at each call site.
func permanentError(resp *gogitlab.Response, err error) error {
	if err == nil {
		return nil
	}
	statusCode := 0
	if resp != nil && resp.Response != nil {
		statusCode = resp.StatusCode
	}
	return utils.CreatePermanentHTTPError(statusCode, err)
}

type GitlabProjectSettings struct {
	*gogitlab.Project
}

func (gP *GitlabProjectSettings) OnlyAllowMergeIfPipelineSucceeds() bool {
	return gP.Project.OnlyAllowMergeIfPipelineSucceeds
}

func (g *GitlabClient) GetProjectSettings(ctx context.Context, project string) (vcs.ProjectSettings, error) {
	_, span := otel.Tracer("TFC").Start(ctx, "GetProjectSettings")
	defer span.End()

	return backoff.RetryWithData(func() (vcs.ProjectSettings, error) {
		proj, resp, err := g.client.Projects.GetProject(project, nil)
		if err != nil {
			return nil, permanentError(resp, err)
		}
		return &GitlabProjectSettings{proj}, nil
	}, createBackOffWithRetries())
}

type GitlabCommitJobStatus struct {
	*gogitlab.CommitStatus
}

func (gS *GitlabCommitJobStatus) GetName() string {
	return gS.Name
}
func (gS *GitlabCommitJobStatus) GetStatus() string {
	return gS.Status
}
func (gS *GitlabCommitJobStatus) GetPipelineID() int {
	return gS.PipelineId
}
func (gS *GitlabCommitJobStatus) GetAllowFailure() bool {
	return gS.AllowFailure
}

// GetCommitJobStatuses returns the latest status per job name for a commit,
// covering both the project's CI jobs and TFBuddy's own TFC/* external
// statuses. It differs from GetCommitStatuses, which returns only the external
// stage.
func (g *GitlabClient) GetCommitJobStatuses(ctx context.Context, project, commitSHA string) ([]vcs.CommitJobStatus, error) {
	_, span := otel.Tracer("TFC").Start(ctx, "GetCommitJobStatuses")
	defer span.End()

	return backoff.RetryWithData(func() ([]vcs.CommitJobStatus, error) {
		// Paginate: a pipeline with more jobs than one page would otherwise hide a
		// failing job, and the apply gate would let a red pipeline through.
		var output []vcs.CommitJobStatus
		for page := 1; page != 0; {
			statuses, resp, err := g.client.Commits.GetCommitStatuses(project, commitSHA, &gogitlab.GetCommitStatusesOptions{
				ListOptions: gogitlab.ListOptions{Page: page, PerPage: commitStatusesPerPage},
			})
			if err != nil {
				return nil, permanentError(resp, err)
			}
			for _, status := range statuses {
				output = append(output, &GitlabCommitJobStatus{status})
			}
			page = resp.NextPage
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
func (gE *GitlabMergeCommentEvent) GetEventSequence() int64 {
	return int64(gE.ObjectAttributes.ID)
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
