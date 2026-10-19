package tfc_utils

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
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
			msg := fmt.Sprintf("TFBuddy published no commit status for %s within %s. "+
				"It did not see this commit (the GitLab webhook may not have been delivered). "+
				"Comment `tfc plan` on the merge request, then retry this job.",
				sha, cfg.StatusTriggerWait)
			postMissingPlanComment(ctx, glClient, projectID, sha, cfg.StatusTriggerWait)
			log.Fatal(msg)
		}
		log.Printf("no TFBuddy status on %s yet; waiting", sha)
		time.Sleep(statusPollInterval)
	}
}

// postMissingPlanComment tells the user on the merge request what to do, since
// the job log is not where they are looking and `tfc plan` is typed there. The
// marker keeps a retried job from posting the same comment twice for one SHA.
func postMissingPlanComment(ctx context.Context, gl *gitlab.GitlabClient, projectID, sha string, waited time.Duration) {
	mrIID, err := strconv.Atoi(os.Getenv("CI_MERGE_REQUEST_IID"))
	if err != nil {
		log.Printf("not a merge request pipeline; skipping the merge request comment")
		return
	}
	marker := fmt.Sprintf("<!-- tfbuddy-status-missing-plan %s -->", sha)
	for _, n := range gl.ListMergeRequestNotes(ctx, mrIID, projectID) {
		if strings.Contains(n.Body, marker) {
			return
		}
	}
	body := fmt.Sprintf(":no_entry: **TFBuddy did not plan commit `%s`.**\n\n"+
		"No TFBuddy status appeared on this commit within %s, which usually means GitLab did not deliver the webhook for the push. "+
		"Nothing has been planned for it.\n\n"+
		"To recover: comment `tfc plan` on this merge request, wait for the plan, then retry the `tfc:status` job.\n%s",
		sha[:8], waited, marker)
	if err := gl.CreateMergeRequestComment(ctx, mrIID, projectID, body); err != nil {
		log.Printf("could not comment on merge request !%d: %v", mrIID, err)
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
