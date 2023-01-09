package tflocal

import (
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/vcs"
)

type TerraformLocal struct {
	cfg       tfc_trigger.TriggerConfig
	gl        vcs.GitClient
	runstream runstream.StreamClient
}

func (t *TerraformLocal) TriggerTFCEvents() (*tfc_trigger.TriggeredTFCWorkspaces, error) {
	mr, err := t.gl.GetMergeRequest(t.cfg.GetMergeRequestIID(), t.cfg.GetProjectNameWithNamespace())
	if err != nil {
		return nil, err
	}
	_, err = t.getTriggeredWorkspacesForRequest(mr)
	if err != nil {
		return nil, err
	}
	workspaceStatus := &tfc_trigger.TriggeredTFCWorkspaces{
		Errored:  make([]*tfc_trigger.ErroredWorkspace, 0),
		Executed: make([]string, 0),
	}
	//for each workspace run terraform init && terraform ACTION
	return workspaceStatus, nil
}
func (t *TerraformLocal) GetConfig() tfc_trigger.TriggerConfig {
	return nil
}
func (t *TerraformLocal) TriggerCleanupEvent() error {
	return nil
}

func (t *TerraformLocal) getTriggeredWorkspacesForRequest(mr vcs.MR) (*tfc_trigger.TriggeredTFCWorkspaces, error) {
	//Use tf buddy config logic to identify the impacted directories
	return nil, nil
}
