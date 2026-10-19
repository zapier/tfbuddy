package tfc_utils

import (
	"context"
	"log"
	"os"
	"strings"
	"time"

	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"github.com/zapier/tfbuddy/pkg/vcs/gitlab"

	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

const statusPollInterval = 15 * time.Second

// MonitorRunStatus is the body of the `tfc status` CI job. It waits for TFBuddy
// to publish at least one commit status for the pipeline's commit and fails if
// none appears within the configured wait. No status means TFBuddy never saw
// the commit, usually because the GitLab webhook for the push was not
// delivered, and without this check the merge request pipeline would be green
// with no Terraform plan behind it.
//
// Whether those runs pass or fail is not this job's concern: TFBuddy publishes
// the outcome onto the same pipeline (a failed plan fails it, a pending apply
// keeps it running), so the pipeline already carries that signal.
func MonitorRunStatus() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("error loading config: %v", err)
	}
	glClient := gitlab.NewGitlabClient(cfg)
	ctx := context.Background()

	projectID := os.Getenv("CI_PROJECT_ID")
	sha := os.Getenv("CI_COMMIT_SHA")

	deadline := time.Now().Add(cfg.StatusTriggerWait)
	for {
		statuses := tfbuddyStatuses(glClient.GetCommitStatuses(ctx, projectID, sha))
		if len(statuses) > 0 {
			for _, s := range statuses {
				log.Printf("%s: %s %s", s.Name, s.Status, s.TargetURL)
			}
			log.Printf("TFBuddy has seen %s.", sha)
			return
		}
		if time.Now().After(deadline) {
			log.Fatalf("TFBuddy published no commit status for %s within %s. "+
				"It did not see this commit (the GitLab webhook may not have been delivered). "+
				"Comment `tfc plan` on the merge request, then retry this job.",
				sha, cfg.StatusTriggerWait)
		}
		log.Printf("no TFBuddy status on %s yet; waiting", sha)
		time.Sleep(statusPollInterval)
	}
}

// tfbuddyStatuses keeps only the statuses TFBuddy publishes; everything else on
// the commit is the project's own CI.
func tfbuddyStatuses(statuses []*gogitlab.CommitStatus) []*gogitlab.CommitStatus {
	var out []*gogitlab.CommitStatus
	for _, s := range statuses {
		if strings.HasPrefix(s.Name, vcs.CommitStatusPrefix) {
			out = append(out, s)
		}
	}
	return out
}
