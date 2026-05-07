package config

import (
	"strings"

	"github.com/spf13/viper"
)

func init() {
	Init()
}

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

type binding struct {
	key          string
	env          string
	defaultValue any
}

var bindings = []binding{
	{key: KeyLogLevel, env: "TFBUDDY_LOG_LEVEL", defaultValue: "info"},
	{key: KeyDevMode, env: "TFBUDDY_DEV_MODE", defaultValue: false},
	{key: KeyOTELEnabled, env: "TFBUDDY_OTEL_ENABLED", defaultValue: false},
	{key: KeyOTELCollectorHost, env: "TFBUDDY_OTEL_COLLECTOR_HOST"},
	{key: KeyOTELCollectorPort, env: "TFBUDDY_OTEL_COLLECTOR_PORT"},
	{key: KeyGitlabHookSecretKey, env: "TFBUDDY_GITLAB_HOOK_SECRET_KEY"},
	{key: KeyGithubHookSecretKey, env: "TFBUDDY_GITHUB_HOOK_SECRET_KEY"},
	{key: KeyDefaultTFCOrganization, env: "TFBUDDY_DEFAULT_TFC_ORGANIZATION"},
	{key: KeyWorkspaceAllowList, env: "TFBUDDY_WORKSPACE_ALLOW_LIST"},
	{key: KeyWorkspaceDenyList, env: "TFBUDDY_WORKSPACE_DENY_LIST"},
	{key: KeyAllowAutoMerge, env: "TFBUDDY_ALLOW_AUTO_MERGE", defaultValue: true},
	{key: KeyFailCIOnSentinelSoftFail, env: "TFBUDDY_FAIL_CI_ON_SENTINEL_SOFT_FAIL", defaultValue: false},
	{key: KeyDeleteOldComments, env: "TFBUDDY_DELETE_OLD_COMMENTS", defaultValue: false},
	{key: KeyNATSServiceURL, env: "TFBUDDY_NATS_SERVICE_URL"},
	{key: KeyGitlabProjectAllowList, env: "TFBUDDY_GITLAB_PROJECT_ALLOW_LIST"},
	{key: KeyLegacyProjectAllowList, env: "TFBUDDY_PROJECT_ALLOW_LIST"},
	{key: KeyGithubRepoAllowList, env: "TFBUDDY_GITHUB_REPO_ALLOW_LIST"},
	{key: KeyGithubCloneDepth, env: "TFBUDDY_GITHUB_CLONE_DEPTH"},
	{key: KeyGitlabCloneDepth, env: "TFBUDDY_GITLAB_CLONE_DEPTH"},
}

func Init() {
	for _, item := range bindings {
		_ = viper.BindEnv(item.key, item.env)
		if item.defaultValue != nil {
			viper.SetDefault(item.key, item.defaultValue)
		}
	}
}

func String(key string) string {
	return strings.TrimSpace(viper.GetString(key))
}

func Bool(key string) bool {
	return viper.GetBool(key)
}

func Int(key string) int {
	return viper.GetInt(key)
}

func StringList(key string) []string {
	raw := viper.Get(key)
	switch value := raw.(type) {
	case []string:
		return trimAndFilter(value)
	case []any:
		items := make([]string, 0, len(value))
		for _, item := range value {
			if str, ok := item.(string); ok {
				items = append(items, str)
			}
		}
		return trimAndFilter(items)
	case string:
		return splitCSV(value)
	default:
		return splitCSV(viper.GetString(key))
	}
}

func LogLevel() string {
	return String(KeyLogLevel)
}

func DevModeEnabled() bool {
	return Bool(KeyDevMode)
}

func OTELEnabled() bool {
	return Bool(KeyOTELEnabled)
}

func OTELCollectorHost() string {
	return String(KeyOTELCollectorHost)
}

func OTELCollectorPort() string {
	return String(KeyOTELCollectorPort)
}

func GitlabHookSecretKey() string {
	return String(KeyGitlabHookSecretKey)
}

func GithubHookSecretKey() string {
	return String(KeyGithubHookSecretKey)
}

func DefaultTFCOrganization() string {
	return String(KeyDefaultTFCOrganization)
}

func WorkspaceAllowList() []string {
	return StringList(KeyWorkspaceAllowList)
}

func WorkspaceDenyList() []string {
	return StringList(KeyWorkspaceDenyList)
}

func AutoMergeEnabled() bool {
	return Bool(KeyAllowAutoMerge)
}

func FailCIOnSentinelSoftFail() bool {
	return Bool(KeyFailCIOnSentinelSoftFail)
}

func DeleteOldCommentsEnabled() bool {
	return Bool(KeyDeleteOldComments)
}

func NATSServiceURL() string {
	return String(KeyNATSServiceURL)
}

func GitlabProjectAllowList() []string {
	return StringList(KeyGitlabProjectAllowList)
}

func LegacyProjectAllowList() []string {
	return StringList(KeyLegacyProjectAllowList)
}

func GithubRepoAllowList() []string {
	return StringList(KeyGithubRepoAllowList)
}

func GithubCloneDepth() int {
	return Int(KeyGithubCloneDepth)
}

func GitlabCloneDepth() int {
	return Int(KeyGitlabCloneDepth)
}

func StringListForUnknownEnv(envName string) []string {
	_ = viper.BindEnv(envName, envName)
	return StringList(envName)
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return trimAndFilter(strings.Split(value, ","))
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
