package vcs

import (
	"errors"

	"github.com/zapier/tfbuddy/internal/config"
)

// ErrRunSuperseded means a newer run of the same workspace and action has
// already replaced this run's comments, or this run's final status was
// already handled. Posting its final comment would only add a stale result.
var ErrRunSuperseded = errors.New("run superseded by a newer run of the same workspace and action")

func IsGlobalAutoMergeEnabled(cfg config.Config) bool {
	return cfg.AllowAutoMerge
}
