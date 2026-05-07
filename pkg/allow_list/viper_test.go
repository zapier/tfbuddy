package allow_list

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/zapier/tfbuddy/internal/config"
)

func TestIsGithubRepoAllowedReadsViper(t *testing.T) {
	viper.Reset()
	config.Init()
	t.Cleanup(func() {
		viper.Reset()
		config.Init()
	})
	viper.Set(config.KeyGithubRepoAllowList, "org")
	config.Reload()

	if !IsGithubRepoAllowed("org/repo") {
		t.Fatal("IsGithubRepoAllowed() = false, want true")
	}
}

func TestIsGitlabProjectAllowedReadsViper(t *testing.T) {
	viper.Reset()
	config.Init()
	t.Cleanup(func() {
		viper.Reset()
		config.Init()
	})
	viper.Set(config.KeyGitlabProjectAllowList, "group")
	config.Reload()

	if !IsGitlabProjectAllowed("group/project") {
		t.Fatal("IsGitlabProjectAllowed() = false, want true")
	}
}
