package tfc_trigger

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/creasty/defaults"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/internal/config"
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

// Finds the workspace with the deepest matching directory suffix.
// For example, given workspaces with config "terraform/dev/" and a dir of "dev/",
// and a filepath directory of "filesystem/terraform/dev/" - "dev" and "terraform/dev/" are both a suffix
// we would return the ws for "terraform/dev/"as it is the deepest match.
// Special case: if a workspace has config "/" and the input dir is ".", that workspace is returned.
// If no workspace matches, returns nil.
func (cfg *ProjectConfig) workspaceForDir(dir string) *TFCWorkspace {
	var longestMatch *TFCWorkspace
	var longestMatchDepth int

	for _, ws := range cfg.Workspaces {
		wsDir := ws.Dir
		if !strings.HasSuffix(wsDir, "/") {
			wsDir += "/"
		}

		if wsDir == "/" {
			if dir == "." {
				return ws
			}
			continue
		}

		if strings.HasSuffix(dir+"/", wsDir) || strings.HasSuffix(dir, wsDir) {
			wsDirDepth := len(strings.Split(wsDir, "/"))
			if wsDirDepth > longestMatchDepth {
				longestMatch = ws
				longestMatchDepth = wsDirDepth
			}
		}
	}
	return longestMatch
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

// hasChangesForWorkspace checks if any of the given modified files fall within
// a workspace's Dir (prefix match) or match its TriggerDirs (glob match).
// This is used for divergence detection: checking whether a workspace's relevant
// directories have changed on the target branch.
func hasChangesForWorkspace(ws *TFCWorkspace, modifiedFiles []string) bool {
	wsDir := ws.Dir
	if wsDir != "" && !strings.HasSuffix(wsDir, "/") {
		wsDir += "/"
	}

	// Root workspace (empty or "/" dir) matches files directly in the root directory
	isRootWS := wsDir == "" || wsDir == "/"

	for _, mf := range modifiedFiles {
		fileDir := path.Dir(mf)

		if isRootWS {
			// Root workspace only matches root-level files (dir == ".")
			if fileDir == "." {
				return true
			}
		} else {
			// Check if file is within workspace's Dir (prefix match)
			if strings.HasPrefix(mf, wsDir) {
				return true
			}
		}

		// Check if file matches any triggerDir (glob match).
		// Match against both the directory and the full file path so that
		// patterns like "staging/*.tf" (file-level) and "modules/**" (directory-level)
		// both work correctly.
		for _, td := range ws.TriggerDirs {
			dirMatch, err := doublestar.Match(td, fileDir)
			if err != nil {
				log.Warn().Err(err).Str("triggerDir", td).Str("fileDir", fileDir).Msg("invalid triggerDir glob pattern, treating as match to be safe")
				return true
			}
			if dirMatch {
				return true
			}
			fileMatch, err := doublestar.Match(td, mf)
			if err != nil {
				log.Warn().Err(err).Str("triggerDir", td).Str("file", mf).Msg("invalid triggerDir glob pattern, treating as match to be safe")
				return true
			}
			if fileMatch {
				return true
			}
		}
	}
	return false
}

type TFCWorkspace struct {
	Name         string   `yaml:"name" validate:"empty=false"`
	Organization string   `yaml:"organization" validate:"empty=false"`
	Dir          string   `yaml:"dir"`
	Mode         string   `yaml:"mode" default:"apply-before-merge" validate:"one_of=apply-before-merge,merge-before-apply,tfc-vcs-repo"`
	TriggerDirs  []string `yaml:"triggerDirs"`
	AutoMerge    bool     `yaml:"autoMerge" default:"true"`
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
	return config.DefaultTFCOrganization()
}
