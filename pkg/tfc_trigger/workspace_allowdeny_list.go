package tfc_trigger

import (
	"os"
	"strings"

	"github.com/rs/zerolog/log"
)

const DefaultTfcOrganizationEnvName = "TFBUDDY_DEFAULT_TFC_ORGANIZATION"

// getWorkspaceAllowDenyList returns a list of allowed and denied workspaces
func getWorkspaceAllowDenyList() ([]string, []string) {
	// if a workspace in the allow list does not include a org component, prepend this value (if defined).
	workspaceAllowList := make([]string, 0)
	workspaceDenyList := make([]string, 0)
	defaultOrg := os.Getenv(DefaultTfcOrganizationEnvName)

	allowEnv := os.Getenv("TFBUDDY_WORKSPACE_ALLOW_LIST")
	if allowEnv != "" {
		allowed := strings.Split(allowEnv, ",")
		for _, w := range allowed {
			ws := strings.TrimSpace(w)
			ws = strings.ToLower(ws)
			if !strings.Contains(ws, "/") {
				ws = defaultOrg + "/" + ws
			}
			log.Info().Str("workspace", ws).Msg("adding Workspace to allow list")
			workspaceAllowList = append(workspaceAllowList, ws)
		}
	}

	denyEnv := os.Getenv("TFBUDDY_WORKSPACE_DENY_LIST")
	denied := strings.Split(denyEnv, ",")
	for _, w := range denied {
		ws := strings.TrimSpace(w)
		ws = strings.ToLower(ws)
		if !strings.Contains(ws, "/") {
			ws = defaultOrg + "/" + ws
		}
		log.Info().Str("workspace", ws).Msg("adding Workspace to deny list")
		workspaceDenyList = append(workspaceDenyList, ws)
	}
	return workspaceAllowList, workspaceDenyList
}

func isWorkspaceAllowed(workspace, org string) bool {
	workspaceAllowList, workspaceDenyList := getWorkspaceAllowDenyList()
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
