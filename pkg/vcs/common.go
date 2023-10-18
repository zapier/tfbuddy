package vcs

import "os"

const TF_BUDDY_AUTO_MERGE = "TFBUDDY_ALLOW_AUTO_MERGE"

func IsGlobalAutoMergeEnabled() bool {
	//empty or true will permit auto merge.
	return os.Getenv(TF_BUDDY_AUTO_MERGE) != "false"
}
