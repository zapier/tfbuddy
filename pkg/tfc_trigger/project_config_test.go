package tfc_trigger

import (
	"os"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/kr/pretty"
)

func TestProjectConfig_workspaceForDir(t *testing.T) {
	type fields struct {
		Workspaces []*TFCWorkspace
	}
	type args struct {
		dir string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   *TFCWorkspace
	}{
		{
			name: "workspace-for-dir-matching",
			fields: fields{
				Workspaces: []*TFCWorkspace{
					{
						Name:         "service-tfbuddy-dev",
						Organization: "foo-corp",
						Dir:          "terraform/dev/",
						Mode:         "apply-before-merge",
					},
				},
			},
			args: args{
				dir: "terraform/dev/",
			},
			want: &TFCWorkspace{
				Name:         "service-tfbuddy-dev",
				Organization: "foo-corp",
				Dir:          "terraform/dev/",
				Mode:         "apply-before-merge",
			},
		},
		{
			name: "workspace-for-non-matching-dir",
			fields: fields{
				Workspaces: []*TFCWorkspace{
					{
						Name:         "service-tfbuddy-dev",
						Organization: "foo-corp",
						Dir:          "terraform/dev/",
						Mode:         "apply-before-merge",
					},
				},
			},
			args: args{
				dir: "extra/workspaces/",
			},
			want: nil,
		},
		{
			name: "different-dir-same-subdir-name",
			fields: fields{
				Workspaces: []*TFCWorkspace{
					{
						Name:         "a-compute",
						Organization: "foo-corp",
						Dir:          "a/compute/",
						Mode:         "apply-before-merge",
					},
					{
						Name:         "b-compute",
						Organization: "foo-corp",
						Dir:          "b/compute/",
						Mode:         "apply-before-merge",
					},
				},
			},
			args: args{
				dir: "b/compute/",
			},
			want: &TFCWorkspace{
				Name:         "b-compute",
				Organization: "foo-corp",
				Dir:          "b/compute/",
				Mode:         "apply-before-merge",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &ProjectConfig{
				Workspaces: tt.fields.Workspaces,
			}
			if got := cfg.workspaceForDir(tt.args.dir); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ProjectConfig.workspaceForDir() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProjectConfig_workspacesForTriggerDir(t *testing.T) {
	type fields struct {
		Workspaces []*TFCWorkspace
	}
	type args struct {
		dir string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   []*TFCWorkspace
	}{
		{
			name: "trigger-dir-match",
			fields: fields{
				Workspaces: []*TFCWorkspace{
					{
						Name:         "service-a",
						Organization: "foo-corp",
						Dir:          "a/",
						TriggerDirs:  []string{"modules/"},
					},
				},
			},
			args: args{
				dir: "modules/",
			},
			want: []*TFCWorkspace{
				{
					Name:         "service-a",
					Organization: "foo-corp",
					Dir:          "a/",
					TriggerDirs:  []string{"modules/"},
				},
			},
		},
		{
			name: "trigger-dir-multiple-workspace-match",
			fields: fields{
				Workspaces: []*TFCWorkspace{
					{
						Name:         "service-a",
						Organization: "foo-corp",
						Dir:          "a/",
						TriggerDirs:  []string{"modules/"},
					},
					{
						Name:         "service-b",
						Organization: "foo-corp",
						Dir:          "b/",
						TriggerDirs:  []string{"modules/"},
					},
				},
			},
			args: args{
				dir: "modules/",
			},
			want: []*TFCWorkspace{
				{
					Name:         "service-a",
					Organization: "foo-corp",
					Dir:          "a/",
					TriggerDirs:  []string{"modules/"},
				},
				{
					Name:         "service-b",
					Organization: "foo-corp",
					Dir:          "b/",
					TriggerDirs:  []string{"modules/"},
				},
			},
		},
		{
			name: "multiple-trigger-dir-workspace-match",
			fields: fields{
				Workspaces: []*TFCWorkspace{
					{
						Name:         "service-a",
						Organization: "foo-corp",
						Dir:          "a/",
						TriggerDirs: []string{
							"modules/a",
							"modules/c",
						},
					},
				},
			},
			args: args{
				dir: "modules/c",
			},
			want: []*TFCWorkspace{
				{
					Name:         "service-a",
					Organization: "foo-corp",
					Dir:          "a/",
					TriggerDirs: []string{"" +
						"modules/a",
						"modules/c",
					},
				},
			},
		},
		{
			name: "trigger-no-match",
			fields: fields{
				Workspaces: []*TFCWorkspace{
					{
						Name:         "service-a",
						Organization: "foo-corp",
						Dir:          "a/",
						TriggerDirs:  []string{"modules/"},
					},
				},
			},
			args: args{
				dir: "docs/",
			},
			want: make([]*TFCWorkspace, 0),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &ProjectConfig{
				Workspaces: tt.fields.Workspaces,
			}
			if got := cfg.workspacesForTriggerDir(tt.args.dir); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("workspacesForTriggerDir() = %v, want %v", got, tt.want)
			}
		})
	}
}

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
		{
			name:    "dir-and-subdir-same-name--dir-change",
			cfgYaml: tfbuddyYamlDirAndSubdirSameName,
			args: args{
				modifiedFiles: []string{
					"workspaces/main.tf",
				},
			},
			want: []*TFCWorkspace{
				testLoadConfig(t, tfbuddyYamlDirAndSubdirSameName).Workspaces[0], // "workspaces" workspace
			},
		},
		{
			name:    "dir-and-subdir-same-name--subdir-change",
			cfgYaml: tfbuddyYamlDirAndSubdirSameName,
			args: args{
				modifiedFiles: []string{
					"aws/workspaces/main.tf",
				},
			},
			want: []*TFCWorkspace{
				testLoadConfig(t, tfbuddyYamlDirAndSubdirSameName).Workspaces[1], // "aws/workspaces" workspace
			},
		},
		{
			name:    "subdir-and-dir-same-name--dir-change",
			cfgYaml: tfbuddyYamlSubdirAndDirSameName,
			args: args{
				modifiedFiles: []string{
					"test2/test3/main.tf",
				},
			},
			want: []*TFCWorkspace{
				testLoadConfig(t, tfbuddyYamlSubdirAndDirSameName).Workspaces[1], // "test2/test3" workspace
			},
		},
		{
			name:    "subdir-and-dir-same-name--subdir-change",
			cfgYaml: tfbuddyYamlSubdirAndDirSameName,
			args: args{
				modifiedFiles: []string{
					"test1/test2/test3/main.tf",
				},
			},
			want: []*TFCWorkspace{
				testLoadConfig(t, tfbuddyYamlSubdirAndDirSameName).Workspaces[0], // "test1/test2/test3" workspace
			},
		},
		{
			name:    "different-dir-same-subdir-name",
			cfgYaml: tfbuddyYamlDifferentDirSameSubdir,
			args: args{
				modifiedFiles: []string{
					"gcp/workspaces/main.tf",
				},
			},
			want: []*TFCWorkspace{
				testLoadConfig(t, tfbuddyYamlDifferentDirSameSubdir).Workspaces[1],
			},
		},
		{
			name:    "multiple-dir-and-subdir",
			cfgYaml: tfbuddyYamlMultipleDirAndSubdir,
			args: args{
				modifiedFiles: []string{
					"workspaces/main.tf",
				},
			},
			want: []*TFCWorkspace{
				testLoadConfig(t, tfbuddyYamlMultipleDirAndSubdir).Workspaces[2],
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

const tfbuddyYamlDirAndSubdirSameName = `
---
workspaces:
  - name: workspaces
    organization: foo-corp
    dir: workspaces
  - name: aws-workspaces
    organization: foo-corp
    dir: aws/workspaces

`

const tfbuddyYamlSubdirAndDirSameName = `
---
workspaces:
  - name: subdir
    organization: foo-corp
    dir: test1/test2/test3
  - name: dir
    organization: foo-corp
    dir: test2/test3

`

const tfbuddyYamlDifferentDirSameSubdir = `
---
workspaces:
  - name: aws-workspaces
    organization: foo-corp
    dir: aws/workspaces
  - name: gcp-workspaces
    organization: foo-corp
    dir: gcp/workspaces

`

const tfbuddyYamlMultipleDirAndSubdir = `
---
workspaces:
  - name: aws-workspaces
    organization: foo-corp
    dir: aws/workspaces
  - name: gcp-workspaces
    organization: foo-corp
    dir: gcp/workspaces
  - name: workspaces
    organization: foo-corp
    dir: workspaces

`
