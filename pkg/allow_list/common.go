package allow_list

import (
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/internal/config"
)

func getAllowList(cfg config.Config, envVar string) []string {
	var allowed []string
	switch envVar {
	case githubRepoAllowListEnv:
		allowed = cfg.GithubRepoAllowList
	case GitlabProjectAllowListEnv:
		allowed = cfg.GitlabProjectAllowList
	case legacyAllowListEnv:
		allowed = cfg.LegacyProjectAllowList
	default:
		return nil
	}

	if len(allowed) > 0 {
		allowList := make([]string, 0, len(allowed))
		for _, prefix := range allowed {
			log.Info().Str("prefix", prefix).Msg("adding repo prefix to allow list")
			allowList = append(allowList, prefix)
		}
		if len(allowList) == 0 {
			return nil
		}
		return allowList
	}

	return nil
}
