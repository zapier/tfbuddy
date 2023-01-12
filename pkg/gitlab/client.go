package gitlab

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/vcs"

	gogitlab "github.com/xanzy/go-gitlab"
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
	exp.MaxElapsedTime = 90 * time.Second
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
func (c *GitlabClient) ResolveMergeRequestDiscussion(projectWithNamespace string, mrIID int, discussionID string) error {
	return backoff.Retry(func() error {
		_, _, err := c.client.Discussions.ResolveMergeRequestDiscussion(projectWithNamespace, mrIID, discussionID, &gogitlab.ResolveMergeRequestDiscussionOptions{Resolved: gogitlab.Bool(true)})
		return err
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

func (c *GitlabClient) SetCommitStatus(projectWithNS string, commitSHA string, status vcs.CommitStatusOptions) (vcs.CommitStatus, error) {
	return backoff.RetryWithData(func() (vcs.CommitStatus, error) {
		commitStatus, _, err := c.client.Commits.SetCommitStatus(projectWithNS, commitSHA, status.(*GitlabCommitStatusOptions).SetCommitStatusOptions)
		return &GitlabCommitStatus{commitStatus}, err
	}, createBackOffWithRetries())
}
func (c *GitlabClient) GetCommitStatuses(projectID, commitSHA string) []*gogitlab.CommitStatus {
	statuses, _, err := c.client.Commits.GetCommitStatuses(projectID, commitSHA, &gogitlab.GetCommitStatusesOptions{Stage: &glExternalStageName})
	if err != nil {
		log.Fatal().Msgf("could not get commit statuses: %v\n", err)
	}

	return statuses
}

// CreateMergeRequestComment creates a comment on the merge request.
func (c *GitlabClient) CreateMergeRequestComment(mrIID int, projectID, comment string) error {
	if comment != "" {
		return backoff.Retry(func() error {
			log.Debug().Str("projectID", projectID).Int("mrIID", mrIID).Msg("posting Gitlab comment")
			_, _, err := c.client.Notes.CreateMergeRequestNote(projectID, mrIID, &gogitlab.CreateMergeRequestNoteOptions{Body: gogitlab.String(comment)})
			return err
		}, createBackOffWithRetries())
	}
	return errors.New("comment is empty")
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

func (c *GitlabClient) CreateMergeRequestDiscussion(mrIID int, project, comment string) (vcs.MRDiscussionNotes, error) {
	if comment == "" {
		return nil, errors.New("comment is empty")
	}
	return backoff.RetryWithData(func() (vcs.MRDiscussionNotes, error) {
		log.Debug().Str("project", project).Int("mrIID", mrIID).Msg("create Gitlab discussion")
		dis, _, err := c.client.Discussions.CreateMergeRequestDiscussion(project, mrIID, &gogitlab.CreateMergeRequestDiscussionOptions{
			Body: gogitlab.String(comment),
		})
		return &GitlabMRDiscussion{dis}, err
	}, createBackOffWithRetries())
}

func (c *GitlabClient) UpdateMergeRequestDiscussionNote(mrIID, noteID int, project, discussionID, comment string) (vcs.MRNote, error) {
	if comment == "" {
		return nil, errors.New("comment is empty")
	}
	return backoff.RetryWithData(func() (vcs.MRNote, error) {
		log.Debug().Str("project", project).Int("mrIID", mrIID).Msg("update Gitlab discussion")
		note, _, err := c.client.Discussions.UpdateMergeRequestDiscussionNote(
			project,
			mrIID,
			discussionID,
			noteID,
			&gogitlab.UpdateMergeRequestDiscussionNoteOptions{
				Body: gogitlab.String(comment),
			})
		return &GitlabMRNote{note}, err
	}, createBackOffWithRetries())
}

// AddMergeRequestDiscussionReply creates a comment on the merge request.
func (c *GitlabClient) AddMergeRequestDiscussionReply(mrIID int, project, discussionID, comment string) (vcs.MRNote, error) {
	if comment != "" {
		return backoff.RetryWithData(func() (vcs.MRNote, error) {
			log.Debug().Str("project", project).Int("mrIID", mrIID).Msg("posting Gitlab discussion reply")
			note, _, err := c.client.Discussions.AddMergeRequestDiscussionNote(project, mrIID, discussionID, &gogitlab.AddMergeRequestDiscussionNoteOptions{Body: gogitlab.String(comment)})

			return &GitlabMRNote{note}, err
		}, createBackOffWithRetries())
	}
	return nil, errors.New("comment is empty")
}

// ResolveMergeRequestDiscussionReply marks a discussion thread as resolved /  unresolved.
func (c *GitlabClient) ResolveMergeRequestDiscussionReply(mrIID int, project, discussionID string, resolved bool) error {
	return backoff.Retry(func() error {
		log.Debug().Str("project", project).Int("mrIID", mrIID).Msg("posting Gitlab discussion reply")
		_, _, err := c.client.Discussions.ResolveMergeRequestDiscussion(project, mrIID, discussionID, &gogitlab.ResolveMergeRequestDiscussionOptions{Resolved: gogitlab.Bool(resolved)})
		return err
	}, createBackOffWithRetries())
}

// GetRepoFile retrieves a single file from a Gitlab repository using the RepositoryFiles API
func (g *GitlabClient) GetRepoFile(project, file, ref string) ([]byte, error) {
	if ref == "" {
		ref = "HEAD"
	}
	return backoff.RetryWithData(func() ([]byte, error) {
		b, _, err := g.client.RepositoryFiles.GetRawFile(project, file, &gogitlab.GetRawFileOptions{Ref: gogitlab.String(ref)})
		return b, err
	}, createBackOffWithRetries())
}

// GetMergeRequestModifiedFiles returns the names of files that were modified in the merge request
// relative to the repo root, e.g. parent/child/file.txt.
func (g *GitlabClient) GetMergeRequestModifiedFiles(mrIID int, projectID string) ([]string, error) {
	const maxPerPage = 100
	return backoff.RetryWithData(func() ([]string, error) {
		var files []string
		nextPage := 1
		// Constructing the api url by hand so we can do pagination.
		apiURL := fmt.Sprintf("projects/%s/merge_requests/%d/changes", url.QueryEscape(projectID), mrIID)
		for {
			opts := gogitlab.ListOptions{
				Page:    nextPage,
				PerPage: maxPerPage,
			}
			req, err := g.client.NewRequest("GET", apiURL, opts, nil)
			if err != nil {
				return nil, err
			}
			mr := new(gogitlab.MergeRequest)
			resp, err := g.client.Do(req, mr)
			if err != nil {
				return nil, err
			}

			for _, f := range mr.Changes {
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
func (g *GitlabClient) GetMergeRequest(mrIID int, project string) (vcs.DetailedMR, error) {
	return backoff.RetryWithData(func() (vcs.DetailedMR, error) {
		mr, _, err := g.client.MergeRequests.GetMergeRequest(
			project,
			mrIID,
			&gogitlab.GetMergeRequestsOptions{
				RenderHTML:                  gogitlab.Bool(false),
				IncludeDivergedCommitsCount: gogitlab.Bool(true),
				IncludeRebaseInProgress:     gogitlab.Bool(true),
			},
		)
		if err != nil {
			return nil, err
		}
		return &GitlabMR{mr}, nil
	}, createBackOffWithRetries())
}

type GitlabMRApproval struct {
	*gogitlab.MergeRequestApprovals
}

func (gm *GitlabMRApproval) IsApproved() bool {
	return gm.Approved
}
func (g *GitlabClient) GetMergeRequestApprovals(mrIID int, project string) (vcs.MRApproved, error) {
	return backoff.RetryWithData(func() (vcs.MRApproved, error) {
		approvals, _, err := g.client.MergeRequestApprovals.GetConfiguration(
			project,
			mrIID,
		)
		if err != nil {
			return nil, err
		}
		return &GitlabMRApproval{approvals}, nil
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
func (g *GitlabClient) GetPipelinesForCommit(project, commitSHA string) ([]vcs.ProjectPipeline, error) {
	return backoff.RetryWithData(func() ([]vcs.ProjectPipeline, error) {
		pipelines, _, err := g.client.Pipelines.ListProjectPipelines(project, &gogitlab.ListProjectPipelinesOptions{
			SHA: gogitlab.String(commitSHA),
		})
		if err != nil {
			return nil, err
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
