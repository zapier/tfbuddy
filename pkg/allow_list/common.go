package allow_list

import (
	"strings"

	"github.com/rs/zerolog/log"
)

func getAllowList(allowed []string) []string {
	if len(allowed) > 0 {
		allowList := make([]string, 0, len(allowed))
		for _, prefix := range allowed {
			prefix = strings.TrimSpace(prefix)
			if prefix == "" {
				continue
			}
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
