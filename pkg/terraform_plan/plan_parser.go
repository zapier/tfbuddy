package terraform_plan

import (
	"bytes"
	_ "embed"
	"fmt"
	"text/template"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/rs/zerolog/log"
)

//go:embed templates/plan_output.tpl
var planTemplate []byte

func parseJSONPlan(b []byte) (*tfjson.Plan, error) {
	plan := &tfjson.Plan{}
	err := plan.UnmarshalJSON(b)
	if err != nil {
		log.Error().Err(err).Send()
		return nil, err
	}
	return plan, nil
}

func PresentPlanChangesAsMarkdown(b []byte, tfcUrl string) string {
	plan, err := parseJSONPlan(b)
	if err != nil {
		return ""
	}

	tplData := PlanTemplateData{
		Changes:      map[string][]*ResourceChange{},
		Replacements: map[string][]*ResourceChange{},
		TfcUrl:       tfcUrl,
	}
	for _, chg := range plan.ResourceChanges {
		switch {
		case chg.Change.Actions.NoOp():
			continue

		case chg.Change.Actions.Import():
			tplData.ImportCount += 1
			tplData.Imports = append(tplData.Additions, chg.Address)

		case chg.Change.Actions.Create():
			tplData.AdditionCount += 1
			tplData.Additions = append(tplData.Additions, chg.Address)

		case chg.Change.Actions.Update():
			tplData.ChangeCount += 1
			tplData.Changes[chg.Address] = processChanges(chg)

		case chg.Change.Actions.Delete():
			tplData.DestructionCount += 1
			tplData.Destructions = append(tplData.Destructions, chg.Address)

		case chg.Change.Actions.Replace():
			tplData.ReplacementCount += 1
			tplData.Replacements[chg.Address] = processChanges(chg)
		}
	}

	t := template.Must(template.New("plan").Parse(string(planTemplate)))

	outputBuffer := &bytes.Buffer{}
	t.Execute(outputBuffer, tplData)
	return outputBuffer.String()
}

func processChanges(chg *tfjson.ResourceChange) []*ResourceChange {
	beforeMap := chg.Change.Before.(map[string]interface{})
	afterMap := chg.Change.After.(map[string]interface{})
	afterUnknownMap := chg.Change.AfterUnknown.(map[string]interface{})

	afterSensitiveMap := map[string]interface{}{}
	if chg.Change.AfterSensitive != nil {
		afterSensitiveMap = chg.Change.AfterSensitive.(map[string]interface{})
	}
	changes := []*ResourceChange{}
	for k, bv := range beforeMap {
		rc := &ResourceChange{
			Field:  k,
			Before: fmt.Sprintf("%v", bv),
		}
		if av, ok := afterMap[k]; ok && fmt.Sprintf("%v", bv) != fmt.Sprintf("%v", bv) {
			rc.After = fmt.Sprintf("%v", av)

		} else if _, ok := afterSensitiveMap[k]; ok {
			rc.After = "*sensitive value*"

		} else if _, ok := afterUnknownMap[k]; ok {
			rc.After = "*known after apply*"

		} else {
			switch {
			case isReplace(chg):
				if rc.After == "" {
					rc.After = rc.Before
				}
				fallthrough
			case chg.Change.Actions[0] == tfjson.ActionCreate:
				changes = append(changes, rc)

			case chg.Change.Actions[0] == tfjson.ActionUpdate:
				if rc.Before != rc.After {
					changes = append(changes, rc)
				}
			}
		}
	}
	return changes
}

func isReplace(chg *tfjson.ResourceChange) bool {
	if len(chg.Change.Actions) < 2 {
		return false
	}
	matchedCriteria := 0
	for _, v := range chg.Change.Actions {
		if v == tfjson.ActionCreate || v == tfjson.ActionDelete {
			matchedCriteria += 1
		}
	}
	return matchedCriteria == 2
}

type PlanTemplateData struct {
	ImportCount    int
	Imports        []string
	AdditionCount    int
	Additions        []string
	ChangeCount      int
	Changes          map[string][]*ResourceChange
	DestructionCount int
	Destructions     []string
	ReplacementCount int
	Replacements     map[string][]*ResourceChange
	TfcUrl           string
}

type ResourceChange struct {
	Field             string
	Before            string
	After             string
	ForcesReplacement bool
}
