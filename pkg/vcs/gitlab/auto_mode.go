package gitlab

import (
	"context"

	"github.com/cenkalti/backoff/v4"
	"github.com/zapier/tfbuddy/pkg/vcs"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
	"go.opentelemetry.io/otel"
)

// autoModePageSize is GitLab's maximum page size.
const autoModePageSize = 100

// GetMergeRequestCommits returns every commit on the merge request, across
// pages.
func (g *GitlabClient) GetMergeRequestCommits(ctx context.Context, mrIID int, project string) ([]vcs.MRCommit, error) {
	_, span := otel.Tracer("TFC").Start(ctx, "GetMergeRequestCommits")
	defer span.End()

	return backoff.RetryWithData(func() ([]vcs.MRCommit, error) {
		var output []vcs.MRCommit
		for page := 1; page != 0; {
			commits, resp, err := g.client.MergeRequests.GetMergeRequestCommits(project, mrIID, &gogitlab.GetMergeRequestCommitsOptions{
				Page: page, PerPage: autoModePageSize,
			}, gogitlab.WithContext(ctx))
			if err != nil {
				return nil, permanentError(resp, err)
			}
			for _, c := range commits {
				output = append(output, vcs.MRCommit{SHA: c.ID, AuthorEmail: c.AuthorEmail, CommitterEmail: c.CommitterEmail})
			}
			page = resp.NextPage
		}
		return output, nil
	}, createBackOffWithRetries(ctx))
}

// GitlabPipelineJob is a CI job of a pipeline.
type GitlabPipelineJob struct {
	*gogitlab.Job
}

func (j *GitlabPipelineJob) GetID() int        { return j.ID }
func (j *GitlabPipelineJob) GetName() string   { return j.Name }
func (j *GitlabPipelineJob) GetStatus() string { return j.Status }

// GetPipelineJobs returns the latest run of every CI job in the pipeline,
// across pages. It reads GitLab's pipeline jobs API, which lists only jobs the
// pipeline ran from its CI config: a commit status that anyone posts with a
// job's name is not one of them.
func (g *GitlabClient) GetPipelineJobs(ctx context.Context, project string, pipelineID int) ([]vcs.PipelineJob, error) {
	_, span := otel.Tracer("TFC").Start(ctx, "GetPipelineJobs")
	defer span.End()

	return backoff.RetryWithData(func() ([]vcs.PipelineJob, error) {
		var output []vcs.PipelineJob
		for page := 1; page != 0; {
			jobs, resp, err := g.client.Jobs.ListPipelineJobs(project, pipelineID, &gogitlab.ListJobsOptions{
				ListOptions: gogitlab.ListOptions{Page: page, PerPage: autoModePageSize},
				// Only each job's latest run: a retried job is judged by its
				// retry.
				IncludeRetried: gogitlab.Ptr(false),
			}, gogitlab.WithContext(ctx))
			if err != nil {
				return nil, permanentError(resp, err)
			}
			for _, j := range jobs {
				output = append(output, &GitlabPipelineJob{j})
			}
			page = resp.NextPage
		}
		return output, nil
	}, createBackOffWithRetries(ctx))
}
