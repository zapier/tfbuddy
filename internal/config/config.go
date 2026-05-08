package config

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/pflag"
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
	description  string
	shorthand    string
}

var bindings = []binding{
	{key: KeyLogLevel, defaultValue: "info", description: "Set the log output level (info, debug, trace)", shorthand: "v"},
	{key: KeyDevMode, defaultValue: false, description: "Enable developer-friendly console logging output."},
	{key: KeyOTELEnabled, defaultValue: false, description: "Enable OpenTelemetry export for TFBuddy."},
	{key: KeyOTELCollectorHost, defaultValue: "", description: "OpenTelemetry collector host."},
	{key: KeyOTELCollectorPort, defaultValue: "", description: "OpenTelemetry collector port."},
	{key: KeyGitlabHookSecretKey, defaultValue: "", description: "Secret key used to validate incoming GitLab webhooks."},
	{key: KeyGithubHookSecretKey, defaultValue: "", description: "Secret key used to validate incoming GitHub webhooks."},
	{key: KeyDefaultTFCOrganization, defaultValue: "", description: "Default Terraform Cloud organization for workspaces that omit one in .tfbuddy.yaml."},
	{key: KeyWorkspaceAllowList, defaultValue: []string{}, description: "Comma-separated workspace allow list. Entries without an organization use the default Terraform Cloud organization."},
	{key: KeyWorkspaceDenyList, defaultValue: []string{}, description: "Comma-separated workspace deny list. Entries without an organization use the default Terraform Cloud organization."},
	{key: KeyAllowAutoMerge, defaultValue: true, description: "Globally enable or disable TFBuddy-managed auto-merge."},
	{key: KeyFailCIOnSentinelSoftFail, defaultValue: false, description: "Mark CI as failed when Terraform policy checks soft-fail."},
	{key: KeyDeleteOldComments, defaultValue: false, description: "Delete older bot comments for the same workspace and action after posting a newer one."},
	{key: KeyNATSServiceURL, defaultValue: "", description: "NATS connection URL. When empty, TFBuddy falls back to the NATS client default."},
	{key: KeyGitlabProjectAllowList, defaultValue: []string{}, description: "Comma-separated GitLab project allow list prefixes."},
	{key: KeyLegacyProjectAllowList, defaultValue: []string{}, description: "Deprecated comma-separated GitLab project allow list prefixes."},
	{key: KeyGithubRepoAllowList, defaultValue: []string{}, description: "Comma-separated GitHub repository allow list prefixes."},
	{key: KeyGithubCloneDepth, defaultValue: 0, description: "Git clone depth to use for GitHub merge request checkouts. Zero means full history."},
	{key: KeyGitlabCloneDepth, defaultValue: 0, description: "Git clone depth to use for GitLab merge request checkouts. Zero means full history."},
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

func Load() (Config, error) {
	cfg := Config{}
	err := viper.Unmarshal(&cfg, func(dc *mapstructure.DecoderConfig) {
		dc.DecodeHook = mapstructure.ComposeDecodeHookFunc(
			trimStringHook(),
			trimStringSliceHook(),
			mapstructure.StringToTimeDurationHookFunc(),
			mapstructure.StringToSliceHookFunc(","),
		)
	})
	return cfg, err
}

func Reload() {
	cfg, err := Load()
	if err != nil {
		panic(err)
	}

	C = cfg
}

func RegisterFlags(fs *pflag.FlagSet) error {
	for _, item := range bindings {
		switch def := item.defaultValue.(type) {
		case string:
			if item.shorthand != "" {
				fs.StringP(item.key, item.shorthand, def, item.description)
			} else {
				fs.String(item.key, def, item.description)
			}
		case bool:
			fs.Bool(item.key, def, item.description)
		case int:
			fs.Int(item.key, def, item.description)
		case []string:
			fs.StringSlice(item.key, def, item.description)
		default:
			continue
		}
		if err := viper.BindPFlag(item.key, fs.Lookup(item.key)); err != nil {
			return err
		}
	}

	return nil
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

func trimStringHook() mapstructure.DecodeHookFuncType {
	return func(from reflect.Type, to reflect.Type, data any) (any, error) {
		if from.Kind() != reflect.String || to.Kind() != reflect.String {
			return data, nil
		}
		return strings.TrimSpace(data.(string)), nil
	}
}

func trimStringSliceHook() mapstructure.DecodeHookFuncType {
	return func(from reflect.Type, to reflect.Type, data any) (any, error) {
		if to != reflect.TypeOf([]string{}) {
			return data, nil
		}

		switch value := data.(type) {
		case string:
			return trimAndFilter(strings.Split(value, ",")), nil
		case []string:
			return trimAndFilter(value), nil
		case []any:
			items := make([]string, 0, len(value))
			for _, item := range value {
				if str, ok := item.(string); ok {
					items = append(items, str)
				}
			}
			return trimAndFilter(items), nil
		default:
			return data, nil
		}
	}
}

type DocumentationOption struct {
	EnvVars      []string
	Flag         string
	Description  string
	DefaultValue string
}

func DocumentationOptions() []DocumentationOption {
	options := make([]DocumentationOption, 0, len(bindings))
	for _, item := range bindings {
		options = append(options, DocumentationOption{
			EnvVars:      []string{envVarName(item.key)},
			Flag:         flagName(item),
			Description:  item.description,
			DefaultValue: defaultValueString(item.defaultValue),
		})
	}
	return options
}

func envVarName(key string) string {
	return "TFBUDDY_" + strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
}

func flagName(item binding) string {
	flag := "--" + item.key
	if item.shorthand == "" {
		return flag
	}
	return flag + ", -" + item.shorthand
}

func defaultValueString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(v)
	case []string:
		if len(v) == 0 {
			return ""
		}
		return strings.Join(v, ",")
	default:
		return ""
	}
}
