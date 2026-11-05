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
