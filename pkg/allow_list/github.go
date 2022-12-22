package allow_list

import (
	"strings"

	"github.com/rs/zerolog/log"
)

const githubRepoAllowListEnv = "TFBUDDY_GITHUB_REPO_ALLOW_LIST"

func IsGithubRepoAllowed(fullName string) bool {
	githubAllowList := getAllowList(githubRepoAllowListEnv)
	if len(githubAllowList) == 0 {
		log.Warn().Str("repo", fullName).Msg("denying action for repo because allow list is not set.")
		return false
	}

	for _, allowed := range githubAllowList {
		if strings.HasPrefix(fullName, allowed) {
			log.Debug().Str("repo", fullName).Msg("repo in allow list")
			return true
		}
	}

	log.Warn().Str("repo", fullName).Msg("denying action for repo because not found in allow list.")
	return false
}
