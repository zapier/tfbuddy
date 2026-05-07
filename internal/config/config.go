package config

import (
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

const (
	KeyLogLevel                 = "log-level"
	KeyDevMode                  = "dev-mode"
	KeyOTELEnabled              = "otel-enabled"
	KeyOTELCollectorHost        = "otel-collector-host"
	KeyOTELCollectorPort        = "otel-collector-port"
	KeyGitlabHookSecretKey      = "gitlab-hook-secret-key"
	KeyGithubHookSecretKey      = "github-hook-secret-key"
	KeyDefaultTFCOrganization   = "default-tfc-organization"
	KeyWorkspaceAllowList       = "workspace-allow-list"
	KeyWorkspaceDenyList        = "workspace-deny-list"
	KeyAllowAutoMerge           = "allow-auto-merge"
	KeyFailCIOnSentinelSoftFail = "fail-ci-on-sentinel-soft-fail"
	KeyDeleteOldComments        = "delete-old-comments"
	KeyNATSServiceURL           = "nats-service-url"
	KeyGitlabProjectAllowList   = "gitlab-project-allow-list"
	KeyLegacyProjectAllowList   = "project-allow-list"
	KeyGithubRepoAllowList      = "github-repo-allow-list"
	KeyGithubCloneDepth         = "github-clone-depth"
	KeyGitlabCloneDepth         = "gitlab-clone-depth"
)

type Config struct {
	LogLevel                 string   `mapstructure:"log-level"`
	DevMode                  bool     `mapstructure:"dev-mode"`
	OTELEnabled              bool     `mapstructure:"otel-enabled"`
	OTELCollectorHost        string   `mapstructure:"otel-collector-host"`
	OTELCollectorPort        string   `mapstructure:"otel-collector-port"`
	GitlabHookSecretKey      string   `mapstructure:"gitlab-hook-secret-key"`
	GithubHookSecretKey      string   `mapstructure:"github-hook-secret-key"`
	DefaultTFCOrganization   string   `mapstructure:"default-tfc-organization"`
	WorkspaceAllowList       []string `mapstructure:"workspace-allow-list"`
	WorkspaceDenyList        []string `mapstructure:"workspace-deny-list"`
	AllowAutoMerge           bool     `mapstructure:"allow-auto-merge"`
	FailCIOnSentinelSoftFail bool     `mapstructure:"fail-ci-on-sentinel-soft-fail"`
	DeleteOldComments        bool     `mapstructure:"delete-old-comments"`
	NATSServiceURL           string   `mapstructure:"nats-service-url"`
	GitlabProjectAllowList   []string `mapstructure:"gitlab-project-allow-list"`
	LegacyProjectAllowList   []string `mapstructure:"project-allow-list"`
	GithubRepoAllowList      []string `mapstructure:"github-repo-allow-list"`
	GithubCloneDepth         int      `mapstructure:"github-clone-depth"`
	GitlabCloneDepth         int      `mapstructure:"gitlab-clone-depth"`
}

var C Config

type binding struct {
	key          string
	defaultValue any
}

var bindings = []binding{
	{key: KeyLogLevel, defaultValue: "info"},
	{key: KeyDevMode, defaultValue: false},
	{key: KeyOTELEnabled, defaultValue: false},
	{key: KeyOTELCollectorHost, defaultValue: ""},
	{key: KeyOTELCollectorPort, defaultValue: ""},
	{key: KeyGitlabHookSecretKey, defaultValue: ""},
	{key: KeyGithubHookSecretKey, defaultValue: ""},
	{key: KeyDefaultTFCOrganization, defaultValue: ""},
	{key: KeyWorkspaceAllowList, defaultValue: []string{}},
	{key: KeyWorkspaceDenyList, defaultValue: []string{}},
	{key: KeyAllowAutoMerge, defaultValue: true},
	{key: KeyFailCIOnSentinelSoftFail, defaultValue: false},
	{key: KeyDeleteOldComments, defaultValue: false},
	{key: KeyNATSServiceURL, defaultValue: ""},
	{key: KeyGitlabProjectAllowList, defaultValue: []string{}},
	{key: KeyLegacyProjectAllowList, defaultValue: []string{}},
	{key: KeyGithubRepoAllowList, defaultValue: []string{}},
	{key: KeyGithubCloneDepth, defaultValue: 0},
	{key: KeyGitlabCloneDepth, defaultValue: 0},
}

func init() {
	Init()
}

func Init() {
	viper.SetEnvPrefix("TFBUDDY")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()

	for _, item := range bindings {
		if item.defaultValue != nil {
			viper.SetDefault(item.key, item.defaultValue)
		}
	}

	Reload()
}

func Reload() {
	cfg := Config{}
	err := viper.Unmarshal(&cfg, func(dc *mapstructure.DecoderConfig) {
		dc.DecodeHook = mapstructure.ComposeDecodeHookFunc(
			mapstructure.StringToSliceHookFunc(","),
		)
	})
	if err != nil {
		panic(err)
	}

	cfg.LogLevel = strings.TrimSpace(cfg.LogLevel)
	cfg.OTELCollectorHost = strings.TrimSpace(cfg.OTELCollectorHost)
	cfg.OTELCollectorPort = strings.TrimSpace(cfg.OTELCollectorPort)
	cfg.GitlabHookSecretKey = strings.TrimSpace(cfg.GitlabHookSecretKey)
	cfg.GithubHookSecretKey = strings.TrimSpace(cfg.GithubHookSecretKey)
	cfg.DefaultTFCOrganization = strings.TrimSpace(cfg.DefaultTFCOrganization)
	cfg.NATSServiceURL = strings.TrimSpace(cfg.NATSServiceURL)

	cfg.WorkspaceAllowList = trimAndFilter(cfg.WorkspaceAllowList)
	cfg.WorkspaceDenyList = trimAndFilter(cfg.WorkspaceDenyList)
	cfg.GitlabProjectAllowList = trimAndFilter(cfg.GitlabProjectAllowList)
	cfg.LegacyProjectAllowList = trimAndFilter(cfg.LegacyProjectAllowList)
	cfg.GithubRepoAllowList = trimAndFilter(cfg.GithubRepoAllowList)

	C = cfg
}

func trimAndFilter(items []string) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
