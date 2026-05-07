package tfc_trigger

import (
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/internal/config"
)

const DefaultTfcOrganizationEnvName = "TFBUDDY_DEFAULT_TFC_ORGANIZATION"

// getWorkspaceAllowDenyList returns a list of allowed and denied workspaces
func getWorkspaceAllowDenyList(cfg config.Config) ([]string, []string) {
	// if a workspace in the allow list does not include a org component, prepend this value (if defined).
	workspaceAllowList := make([]string, 0)
	workspaceDenyList := make([]string, 0)
	defaultOrg := cfg.DefaultTFCOrganization

	for _, w := range cfg.WorkspaceAllowList {
		ws := strings.ToLower(strings.TrimSpace(w))
		if !strings.Contains(ws, "/") {
			ws = defaultOrg + "/" + ws
		}
		log.Info().Str("workspace", ws).Msg("adding Workspace to allow list")
		workspaceAllowList = append(workspaceAllowList, ws)
	}

	for _, w := range cfg.WorkspaceDenyList {
		ws := strings.ToLower(strings.TrimSpace(w))
		if !strings.Contains(ws, "/") {
			ws = defaultOrg + "/" + ws
		}
		log.Info().Str("workspace", ws).Msg("adding Workspace to deny list")
		workspaceDenyList = append(workspaceDenyList, ws)
	}
	return workspaceAllowList, workspaceDenyList
}

func isWorkspaceAllowed(cfg config.Config, workspace, org string) bool {
	workspaceAllowList, workspaceDenyList := getWorkspaceAllowDenyList(cfg)
	fullName := org + "/" + workspace
	for _, denied := range workspaceDenyList {
		if fullName == denied {
			log.Info().Str("workspace", fullName).Msg("workspace in deny list")
			return false
		}
	}

	for _, allowed := range workspaceAllowList {
		if fullName == allowed {
			log.Debug().Str("workspace", fullName).Msg("workspace in allow list")
			return true
		}
	}

	// not found in deny or allow list, if we have a whitelist set, deny this workspace, otherwise allow.
	return len(workspaceAllowList) == 0
}
