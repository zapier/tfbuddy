package vcs

import "github.com/zapier/tfbuddy/internal/config"

const TF_BUDDY_AUTO_MERGE = "TFBUDDY_ALLOW_AUTO_MERGE"

func IsGlobalAutoMergeEnabled() bool {
	return config.AutoMergeEnabled()
}
