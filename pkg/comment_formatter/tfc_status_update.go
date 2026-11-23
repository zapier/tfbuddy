package comment_formatter

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/hashicorp/go-tfe"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/terraform_plan"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/utils"
)

func getProperApplyText(rmd runstream.RunMetadata, wsName string) string {
	if rmd.GetAutoMerge() {
		return fmt.Sprintf(howToApplyFormat, wsName, autoMRMergeSnippet)
	} else {
		return fmt.Sprintf(howToApplyFormat, wsName, manualMRMergeSnippet)
	}
}
func getProperTargetedApplyText(rmd runstream.RunMetadata, run *tfe.Run, wsName string) string {
	targets := strings.Join(run.TargetAddrs, ",")
	if rmd.GetAutoMerge() {
		return fmt.Sprintf(howToApplyFormatWithTarget, targets, wsName, targets, autoMRMergeSnippet)
	} else {
		return fmt.Sprintf(howToApplyFormatWithTarget, targets, wsName, targets, manualMRMergeSnippet)
	}
}
func FormatRunStatusCommentBody(cfg config.Config, tfc tfc_api.ApiClient, run *tfe.Run, rmd runstream.RunMetadata) (main, toplevel string, resolve bool) {
	wsName := run.Workspace.Name
	org := run.Workspace.Organization.Name
	runUrl := fmt.Sprintf("https://app.terraform.io/app/%s/workspaces/%s/runs/%s", org, wsName, run.ID)

	extraInfo := ""
	resolveDiscussion := false

	switch run.Status {
	case tfe.RunPending:
		// TODO: check our run is the "current" run
		// There could be another run waiting for confirmation that prevents our run from starting.
		if run.PositionInQueue > 0 {
			extraInfo = fmt.Sprintf("Position in Queue: %d", run.PositionInQueue)
		}
	case tfe.RunApplying:
		// no extra info
	case tfe.RunApplied:
		extraInfo = fmt.Sprintf(successPlanSummaryFormat, run.Apply.ResourceImports, run.Apply.ResourceAdditions, run.Apply.ResourceChanges, run.Apply.ResourceDestructions)
		if len(run.TargetAddrs) > 0 {
			extraInfo += needToApplyFullWorkSpace
			extraInfo += getProperApplyText(rmd, wsName)
		} else {
			resolveDiscussion = true
		}
	case tfe.RunDiscarded:
		// no extra info
	case tfe.RunErrored:
		if rmd.GetAction() == runstream.PlanAction {
			extraInfo += failedPlanSummaryFormat
		}

	case tfe.RunPlanning:
		// no extra info
		if run.AutoApply {
			extraInfo = "Auto Apply Enabled - plan will automatically Apply if it passes policy checks."
		}
	case tfe.RunPlanned:
		extraInfo = fmt.Sprintf(successPlanSummaryFormat, run.Apply.ResourceImports, run.Plan.ResourceAdditions, run.Plan.ResourceChanges, run.Plan.ResourceDestructions)
		if !run.AutoApply {
			if len(run.TargetAddrs) > 0 {
				extraInfo += getProperTargetedApplyText(rmd, run, wsName)
			} else {
				extraInfo += getProperApplyText(rmd, wsName)
			}
		}
	case tfe.RunPlannedAndFinished:
		// The plan JSON holds every value in the plan, sensitive ones included,
		// so it is only parsed here and never logged, at any level.
		b, err := tfc.GetPlanOutput(run.Plan.ID)
		if err != nil {
			log.Error().Err(err).Str("runID", run.ID).Str("planID", run.Plan.ID).Msg("could not get plan JSON")
		} else {
			extraInfo += "<br>" + terraform_plan.PresentPlanChangesAsMarkdown(b, runUrl) + "</br>"
		}

		if hasChanges(run.Plan) {
			extraInfo += costEstimateLine(cfg, tfc, run)
			if len(run.TargetAddrs) > 0 {
				extraInfo += getProperTargetedApplyText(rmd, run, wsName)
			} else {
				extraInfo += getProperApplyText(rmd, wsName)
			}
		} else {
			if len(run.TargetAddrs) > 0 {
				extraInfo += needToApplyFullWorkSpace
				extraInfo += getProperApplyText(rmd, wsName)
			} else {
				resolveDiscussion = true
			}
		}

	case tfe.RunPolicySoftFailed:
		// Policy checks executed and soft-failed; approval required in TFC UI
		log.Trace().Str("project", rmd.GetMRProjectNameWithNamespace()).Int("mergeRequestID", rmd.GetMRInternalID()).Msg("policy soft failed")
		if cfg.FailCIOnSentinelSoftFail {
			extraInfo = "Policy Checks: Soft Failed. Review plan and make changes to pass policy checks."
		} else {
			extraInfo = "Policy Checks: Soft Failed — approval required in Terraform Cloud before apply can proceed."
		}
	case tfe.RunPolicyChecked:
		// Policy checks completed successfully
		extraInfo = "Policy Checks: Passed."
		if !run.AutoApply {
			extraInfo += " Plan requires confirmation through the Terraform Cloud console. Click Run URL link to open & confirm."
		}

	default:
		log.Debug().Str("project", rmd.GetMRProjectNameWithNamespace()).Int("mergeRequestID", rmd.GetMRInternalID()).Str("run_status", string(run.Status)).Msg("No action defined for status.")
		return
	}

	topLevelNoteBody := fmt.Sprintf(
		MR_RUN_DETAILS_FORMAT,
		wsName,
		rmd.GetAction(),
		run.Status,
		run.ID, runUrl,
	) + "\n" + utils.FormatTFBuddyMarker(wsName, rmd.GetAction())

	return extraInfo, topLevelNoteBody, resolveDiscussion

}

// costEstimateLine returns "" unless the run has a finished estimate whose
// monthly change is at least cfg.CostEstimateMinDelta dollars either way.
func costEstimateLine(cfg config.Config, tfc tfc_api.ApiClient, run *tfe.Run) string {
	if cfg.CostEstimateMinDelta <= 0 || run.CostEstimate == nil || run.CostEstimate.ID == "" {
		return ""
	}
	ce, err := tfc.GetCostEstimate(context.Background(), run.CostEstimate.ID)
	if err != nil {
		log.Error().Err(err).Str("runID", run.ID).Str("costEstimateID", run.CostEstimate.ID).Msg("could not get cost estimate")
		return ""
	}
	if ce == nil || ce.Status != tfe.CostEstimateFinished {
		return ""
	}
	delta, err := strconv.ParseFloat(ce.DeltaMonthlyCost, 64)
	if err != nil || math.Abs(delta) < float64(cfg.CostEstimateMinDelta) {
		return ""
	}
	return fmt.Sprintf(costEstimateFormat, formatUSD(delta))
}

// formatUSD renders a signed dollar amount, e.g. +$1,582.56 or -$4,410.72.
func formatUSD(v float64) string {
	sign := "+"
	if v < 0 {
		sign, v = "-", -v
	}
	whole, cents, _ := strings.Cut(strconv.FormatFloat(v, 'f', 2, 64), ".")
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "," + whole[i:]
	}
	return sign + "$" + whole + "." + cents
}

func hasChanges(plan *tfe.Plan) bool {
	if plan.ResourceAdditions > 0 {
		return true
	}
	if plan.ResourceDestructions > 0 {
		return true
	}
	if plan.ResourceChanges > 0 {
		return true
	}
	return false
}

const MR_COMMENT_FORMAT = `
### Terraform Cloud
%s
`
const MR_RUN_DETAILS_FORMAT = `
### Terraform Cloud
**Workspace**: ` + "`%s`" + `<br>
**Command**: %s <br>
**Status**: ` + "`%s`" + `<br>
**Run URL**: [%s](%s) <br>
`

var failedPlanSummaryFormat = `
*Click Terraform Cloud URL to see detailed plan output*
`

var successPlanSummaryFormat = `
  * Imports: %d
  * Additions: %d
  * Changes: %d
  * Destructions: %d`

var costEstimateFormat = `

:moneybag: **Cost estimate:** %s/mo at list price. Terraform Cloud prices [only some resource types](https://developer.hashicorp.com/terraform/cloud-docs/cost-estimation) and no usage-based costs.
`

var manualMRMergeSnippet = `Remember to **merge** the MR once the apply has succeeded`
var autoMRMergeSnippet = `Your MR will be **automatically** merged once the apply has succeeded`
var howToApplyFormat = `

---
* To **apply** the plan for all workspaces, comment:
	> ` + "`tfc apply`" + `

* To **apply** the plan for this workspace only, comment:
	> ` + "`tfc apply -w %s`" + `

%s`

var howToApplyFormatWithTarget = `

---
* To **apply** the plan for all workspaces, comment:
	> ` + "`tfc apply -t %s`" + `

* To **apply** the plan for this workspace only, comment:
	> ` + "`tfc apply -w %s -t %s`" + `

%s`

var needToApplyFullWorkSpace = `

**Need to Apply Full Workspace Before Merging**
`

const OLD_RUN_BLOCK = `
### Previous TFC URLS

| Run ID | Status | Created at |
| ------ | ------ | ---------- |
%s`
