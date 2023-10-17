package tfc_trigger

import (
	"os"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/kr/pretty"
)

func TestProjectConfig_triggeredWorkspaces(t *testing.T) {

	type args struct {
		modifiedFiles []string
	}
	tests := []struct {
		name    string
		cfgYaml string
		args    args
		want    []*TFCWorkspace
	}{
		{
			name:    "no-match",
			cfgYaml: tfbuddyYamlNoTriggerDirs,
			args: args{
				modifiedFiles: []string{
					".gitlab-ci.yml",
				},
			},
			want: []*TFCWorkspace{},
		},
		{
			name:    "dir-match",
			cfgYaml: tfbuddyYamlNoTriggerDirs,
			args: args{
				modifiedFiles: []string{
					"terraform/dev/main.tf",
				},
			},
			want: testLoadConfig(t, tfbuddyYamlNoTriggerDirs).Workspaces,
		},
		{
			name:    "multiple-matching-files-single-workspace",
			cfgYaml: tfbuddyYamlNoTriggerDirs,
			args: args{
				modifiedFiles: []string{
					"terraform/dev/main.tf",
					"terraform/dev/variables.tf",
					"terraform/dev/outputs.tf",
					"terraform/dev/README.md",
				},
			},
			want: testLoadConfig(t, tfbuddyYamlNoTriggerDirs).Workspaces,
		},
		{
			name:    "trigger-dir-match",
			cfgYaml: tfbuddyYamlDoublestarTriggerDir,
			args: args{
				modifiedFiles: []string{
					"modules/service/main.tf",
				},
			},
			want: testLoadConfig(t, tfbuddyYamlDoublestarTriggerDir).Workspaces,
		},
		{
			name:    "nested-workspaces-parent",
			cfgYaml: tfbuddyYamlNestedWorkspace,
			args: args{
				modifiedFiles: []string{
					"terraform/dev/main.tf",
				},
			},
			want: []*TFCWorkspace{testLoadConfig(t, tfbuddyYamlNestedWorkspace).Workspaces[0]},
		},
		{
			name:    "nested-workspaces-child",
			cfgYaml: tfbuddyYamlNestedWorkspace,
			args: args{
				modifiedFiles: []string{
					"terraform/dev/res-x/main.tf",
				},
			},
			want: []*TFCWorkspace{testLoadConfig(t, tfbuddyYamlNestedWorkspace).Workspaces[1]},
		},
		{
			name:    "no-trailing-slash",
			cfgYaml: tfbuddyYamlDirNoTrailingSlash,
			args: args{
				modifiedFiles: []string{
					"terraform/dev/main.tf",
				},
			},
			want: []*TFCWorkspace{testLoadConfig(t, tfbuddyYamlDirNoTrailingSlash).Workspaces[0]},
		},
		{
			name:    "no-trailing-slash-trigger-dir",
			cfgYaml: tfbuddyYamlDirNoTrailingSlash,
			args: args{
				modifiedFiles: []string{
					"modules/database/main.tf",
				},
			},
			want: []*TFCWorkspace{testLoadConfig(t, tfbuddyYamlDirNoTrailingSlash).Workspaces[0]},
		},
		{
			name:    "root-and-subdir--root-change",
			cfgYaml: tfbuddyYamlRootSubdirWorkspaces,
			args: args{
				modifiedFiles: []string{
					"main.tf",
				},
			},
			want: []*TFCWorkspace{testLoadConfig(t, tfbuddyYamlRootSubdirWorkspaces).Workspaces[0]},
		},
		{
			name:    "root-and-subdir--subdir-change",
			cfgYaml: tfbuddyYamlRootSubdirWorkspaces,
			args: args{
				modifiedFiles: []string{
					"subdir/main.tf",
				},
			},
			want: []*TFCWorkspace{testLoadConfig(t, tfbuddyYamlRootSubdirWorkspaces).Workspaces[1]},
		},
		{
			name:    "shared-trigger-dir-multi-ws",
			cfgYaml: tfbuddyYamlSharedTriggerDirMultipleWorkspaces,
			args: args{
				modifiedFiles: []string{
					"modules/database/main.tf",
				},
			},
			want: []*TFCWorkspace{
				testLoadConfig(t, tfbuddyYamlSharedTriggerDirMultipleWorkspaces).Workspaces[0],
				testLoadConfig(t, tfbuddyYamlSharedTriggerDirMultipleWorkspaces).Workspaces[1],
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testLoadConfig(t, tt.cfgYaml)

			got := cfg.triggeredWorkspaces(tt.args.modifiedFiles)

			less := func(a, b *TFCWorkspace) bool { return a.Name < b.Name }
			equalIgnoreOrder := cmp.Diff(got, tt.want, cmpopts.SortSlices(less))
			if equalIgnoreOrder != "" {
				t.Errorf("triggeredWorkspaces() - %s", equalIgnoreOrder)
			}
		})
	}
}

func Test_loadProjectConfig(t *testing.T) {
	type args struct {
		b []byte
	}
	tests := []struct {
		name       string
		args       args
		want       *ProjectConfig
		wantErr    bool
		preTestFn  func()
		postTestFn func()
	}{
		{
			name: "basic",
			args: args{b: []byte(tfbuddyYamlNoTriggerDirs)},
			want: &ProjectConfig{Workspaces: []*TFCWorkspace{
				{
					Name:         "service-tfbuddy-dev",
					Organization: "foo-corp",
					Dir:          "terraform/dev/",
					Mode:         "apply-before-merge",
					TriggerDirs:  nil,
					AutoMerge:    true,
				},
			}},
			wantErr: false,
		},
		{
			name: "no auto merge",
			args: args{b: []byte(tfbuddyYamlNoTriggerDirsNoAutoMerge)},
			want: &ProjectConfig{Workspaces: []*TFCWorkspace{
				{
					Name:         "service-tfbuddy-dev",
					Organization: "foo-corp",
					Dir:          "terraform/dev/",
					Mode:         "apply-before-merge",
					TriggerDirs:  nil,
					AutoMerge:    false,
				},
			}},
			wantErr: false,
		},
		{
			name:    "no-organization",
			args:    args{b: []byte(tfbuddyYamlNoOrg)},
			want:    nil,
			wantErr: true,
		},
		{
			name: "no-organization-w-default",
			args: args{b: []byte(tfbuddyYamlNoOrg)},
			preTestFn: func() {
				os.Setenv(DefaultTfcOrganizationEnvName, "foo-corp")
			},
			postTestFn: func() {
				os.Unsetenv(DefaultTfcOrganizationEnvName)
			},
			want: &ProjectConfig{Workspaces: []*TFCWorkspace{
				{
					Name:         "service-tfbuddy-dev",
					Organization: "foo-corp",
					Dir:          "terraform/dev/",
					Mode:         "apply-before-merge",
					TriggerDirs:  nil,
					AutoMerge:    true,
				},
			}},
			wantErr: false,
		},
		{
			name: "doublestar",
			args: args{b: []byte(tfbuddyYamlDoublestarTriggerDir)},
			want: &ProjectConfig{Workspaces: []*TFCWorkspace{
				{
					Name:         "service-tfbuddy-dev",
					Organization: "foo-corp",
					Dir:          "terraform/dev/",
					Mode:         "apply-before-merge",
					AutoMerge:    true,
					TriggerDirs: []string{
						"modules/**",
					},
				},
			}},
			wantErr: false,
		},
		{
			name: "single trigger dir",
			args: args{b: []byte(tfbuddyYamlSingleTriggerDir)},
			want: &ProjectConfig{Workspaces: []*TFCWorkspace{
				{
					Name:         "service-tfbuddy-dev",
					Organization: "foo-corp",
					Dir:          "terraform/dev/",
					Mode:         "apply-before-merge",
					AutoMerge:    true,
					TriggerDirs: []string{
						"modules/database/",
					},
				},
			}},
			wantErr: false,
		},
		{
			name: "no-mode",
			args: args{b: []byte(tfbuddyYamlNoMode)},
			want: &ProjectConfig{Workspaces: []*TFCWorkspace{
				{
					Name:         "service-tfbuddy-dev",
					Organization: "foo-corp",
					Dir:          "terraform/dev/",
					Mode:         "apply-before-merge",
					AutoMerge:    true,
				},
			}},
			wantErr: false,
		},
		{
			name:    "invalid-mode",
			args:    args{b: []byte(tfbuddyYamlInvalidMode)},
			want:    nil,
			wantErr: true,
		},
		{
			name: "multiple-workspaces",
			args: args{b: []byte(tfbuddyYamlMultipleWorkspaces)},
			want: &ProjectConfig{Workspaces: []*TFCWorkspace{
				{
					Name:         "service-tfbuddy-dev",
					Organization: "foo-corp",
					Dir:          "terraform/dev/",
					Mode:         "apply-before-merge",
					AutoMerge:    true,
				},
				{
					Name:         "service-tfbuddy-tooling",
					Organization: "foo-corp",
					Dir:          "terraform/tooling/",
					Mode:         "apply-before-merge",
					AutoMerge:    true,
				},
			}},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.preTestFn != nil {
				tt.preTestFn()
			}
			got, err := loadProjectConfig(tt.args.b)
			if (err != nil) != tt.wantErr {
				t.Errorf("loadProjectConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("loadProjectConfig() got = %v, want %v", pretty.Sprint(got), pretty.Sprint(tt.want))
			}
			if tt.postTestFn != nil {
				tt.postTestFn()
			}
		})
	}
}

func testLoadConfig(t *testing.T, yaml string) *ProjectConfig {
	pc, err := loadProjectConfig([]byte(yaml))
	if err != nil {
		t.Errorf("could not load ProjectConfig for test: %v", err)
	}
	return pc
}

const tfbuddyYamlNoTriggerDirs = `
---
workspaces:
  - name: service-tfbuddy-dev
    organization: foo-corp
    dir: terraform/dev/
    mode: apply-before-merge
`
const tfbuddyYamlNoTriggerDirsNoAutoMerge = `
---
workspaces:
  - name: service-tfbuddy-dev
    organization: foo-corp
    dir: terraform/dev/
    mode: apply-before-merge
    autoMerge: false
`

const tfbuddyYamlNoOrg = `
---
workspaces:
  - name: service-tfbuddy-dev
    dir: terraform/dev/
    mode: apply-before-merge
`

const tfbuddyYamlDoublestarTriggerDir = `
---
workspaces:
  - name: service-tfbuddy-dev
    organization: foo-corp
    dir: terraform/dev/
    mode: apply-before-merge
    triggerDirs:
    - modules/**
`

const tfbuddyYamlSingleTriggerDir = `
---
workspaces:
  - name: service-tfbuddy-dev
    organization: foo-corp
    dir: terraform/dev/
    mode: apply-before-merge
    triggerDirs:
    - modules/database/
`

const tfbuddyYamlNoMode = `
---
workspaces:
  - name: service-tfbuddy-dev
    organization: foo-corp
    dir: terraform/dev/
`

const tfbuddyYamlInvalidMode = `
---
workspaces:
  - name: service-tfbuddy-dev
    dir: terraform/dev/
    mode: sausage
`

const tfbuddyYamlMultipleWorkspaces = `
---
workspaces:
  - name: service-tfbuddy-dev
    organization: foo-corp
    dir: terraform/dev/
    mode: apply-before-merge
  - name: service-tfbuddy-tooling
    organization: foo-corp
    dir: terraform/tooling/
    mode: apply-before-merge
`

const tfbuddyYamlNestedWorkspace = `
---
workspaces:
  - name: environment-dev
    organization: foo-corp
    dir: terraform/dev/

  - name: dev-res-x
    organization: foo-corp
    dir: terraform/dev/res-x/

`

const tfbuddyYamlDirNoTrailingSlash = `
---
workspaces:
  - name: environment-dev
    organization: foo-corp
    dir: terraform/dev
    triggerDirs:
    - modules/database

`

const tfbuddyYamlRootSubdirWorkspaces = `
---
workspaces:
  - name: root-ws
    organization: foo-corp
    dir: /
  - name: subdir-ws
    organization: foo-corp
    dir: subdir/

`

const tfbuddyYamlSharedTriggerDirMultipleWorkspaces = `
---
workspaces:
  - name: environment-dev
    organization: foo-corp
    dir: terraform/dev
    triggerDirs:
    - modules/database
  - name: environment-prod
    organization: foo-corp
    dir: terraform/prod
    triggerDirs:
    - modules/database

`
