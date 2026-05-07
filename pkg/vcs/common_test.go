package vcs

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/zapier/tfbuddy/internal/config"
)

func TestIsGlobalAutoMergeEnabledReadsViper(t *testing.T) {
	viper.Reset()
	config.Init()
	t.Cleanup(func() {
		viper.Reset()
		config.Init()
	})
	viper.Set(config.KeyAllowAutoMerge, false)
	config.Reload()

	if IsGlobalAutoMergeEnabled() {
		t.Fatal("IsGlobalAutoMergeEnabled() = true, want false")
	}
}
