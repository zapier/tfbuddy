package allow_list

import (
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/internal/config"
)

func IsGitlabProjectAllowed(cfg config.Config, projectWithNamespace string) bool {
	allowList := getAllowList(cfg.GitlabProjectAllowList)
	if len(allowList) == 0 {
		allowList = getAllowList(cfg.LegacyProjectAllowList)
	}

	if len(allowList) == 0 {
		log.Warn().Str("project", projectWithNamespace).Msg("denying action for project because allow list is not set.")
		return false
	}

	for _, allowed := range allowList {
		if strings.HasPrefix(projectWithNamespace, allowed) {
			log.Debug().Str("project", projectWithNamespace).Msg("project in allow list")
			return true
		}
	}

	log.Warn().Str("project", projectWithNamespace).Msg("denying action for project because not found in allow list.")
	return false
}
