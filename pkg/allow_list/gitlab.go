package allow_list

import (
	"strings"

	"github.com/rs/zerolog/log"
)

const legacyAllowListEnv = "TFBUDDY_PROJECT_ALLOW_LIST"
const GitlabProjectAllowListEnv = "TFBUDDY_GITLAB_PROJECT_ALLOW_LIST"

func IsGitlabProjectAllowed(projectWithNamespace string) bool {
	allowList := getAllowList(GitlabProjectAllowListEnv)
	if len(allowList) == 0 {
		allowList = getAllowList(legacyAllowListEnv)
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
