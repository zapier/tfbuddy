package vcs

import "strings"

const (
	// PipelineSourceMergeRequestEvent is the GitLab pipeline source for pipelines
	// created by a merge request.
	PipelineSourceMergeRequestEvent = "merge_request_event"
	// PipelineSourceExternal is the GitLab pipeline source for pipelines created
	// implicitly by the commit status API. TFBuddy produces these whenever it
	// posts a status without a pipeline ID to attach it to.
	PipelineSourceExternal = "external"
	// CommitStatusPrefix prefixes every commit status TFBuddy publishes, which is
	// how TFBuddy's own statuses are told apart from a project's CI jobs.
	CommitStatusPrefix = "TFC/"
)

// SelectPipelineForCommit returns the pipeline that represents CI for a commit:
// the merge request pipeline when the commit has one, otherwise the newest
// pipeline. It returns nil for an empty list.
//
// GitLab lists pipelines newest first (order_by=id, sort=desc by default), so
// the fallback is the head of the list.
func SelectPipelineForCommit(pipelines []ProjectPipeline) ProjectPipeline {
	for _, p := range pipelines {
		if p.GetSource() == PipelineSourceMergeRequestEvent {
			return p
		}
	}
	if len(pipelines) > 0 {
		return pipelines[0]
	}
	return nil
}

// IsTFBuddyCommitStatus reports whether a commit status was published by
// TFBuddy rather than by the project's CI.
func IsTFBuddyCommitStatus(s CommitJobStatus) bool {
	return strings.HasPrefix(s.GetName(), CommitStatusPrefix)
}
