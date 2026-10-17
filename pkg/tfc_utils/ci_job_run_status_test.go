package tfc_utils

import (
	"testing"

	gogitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/zapier/tfbuddy/pkg/vcs"
)

func status(action, ws, state string) *gogitlab.CommitStatus {
	return &gogitlab.CommitStatus{Name: vcs.CommitStatusPrefix + action + "/" + ws, Status: state}
}

func Test_evaluate(t *testing.T) {
	tests := []struct {
		name     string
		statuses []*gogitlab.CommitStatus
		want     verdict
	}{
		{name: "no statuses: TFBuddy never saw the commit", want: verdictWaiting},
		{name: "plan running", statuses: []*gogitlab.CommitStatus{status("plan", "a", "running")}, want: verdictPlanning},
		{name: "plan pending", statuses: []*gogitlab.CommitStatus{status("plan", "a", "pending")}, want: verdictPlanning},
		{name: "plan failed", statuses: []*gogitlab.CommitStatus{status("plan", "a", "failed"), status("apply", "a", "pending")}, want: verdictFailed},
		{name: "plan done, apply pending is not waited for", statuses: []*gogitlab.CommitStatus{status("plan", "a", "success"), status("apply", "a", "pending")}, want: verdictOK},
		{name: "apply failed", statuses: []*gogitlab.CommitStatus{status("plan", "a", "success"), status("apply", "a", "failed")}, want: verdictFailed},
		{name: "two workspaces, one still planning", statuses: []*gogitlab.CommitStatus{status("plan", "a", "success"), status("plan", "b", "running")}, want: verdictPlanning},
		{name: "canceled counts as failed", statuses: []*gogitlab.CommitStatus{status("plan", "a", "canceled")}, want: verdictFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evaluate(tt.statuses); got != tt.want {
				t.Errorf("evaluate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_tfbuddyStatusesIgnoresProjectCI(t *testing.T) {
	in := []*gogitlab.CommitStatus{
		{Name: "lint", Status: "failed"},
		status("plan", "a", "success"),
		{Name: "tfc:status", Status: "running"},
	}
	got := tfbuddyStatuses(in)
	if len(got) != 1 || got[0].Name != vcs.CommitStatusPrefix+"plan/a" {
		t.Fatalf("tfbuddyStatuses() = %v, want only the TFBuddy plan status", got)
	}
}
