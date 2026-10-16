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

// Terminal states a TFBuddy commit status can reach. Anything else (pending,
// running, created) is still in flight.
const (
	statusSuccess  = "success"
	statusFailed   = "failed"
	statusCanceled = "canceled"
)

// How often the job re-reads commit statuses while waiting, and the ceiling
// for a plan that TFBuddy has started but not finished.
const (
	statusPollInterval = 15 * time.Second
	planWait           = 60 * time.Minute
)

// verdict is what MonitorRunStatus decided about a commit's TFBuddy statuses.
type verdict int

const (
	// verdictWaiting: TFBuddy has not published anything for the commit yet.
	verdictWaiting verdict = iota
	// verdictPlanning: TFBuddy has started at least one plan that is not done.
	verdictPlanning
	// verdictFailed: a plan or apply ended in failure.
	verdictFailed
	// verdictOK: every plan finished without failure. A pending apply is not
	// waited for; it is the user's `tfc apply` comment that clears it, and the
	// pending status already keeps the pipeline from reporting success.
	verdictOK
)

// MonitorRunStatus is the body of the `tfc status` CI job. It fails the job
// when TFBuddy never published a commit status for the pipeline's commit, which
// is the case when the webhook for the push was not delivered, and when a plan
// TFBuddy did run failed. Both would otherwise leave the merge request pipeline
// green with no Terraform plan behind it.
func MonitorRunStatus() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("error loading config: %v", err)
	}
	glClient := gitlab.NewGitlabClient(cfg)
	ctx := context.Background()

	projectID := os.Getenv("CI_PROJECT_ID")
	sha := os.Getenv("CI_COMMIT_SHA")

	triggerDeadline := time.Now().Add(cfg.StatusTriggerWait)
	planDeadline := time.Now().Add(planWait)
	for {
		statuses := tfbuddyStatuses(glClient.GetCommitStatuses(ctx, projectID, sha))
		v := evaluate(statuses)
		for _, s := range statuses {
			log.Printf("%s: %s %s", s.Name, s.Status, s.TargetURL)
		}

		switch v {
		case verdictOK:
			log.Printf("TFBuddy planned %s; see the merge request for plan output.", sha)
			return
		case verdictFailed:
			log.Fatalf("a TFBuddy run for %s failed; fix the plan and push again.", sha)
		case verdictWaiting:
			if time.Now().After(triggerDeadline) {
				log.Fatalf("TFBuddy published no commit status for %s within %s. "+
					"It did not see this commit (the GitLab webhook may not have been delivered). "+
					"Comment `tfc plan` on the merge request, then retry this job.",
					sha, cfg.StatusTriggerWait)
			}
			log.Printf("no TFBuddy status on %s yet; waiting", sha)
		case verdictPlanning:
			if time.Now().After(planDeadline) {
				log.Fatalf("a TFBuddy plan for %s has not finished within %s.", sha, planWait)
			}
		}
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

// evaluate turns TFBuddy's statuses for a commit into a verdict. Status names
// are "<prefix><action>/<workspace>"; only the plan action has to settle.
func evaluate(statuses []*gogitlab.CommitStatus) verdict {
	if len(statuses) == 0 {
		return verdictWaiting
	}
	v := verdictOK
	for _, s := range statuses {
		action, _, _ := strings.Cut(strings.TrimPrefix(s.Name, vcs.CommitStatusPrefix), "/")
		switch {
		case s.Status == statusFailed || s.Status == statusCanceled:
			return verdictFailed
		case action == "plan" && s.Status != statusSuccess:
			v = verdictPlanning
		}
	}
	return v
}
