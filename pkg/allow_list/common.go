package allow_list

import (
	"os"
	"strings"

	"github.com/rs/zerolog/log"
)

func getAllowList(envVar string) []string {
	allowed := strings.TrimSpace(os.Getenv(envVar))

	if allowed != "" {
		allowedParts := strings.Split(allowed, ",")
		allowList := make([]string, 0)
		for _, p := range allowedParts {
			prefix := strings.TrimSpace(p)
			log.Info().Str("prefix", prefix).Msg("adding repo prefix to allow list")
			allowList = append(allowList, prefix)

		}
		return allowList
	}

	return nil
}
