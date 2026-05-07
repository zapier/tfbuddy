package vcs

import "github.com/zapier/tfbuddy/internal/config"

func IsGlobalAutoMergeEnabled(cfg config.Config) bool {
	return cfg.AllowAutoMerge
}
