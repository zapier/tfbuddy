package tfc_trigger

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/creasty/defaults"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/utils"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.opentelemetry.io/otel"
	"gopkg.in/dealancer/validate.v2"
	"gopkg.in/yaml.v2"
)

const ProjectConfigFilename = `.tfbuddy.yaml`

type ProjectConfig struct {
	Workspaces []*TFCWorkspace `yaml:"workspaces"`
}

func (cfg *ProjectConfig) workspaceForDir(dir string) *TFCWorkspace {
	for _, ws := range cfg.Workspaces {
		wsDir := ws.Dir
		if !strings.HasSuffix(wsDir, "/") {
			wsDir += "/"
		}

		if strings.HasSuffix(dir, wsDir) {
			return ws
		} else if wsDir != "/" && strings.HasSuffix(dir+"/", wsDir) {
			return ws
		} else if dir == "." && wsDir == "/" {
			return ws
		}

	}
	return nil
}

func (cfg *ProjectConfig) workspacesForTriggerDir(dir string) []*TFCWorkspace {
	result := []*TFCWorkspace{}
	for _, ws := range cfg.Workspaces {
		wsDir := ws.Dir
		if !strings.HasSuffix(wsDir, "/") {
			wsDir += "/"
		}

		for _, td := range ws.TriggerDirs {
			if match, err := doublestar.Match(td, dir); match {
				result = append(result, ws)
			} else if err != nil {
				log.Debug().Err(err).Str("dir", dir).Msg("error matching workspace for directory")
			}
		}
	}
	return result
}

func (cfg *ProjectConfig) triggeredWorkspaces(modifiedFiles []string) []*TFCWorkspace {
	triggeredMap := map[string]*TFCWorkspace{}
	for _, mf := range modifiedFiles {
		dir := path.Dir(mf)
		ws := cfg.workspaceForDir(dir)
		if ws != nil {
			triggeredMap[ws.Dir] = ws
		}

		for _, trig := range cfg.workspacesForTriggerDir(dir) {
			if trig != nil {
				triggeredMap[trig.Dir] = trig
			}
		}
	}

	triggered := make([]*TFCWorkspace, 0, len(triggeredMap))
	for _, v := range triggeredMap {
		triggered = append(triggered, v)
	}
	return triggered
}

type TFCWorkspace struct {
	Name         string   `yaml:"name" validate:"empty=false"`
	Organization string   `yaml:"organization" validate:"empty=false"`
	Dir          string   `yaml:"dir"`
	Mode         string   `yaml:"mode" default:"apply-before-merge" validate:"one_of=apply-before-merge,merge-before-apply,tfc-vcs-repo"`
	TriggerDirs  []string `yaml:"triggerDirs"`
}

func getProjectConfigFile(ctx context.Context, gl vcs.GitClient, trigger *TFCTrigger) (*ProjectConfig, error) {
	ctx, span := otel.Tracer("GitlabHandler").Start(ctx, "getProjectConfigFile")
	defer span.End()

	branches := []string{trigger.GetBranch(), "master", "main"}
	for _, branch := range branches {
		log.Debug().Msg(fmt.Sprintf("considering branch %s", branch))
		b, err := gl.GetRepoFile(ctx, trigger.GetProjectNameWithNamespace(), ProjectConfigFilename, branch)
		if err != nil {
			log.Info().Err(err).Msg(fmt.Sprintf("no file on branch %s", branch))
			continue
		}
		return loadProjectConfig(b)
	}
	log.Warn().Msg("could not retrieve .tfbuddy.yaml for repo")

	return nil, utils.CreatePermanentError(errors.New("could not retrieve .tfbuddy.yaml for repo"))
}

func loadProjectConfig(b []byte) (*ProjectConfig, error) {
	cfg := &ProjectConfig{}
	err := yaml.Unmarshal(b, cfg)
	if err != nil {
		return nil, fmt.Errorf("could not parse Project config file (.tfbuddy.yaml): %v. %w", err, utils.ErrPermanent)
	}

	defaultOrgName := getDefaultOrgName()
	for _, ws := range cfg.Workspaces {
		if ws.Organization == "" {
			ws.Organization = defaultOrgName
		}
	}

	if err := validate.Validate(cfg); err != nil {
		return nil, utils.CreatePermanentError(err)
	}

	return cfg, nil
}

func (s *TFCWorkspace) UnmarshalYAML(unmarshal func(interface{}) error) error {
	err := defaults.Set(s)
	if err != nil {
		return fmt.Errorf("failed to set defaults for project config: %v", err)
	}

	type plain TFCWorkspace
	if err := unmarshal((*plain)(s)); err != nil {
		return err
	}

	return nil
}

func getDefaultOrgName() string {
	return os.Getenv(DefaultTfcOrganizationEnvName)
}
