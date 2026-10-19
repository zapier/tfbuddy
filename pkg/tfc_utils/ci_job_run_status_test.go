package tfc_utils

import (
	"testing"

	gogitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/zapier/tfbuddy/pkg/vcs"
)

// The job must only count statuses TFBuddy wrote. A project's own CI jobs are
// also commit statuses on the same SHA, and counting them would make the job
// pass on a commit TFBuddy never saw.
func Test_tfbuddyStatusesIgnoresProjectCI(t *testing.T) {
	in := []*gogitlab.CommitStatus{
		{Name: "lint", Status: "success"},
		{Name: "tfc:status", Status: "running"},
		{Name: vcs.CommitStatusPrefix + "plan/a", Status: "failed"},
	}
	got := tfbuddyStatuses(in)
	if len(got) != 1 || got[0].Name != vcs.CommitStatusPrefix+"plan/a" {
		t.Fatalf("tfbuddyStatuses() = %v, want only the TFBuddy plan status", got)
	}
	if got := tfbuddyStatuses([]*gogitlab.CommitStatus{{Name: "lint", Status: "success"}}); len(got) != 0 {
		t.Fatalf("tfbuddyStatuses() = %v, want none when only project CI statuses exist", got)
	}
}
