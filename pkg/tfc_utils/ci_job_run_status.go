package tfc_utils

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/vcs/gitlab"
	"go.opentelemetry.io/otel"

	tfe "github.com/hashicorp/go-tfe"
	gogitlab "github.com/xanzy/go-gitlab"
)

const TFC_RUN_STATUS_PREFIX = `Terraform Cloud/`
const TFC_POLICY_STATUS_PREFIX = `sentinel/`
const TFC_NO_CHANGE = "Run not triggered: Terraform working directories did not change."

var (
	glClient  *gitlab.GitlabClient
	tfcClient tfc_api.ApiClient
)

const MR_COMMENT_FORMAT = `
### Terraform Cloud
%s
`
const MR_RUN_DETAILS_FORMAT = `
#### Workspace: %s

**Status**: %s

[%s](%s)

%s
---
`

func MonitorRunStatus() {
	glClient = gitlab.NewGitlabClient()
	tfcClient = tfc_api.NewTFCClient()
	ctx := context.Background()

	projectID := os.Getenv("CI_PROJECT_ID")
	sha := os.Getenv("CI_COMMIT_SHA")
	statuses := glClient.GetCommitStatuses(ctx, projectID, sha)
	commentBody := ""
	wg := sync.WaitGroup{}
	for _, s := range statuses {
		if strings.HasPrefix(s.Name, TFC_RUN_STATUS_PREFIX) {
			workspace := strings.Replace(s.Name, TFC_RUN_STATUS_PREFIX, "", 1)
			if s.Description != TFC_NO_CHANGE {
				// extract runID from targetURL
				urlParts := strings.SplitAfter(s.TargetURL, "/")
				runID := urlParts[len(urlParts)-1]

				description := s.Description
				switch s.Status {
				case "pending":
					description = ""
					commentBody += fmt.Sprintf(MR_RUN_DETAILS_FORMAT, workspace, s.Status, s.TargetURL, s.TargetURL, description)
					st := s
					wg.Add(1)
					go waitForRunCompletionOrFailure(ctx, &wg, st, workspace, runID)
				case "success":
					fallthrough
				case "failed":
					// get run summary
					postRunSummary(ctx, s, workspace, runID)
				}
			}
		}
	}
	postCommentBody(ctx, commentBody)

	wg.Wait()
}

func postCommentBody(ctx context.Context, commentBody string) {
	if commentBody != "" {
		projectID := os.Getenv("CI_PROJECT_ID")
		mrIID, err := strconv.Atoi(os.Getenv("CI_MERGE_REQUEST_IID"))
		if err != nil {
			log.Printf("erroring posting comment: %v", err)
		}
		glClient.CreateMergeRequestComment(ctx, mrIID, projectID, fmt.Sprintf(MR_COMMENT_FORMAT, commentBody))
	}
}

var successPlanSummaryFormat = `
  * Additions: %d
  * Changes: %d
  * Destructions: %d

*Click Terraform Cloud URL to see detailed plan output*

**Merge MR to apply changes**
`

var failedPlanSummaryFormat = `
*Click Terraform Cloud URL to see detailed plan output*
`

func postRunSummary(ctx context.Context, commitStatus *gogitlab.CommitStatus, wsName, runID string) {
	//run, _ := tfcClient.Client.Runs.ReadWithOptions(context.Background(), runID, &tfe.RunReadOptions{Include: "plan"})
	run, err := tfcClient.GetRun(ctx, runID)
	if err != nil {
		log.Printf("err: %v\n", err)
		return
	}

	description := ""

	switch run.Status {
	case tfe.RunErrored:
		description = failedPlanSummaryFormat
	default:
		description = fmt.Sprintf(successPlanSummaryFormat, run.Plan.ResourceAdditions, run.Plan.ResourceChanges, run.Plan.ResourceDestructions)
	}

	commentBody := fmt.Sprintf(MR_RUN_DETAILS_FORMAT, wsName, run.Status, commitStatus.TargetURL, commitStatus.TargetURL, description)
	postCommentBody(ctx, commentBody)
}

func waitForRunCompletionOrFailure(ctx context.Context, wg *sync.WaitGroup, commitStatus *gogitlab.CommitStatus, wsName, runID string) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "waitForRunCompletionOrFailure")
	defer span.End()
	defer wg.Done()

	attempts := 360
	retryInterval := 10 * time.Second

	for i := 0; i < attempts; i++ {
		time.Sleep(retryInterval)

		log.Println("Reading Run details.", runID)
		run, err := tfcClient.GetRun(ctx, runID)
		if err != nil {
			log.Printf("err: %v\n", err)
		}

		printRunInfo(run, "Run Details", wsName)
		if isRunning(run) {
			continue
		}

		postRunSummary(ctx, commitStatus, wsName, runID)
		break
	}
}

func isRunning(run *tfe.Run) bool {
	// Get current run
	if run == nil {
		return false
	}

	switch run.Status {
	case "apply_queued":
		fallthrough
	case "applying":
		fallthrough
	case "cost_estimating":
		fallthrough
	case "plan_queued":
		fallthrough
	case "policy_checking":
		fallthrough
	case "planning":
		fallthrough
	case "pending":
		return true
	}

	return false
}
