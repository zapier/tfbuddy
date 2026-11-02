package gitlab

import (
	"context"
	"fmt"
	"net/http"
	"time"

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

// mergeRequestApprovals is the part of GitLab's merge request approvals
// response that Auto Mode reads. client-go v0.129.0 decodes approved_by into
// MergeRequestApproverUser, which has no approved_at, so this decodes the
// response itself.
type mergeRequestApprovals struct {
	Approved   bool `json:"approved"`
	ApprovedBy []struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
		ApprovedAt *time.Time `json:"approved_at"`
	} `json:"approved_by"`
}

// GetMergeRequestApprovalState returns whether the merge request is approved
// and when each approval was given.
func (g *GitlabClient) GetMergeRequestApprovalState(ctx context.Context, mrIID int, project string) (*vcs.MRApprovalState, error) {
	_, span := otel.Tracer("TFC").Start(ctx, "GetMergeRequestApprovalState")
	defer span.End()

	u := fmt.Sprintf("projects/%s/merge_requests/%d/approvals", gogitlab.PathEscape(project), mrIID)
	return backoff.RetryWithData(func() (*vcs.MRApprovalState, error) {
		req, err := g.client.NewRequest(http.MethodGet, u, nil, []gogitlab.RequestOptionFunc{gogitlab.WithContext(ctx)})
		if err != nil {
			return nil, backoff.Permanent(err)
		}
		var raw mergeRequestApprovals
		resp, err := g.client.Do(req, &raw)
		if err != nil {
			return nil, permanentError(resp, err)
		}
		state := &vcs.MRApprovalState{Approved: raw.Approved}
		for _, a := range raw.ApprovedBy {
			state.ApprovedBy = append(state.ApprovedBy, vcs.MRApproval{Username: a.User.Username, ApprovedAt: a.ApprovedAt})
		}
		return state, nil
	}, createBackOffWithRetries(ctx))
}

// GetMergeRequestVersions returns the merge request's diff versions, across
// pages, in the order GitLab lists them.
func (g *GitlabClient) GetMergeRequestVersions(ctx context.Context, mrIID int, project string) ([]vcs.MRVersion, error) {
	_, span := otel.Tracer("TFC").Start(ctx, "GetMergeRequestVersions")
	defer span.End()

	return backoff.RetryWithData(func() ([]vcs.MRVersion, error) {
		var output []vcs.MRVersion
		for page := 1; page != 0; {
			versions, resp, err := g.client.MergeRequests.GetMergeRequestDiffVersions(project, mrIID, &gogitlab.GetMergeRequestDiffVersionsOptions{
				Page: page, PerPage: autoModePageSize,
			}, gogitlab.WithContext(ctx))
			if err != nil {
				return nil, permanentError(resp, err)
			}
			for _, v := range versions {
				version := vcs.MRVersion{HeadSHA: v.HeadCommitSHA}
				if v.CreatedAt != nil {
					version.CreatedAt = *v.CreatedAt
				}
				output = append(output, version)
			}
			page = resp.NextPage
		}
		return output, nil
	}, createBackOffWithRetries(ctx))
}
