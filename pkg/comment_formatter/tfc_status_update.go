package comment_formatter

import (
	"fmt"
	"strings"

	"github.com/hashicorp/go-tfe"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/terraform_plan"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
)

func FormatRunStatusCommentBody(tfc tfc_api.ApiClient, run *tfe.Run, rmd runstream.RunMetadata) (main, toplevel string, resolve bool) {
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
			extraInfo += fmt.Sprintf(howToApplyFormat, wsName)
		} else {
			resolveDiscussion = true
		}
	case tfe.RunDiscarded:
		// no extra info
	case tfe.RunErrored:
		if rmd.GetAction() == "plan" {
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
				extraInfo += fmt.Sprintf(howToApplyFormatWithTarget, strings.Join(run.TargetAddrs, ","), wsName, strings.Join(run.TargetAddrs, ","))
			} else {
				extraInfo += fmt.Sprintf(howToApplyFormat, wsName)
			}
		}
	case tfe.RunPlannedAndFinished:
		log.Trace().Interface("plan", run.Plan).Msg("planned_and_finished")

		b, err := tfc.GetPlanOutput(run.Plan.ID)
		if err != nil {
			log.Error().Err(err).Msg("could not get plan JSON")
		} else {
			extraInfo += "<br>" + terraform_plan.PresentPlanChangesAsMarkdown(b, runUrl) + "</br>"
		}
		log.Trace().Str("plan_id", run.Plan.ID).Str("plan_json", string(b)).Msg("")

		if hasChanges(run.Plan) {
			if len(run.TargetAddrs) > 0 {
				extraInfo += fmt.Sprintf(howToApplyFormatWithTarget, strings.Join(run.TargetAddrs, ","), wsName, strings.Join(run.TargetAddrs, ","))
			} else {
				extraInfo += fmt.Sprintf(howToApplyFormat, wsName)
			}
		} else {
			if len(run.TargetAddrs) > 0 {
				extraInfo += needToApplyFullWorkSpace
				extraInfo += fmt.Sprintf(howToApplyFormat, wsName)
			} else {
				resolveDiscussion = true
			}
		}

	case tfe.RunPolicySoftFailed:
		// no extra info
		extraInfo = "The plan has soft failed policy checks, please open TFC URL to approve."

	case tfe.RunPolicyChecked:
		if !run.AutoApply {
			extraInfo = "Plan requires confirmation through the Terraform Cloud console. Click Run URL link to open & confirm."
		}

	default:
		log.Debug().Str("run_status", string(run.Status)).Msg("No action defined for status.")
		return
	}

	topLevelNoteBody := fmt.Sprintf(
		MR_RUN_DETAILS_FORMAT,
		wsName,
		rmd.GetAction(),
		run.Status,
		runUrl, runUrl,
	)

	return extraInfo, topLevelNoteBody, resolveDiscussion

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

var howToApplyFormat = `

---
* To **apply** the plan for all workspaces, comment:
	> ` + "`tfc apply`" + `

* To **apply** the plan for this workspace only, comment:
	> ` + "`tfc apply -w %s`" + `

Remember to **merge** the MR once the apply has succeeded`

var howToApplyFormatWithTarget = `

---
* To **apply** the plan for all workspaces, comment:
	> ` + "`tfc apply -t %s`" + `

* To **apply** the plan for this workspace only, comment:
	> ` + "`tfc apply -w %s -t %s`" + `

Remember to **merge** the MR once the apply has succeeded`

var needToApplyFullWorkSpace = `

**Need to Apply Full Workspace Before Merging**
`

const OLD_RUN_BLOCK = `
### Previous TFC URLS

| Run ID | Status |
| ------ | ------ |
%s`
