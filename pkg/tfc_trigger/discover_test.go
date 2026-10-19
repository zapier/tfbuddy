package tfc_trigger

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/zapier/tfbuddy/internal/config"
)

// The layout used by terraform-services: one workspace per
// services/<service>/<env>/<cell>/, planned on changes to the cell, to the
// env directory, or to the env's non-Terraform payloads under files/.
const tfbuddyYamlDiscover = `---
discover:
  - dir: services/*/*/*/
    exclude: [services/*/*/files]
    name: services-{1}-{2}-{3}
    organization: foo-corp
    triggerDirs:
      - services/{1}/{2}
      - services/{1}/{2}/files/**
workspaces:
  - name: workspaces-root
    organization: foo-corp
    dir: workspaces/
`

func mustLoadDiscoverConfig(t *testing.T, yaml string) *ProjectConfig {
	t.Helper()
	cfg, err := loadProjectConfig(config.C, []byte(yaml))
	if err != nil {
		t.Fatalf("loadProjectConfig() error = %v", err)
	}
	return cfg
}

// fakeRepo lists directories from a fixed set of paths and records which
// roots were listed.
type fakeRepo struct {
	dirs   []string
	listed []string
	err    error
}

func (f *fakeRepo) list(_ context.Context, root string) ([]string, error) {
	f.listed = append(f.listed, root)
	if f.err != nil {
		return nil, f.err
	}
	var result []string
	for _, d := range f.dirs {
		if root == "" || strings.HasPrefix(d, root+"/") {
			result = append(result, d)
		}
	}
	return result, nil
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{dirs: []string{
		"services",
		"services/api",
		"services/api/staging",
		"services/api/staging/main-us-west-2",
		"services/api/staging/global-us-east-1",
		"services/api/staging/files",
		"services/api/staging/files/k6",
		"services/api/production",
		"services/api/production/main-us-east-1",
		"services/web",
		"services/web/staging",
		"services/web/staging/main-us-west-2",
		"workspaces",
		"workspaces/staging",
	}}
}

func workspaceNames(workspaces []*TFCWorkspace) []string {
	var names []string
	for _, ws := range workspaces {
		names = append(names, ws.Name)
	}
	sort.Strings(names)
	return names
}

func Test_loadProjectConfig_discover(t *testing.T) {
	cfg := mustLoadDiscoverConfig(t, tfbuddyYamlDiscover)
	want := []*DiscoverRule{{
		Dir:          "services/*/*/*/",
		Exclude:      []string{"services/*/*/files"},
		Name:         "services-{1}-{2}-{3}",
		Organization: "foo-corp",
		Mode:         "apply-before-merge",
		TriggerDirs:  []string{"services/{1}/{2}", "services/{1}/{2}/files/**"},
		AutoMerge:    true,
	}}
	if diff := cmp.Diff(want, cfg.Discover); diff != "" {
		t.Errorf("Discover mismatch (-want +got):\n%s", diff)
	}
	if len(cfg.Workspaces) != 1 {
		t.Errorf("expected the explicit workspace only before discovery, got %v", workspaceNames(cfg.Workspaces))
	}
}

func Test_loadProjectConfig_discoverInvalid(t *testing.T) {
	tests := []struct {
		name    string
		rule    string
		wantErr string
	}{
		{"no wildcard", "dir: services/api/\n    name: api", "at least one '*'"},
		{"partial glob segment", "dir: services/api-*/\n    name: api-{1}", "literal name or '*'"},
		{"double star", "dir: services/**/\n    name: api-{1}", "literal name or '*'"},
		{"parent segment", "dir: services/../*/\n    name: api-{1}", "without empty, '.' or '..'"},
		{"name misses a wildcard", "dir: services/*/*/\n    name: api-{1}", "missing {2}"},
		{"placeholder out of range", "dir: services/*/\n    name: api-{1}\n    triggerDirs: ['shared/{2}']", "refers to {2}"},
		{"invalid exclude", "dir: services/*/\n    name: api-{1}\n    exclude: ['services/[']", "invalid exclude"},
		{"invalid mode", "dir: services/*/\n    name: api-{1}\n    mode: sometimes", "one_of"},
		{"missing name", "dir: services/*/", "Name"},
		{"placeholder in character class", "dir: services/*/\n    name: api-{1}\n    triggerDirs: ['shared/[{1}]']", "inside [...]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			yaml := "discover:\n  - organization: foo-corp\n    " + tt.rule + "\n"
			_, err := loadProjectConfig(config.C, []byte(yaml))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("loadProjectConfig() error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestDiscoverRule_listRoots(t *testing.T) {
	rule := mustLoadDiscoverConfig(t, tfbuddyYamlDiscover).Discover[0]
	tests := []struct {
		name          string
		modifiedFiles []string
		want          []string
	}{
		{"file in a cell", []string{"services/api/staging/main-us-west-2/main.tf"}, []string{"services/api/staging"}},
		{"env-level file", []string{"services/api/staging/variables.tf"}, []string{"services/api/staging"}},
		{"payload under files", []string{"services/api/staging/files/k6/script.js"}, []string{"services/api/staging"}},
		{"several envs and services", []string{
			"services/api/staging/variables.tf",
			"services/api/staging/main-us-west-2/main.tf",
			"services/api/production/main-us-east-1/main.tf",
			"services/web/staging/main-us-west-2/main.tf",
		}, []string{"services/api/staging", "services/api/production", "services/web/staging"}},
		{"service-level file", []string{"services/api/README.md"}, nil},
		{"unrelated file", []string{"README.md", "scripts/check.py"}, nil},
		{"nested dir inside a cell", []string{"services/api/staging/main-us-west-2/modules/x/main.tf"}, nil},
		{"directory ending with a cell", []string{"archive/services/api/staging/main-us-west-2/main.tf"}, []string{"services/api/staging"}},
		{"directory name ending with a cell's first segment", []string{"oldservices/api/staging/main-us-west-2/main.tf"}, []string{"services/api/staging"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, rule.listRoots(tt.modifiedFiles)); diff != "" {
				t.Errorf("listRoots() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDiscoverRule_listRootsUnboundTriggerDir(t *testing.T) {
	// A triggerDir that pins down no wildcard can trigger any workspace the
	// rule derives, so the whole rule has to be listed.
	cfg := mustLoadDiscoverConfig(t, `---
discover:
  - dir: services/*/*/
    name: services-{1}-{2}
    organization: foo-corp
    triggerDirs: [modules/**]
`)
	got := cfg.Discover[0].listRoots([]string{"modules/vpc/main.tf"})
	if diff := cmp.Diff([]string{"services"}, got); diff != "" {
		t.Errorf("listRoots() mismatch (-want +got):\n%s", diff)
	}
}

func TestDiscoverRule_listRootsCollapsesBulkChanges(t *testing.T) {
	rule := mustLoadDiscoverConfig(t, tfbuddyYamlDiscover).Discover[0]
	var modified []string
	for i := 0; i <= maxListRootsPerRule; i++ {
		modified = append(modified, "services/svc"+strings.Repeat("x", i)+"/staging/main-us-west-2/main.tf")
	}
	if diff := cmp.Diff([]string{"services"}, rule.listRoots(modified)); diff != "" {
		t.Errorf("listRoots() mismatch (-want +got):\n%s", diff)
	}
}

func TestProjectConfig_discoverWorkspaces(t *testing.T) {
	cfg := mustLoadDiscoverConfig(t, tfbuddyYamlDiscover)
	repo := newFakeRepo()
	modified := []string{"services/api/staging/variables.tf"}

	if err := cfg.discoverWorkspaces(context.Background(), repo.list, modified); err != nil {
		t.Fatal(err)
	}
	// One listing finds the workspaces; the rest check that no other directory
	// derives the same names (see TestProjectConfig_discoverWorkspacesNameCollision).
	wantListed := []string{
		"services/api/staging",
		"services/api",
		"services/api-staging",
		"services/api-staging-main",
		"services/api-staging-main-us",
		"services/api-staging-global",
		"services/api-staging-global-us",
	}
	if diff := cmp.Diff(wantListed, repo.listed); diff != "" {
		t.Errorf("listed roots mismatch (-want +got):\n%s", diff)
	}
	wantAll := []string{
		"services-api-staging-global-us-east-1",
		"services-api-staging-main-us-west-2",
		"workspaces-root",
	}
	if diff := cmp.Diff(wantAll, workspaceNames(cfg.Workspaces)); diff != "" {
		t.Errorf("workspaces mismatch (-want +got):\n%s", diff)
	}

	var cell *TFCWorkspace
	for _, ws := range cfg.Workspaces {
		if ws.Name == "services-api-staging-main-us-west-2" {
			cell = ws
		}
	}
	want := &TFCWorkspace{
		Name:         "services-api-staging-main-us-west-2",
		Organization: "foo-corp",
		Dir:          "services/api/staging/main-us-west-2/",
		Mode:         "apply-before-merge",
		TriggerDirs:  []string{"services/api/staging", "services/api/staging/files/**"},
		AutoMerge:    true,
		Discovered:   true,
	}
	if diff := cmp.Diff(want, cell); diff != "" {
		t.Errorf("discovered workspace mismatch (-want +got):\n%s", diff)
	}

	// The env-level change triggers both cells of the env and nothing else.
	wantTriggered := []string{"services-api-staging-global-us-east-1", "services-api-staging-main-us-west-2"}
	if diff := cmp.Diff(wantTriggered, workspaceNames(cfg.triggeredWorkspaces(modified))); diff != "" {
		t.Errorf("triggered workspaces mismatch (-want +got):\n%s", diff)
	}
}

func TestProjectConfig_discoverWorkspacesMatchesExplicitConfig(t *testing.T) {
	// Discovery must trigger exactly what the equivalent explicit entries would.
	explicit := mustLoadDiscoverConfig(t, `---
workspaces:
  - {name: services-api-staging-main-us-west-2, organization: foo-corp, dir: services/api/staging/main-us-west-2/, triggerDirs: [services/api/staging, services/api/staging/files/**]}
  - {name: services-api-staging-global-us-east-1, organization: foo-corp, dir: services/api/staging/global-us-east-1/, triggerDirs: [services/api/staging, services/api/staging/files/**]}
  - {name: services-api-production-main-us-east-1, organization: foo-corp, dir: services/api/production/main-us-east-1/, triggerDirs: [services/api/production, services/api/production/files/**]}
  - {name: services-web-staging-main-us-west-2, organization: foo-corp, dir: services/web/staging/main-us-west-2/, triggerDirs: [services/web/staging, services/web/staging/files/**]}
`)
	for _, modified := range [][]string{
		{"services/api/staging/main-us-west-2/main.tf"},
		{"services/api/staging/variables.tf"},
		{"services/api/staging/files/k6/script.js"},
		{"services/api/production/main-us-east-1/main.tf", "services/web/staging/main-us-west-2/main.tf"},
		{"services/api/README.md"},
		{"README.md"},
		{"archive/services/api/staging/main-us-west-2/main.tf"},
		{"oldservices/api/staging/main-us-west-2/main.tf"},
	} {
		cfg := mustLoadDiscoverConfig(t, tfbuddyYamlDiscover)
		cfg.Workspaces = nil
		if err := cfg.discoverWorkspaces(context.Background(), newFakeRepo().list, modified); err != nil {
			t.Fatal(err)
		}
		want := workspaceNames(explicit.triggeredWorkspaces(modified))
		if diff := cmp.Diff(want, workspaceNames(cfg.triggeredWorkspaces(modified))); diff != "" {
			t.Errorf("%v: triggered workspaces mismatch (-explicit +discovered):\n%s", modified, diff)
		}
	}
}

func TestProjectConfig_discoverWorkspacesSkipsDeletedDirs(t *testing.T) {
	// An MR deleting a cell removes it from the branch, so it is not
	// discovered and nothing plans the now-empty directory.
	cfg := mustLoadDiscoverConfig(t, tfbuddyYamlDiscover)
	repo := newFakeRepo()
	modified := []string{"services/gone/staging/main-us-west-2/main.tf"}
	if err := cfg.discoverWorkspaces(context.Background(), repo.list, modified); err != nil {
		t.Fatal(err)
	}
	if got := cfg.triggeredWorkspaces(modified); len(got) != 0 {
		t.Errorf("expected no triggered workspaces, got %v", workspaceNames(got))
	}
}

func TestProjectConfig_discoverWorkspacesExplicitEntryWins(t *testing.T) {
	cfg := mustLoadDiscoverConfig(t, tfbuddyYamlDiscover+`  - name: legacy-api-staging-west
    organization: foo-corp
    dir: services/api/staging/main-us-west-2/
`)
	if err := cfg.discoverWorkspaces(context.Background(), newFakeRepo().list, []string{"services/api/staging/variables.tf"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"legacy-api-staging-west", "services-api-staging-global-us-east-1", "workspaces-root"}
	if diff := cmp.Diff(want, workspaceNames(cfg.Workspaces)); diff != "" {
		t.Errorf("workspaces mismatch (-want +got):\n%s", diff)
	}
}

func TestProjectConfig_discoverWorkspacesDuplicateName(t *testing.T) {
	// "a-b" + "c" and "a" + "b-c" fill services-{1}-{2} with the same name.
	cfg := mustLoadDiscoverConfig(t, `---
discover:
  - dir: services/*/*/
    name: services-{1}-{2}
    organization: foo-corp
`)
	repo := &fakeRepo{dirs: []string{"services/a-b/c", "services/a/b-c"}}
	err := cfg.discoverAllWorkspaces(context.Background(), repo.list)
	if err == nil || !strings.Contains(err.Error(), `"foo-corp/services-a-b-c"`) {
		t.Fatalf("expected a duplicate workspace error, got %v", err)
	}
}

func TestProjectConfig_discoverWorkspacesIsIdempotent(t *testing.T) {
	cfg := mustLoadDiscoverConfig(t, tfbuddyYamlDiscover)
	repo := newFakeRepo()
	if err := cfg.discoverWorkspaces(context.Background(), repo.list, []string{"services/api/staging/variables.tf"}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.discoverAllWorkspaces(context.Background(), repo.list); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"services-api-production-main-us-east-1",
		"services-api-staging-global-us-east-1",
		"services-api-staging-main-us-west-2",
		"services-web-staging-main-us-west-2",
		"workspaces-root",
	}
	if diff := cmp.Diff(want, workspaceNames(cfg.Workspaces)); diff != "" {
		t.Errorf("workspaces mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("services", repo.listed[len(repo.listed)-1]); diff != "" {
		t.Errorf("listed roots mismatch (-want +got):\n%s", diff)
	}
}

func TestProjectConfig_discoverWorkspacesListError(t *testing.T) {
	cfg := mustLoadDiscoverConfig(t, tfbuddyYamlDiscover)
	repo := &fakeRepo{err: errors.New("boom")}
	err := cfg.discoverWorkspaces(context.Background(), repo.list, []string{"services/api/staging/variables.tf"})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected the listing error, got %v", err)
	}
}

func TestProjectConfig_noDiscoverRulesListsNothing(t *testing.T) {
	cfg := mustLoadDiscoverConfig(t, tfbuddyYamlMultipleWorkspaces)
	repo := newFakeRepo()
	if err := cfg.discoverWorkspaces(context.Background(), repo.list, []string{"terraform/dev/main.tf"}); err != nil {
		t.Fatal(err)
	}
	if len(repo.listed) != 0 {
		t.Errorf("expected no listings without discover rules, got %v", repo.listed)
	}
}

func TestProjectConfig_discoverWorkspacesNameCollision(t *testing.T) {
	// "a-b" + "c" and "a" + "b-c" fill services-{1}-{2} with the same name. An
	// MR touching only services/a-b/c never lists services/a, so the check has
	// to look for the other directory itself.
	const rule = `---
discover:
  - dir: services/*/*/
    exclude: [services/a/b-c-excluded]
    name: services-{1}-{2}
    organization: foo-corp
`
	tests := []struct {
		name    string
		dirs    []string
		extra   string
		wantErr string
	}{
		{name: "collision outside the listed root", dirs: []string{"services/a-b", "services/a-b/c", "services/a", "services/a/b-c"},
			wantErr: `workspace "foo-corp/services-a-b-c" from both "services/a-b/c" and "services/a/b-c"`},
		{name: "no other directory", dirs: []string{"services/a-b", "services/a-b/c", "services/a", "services/a/other"}},
		{name: "explicit entry owns the other directory", dirs: []string{"services/a-b", "services/a-b/c", "services/a", "services/a/b-c"},
			extra: "workspaces:\n  - name: legacy-a\n    organization: foo-corp\n    dir: services/a/b-c\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := mustLoadDiscoverConfig(t, rule+tt.extra)
			repo := &fakeRepo{dirs: tt.dirs}
			err := cfg.discoverWorkspaces(context.Background(), repo.list, []string{"services/a-b/c/main.tf"})
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected an error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestProjectConfig_discoverWorkspacesNameCollisionExcluded(t *testing.T) {
	cfg := mustLoadDiscoverConfig(t, `---
discover:
  - dir: services/*/*/
    exclude: [services/a/b-c]
    name: services-{1}-{2}
    organization: foo-corp
`)
	repo := &fakeRepo{dirs: []string{"services/a-b", "services/a-b/c", "services/a", "services/a/b-c"}}
	if err := cfg.discoverWorkspaces(context.Background(), repo.list, []string{"services/a-b/c/main.tf"}); err != nil {
		t.Fatalf("an excluded directory is not a workspace, got %v", err)
	}
}

func TestProjectConfig_discoverWorkspacesNameCollisionAcrossRules(t *testing.T) {
	cfg := mustLoadDiscoverConfig(t, `---
discover:
  - dir: services/*/*/
    name: svc-{1}-{2}
    organization: foo-corp
  - dir: legacy/*/
    name: svc-{1}
    organization: foo-corp
`)
	repo := &fakeRepo{dirs: []string{"services/a", "services/a/b", "legacy", "legacy/a-b"}}
	err := cfg.discoverWorkspaces(context.Background(), repo.list, []string{"services/a/b/main.tf"})
	if err == nil || !strings.Contains(err.Error(), `"legacy/a-b"`) {
		t.Fatalf("expected a collision with legacy/a-b, got %v", err)
	}
}

func TestProjectConfig_discoverWorkspacesTooManySplitsListsEverything(t *testing.T) {
	// Adjacent placeholders split anywhere, so a long name has too many splits
	// to check one by one and the rule's whole tree is listed instead.
	cfg := mustLoadDiscoverConfig(t, `---
discover:
  - dir: services/*/*/*/
    name: "{1}{2}{3}"
    organization: foo-corp
`)
	repo := &fakeRepo{dirs: []string{
		"services/abcdefghij", "services/abcdefghij/klmnop", "services/abcdefghij/klmnop/qrstuv",
		"services/abcdefghijk", "services/abcdefghijk/lmnop", "services/abcdefghijk/lmnop/qrstuv",
	}}
	err := cfg.discoverWorkspaces(context.Background(), repo.list, []string{"services/abcdefghij/klmnop/qrstuv/main.tf"})
	if err == nil || !strings.Contains(err.Error(), `"foo-corp/abcdefghijklmnopqrstuv"`) {
		t.Fatalf("expected a duplicate workspace error, got %v", err)
	}
	if got := repo.listed[len(repo.listed)-1]; got != "services" {
		t.Errorf("expected the whole rule to be listed last, got %q", got)
	}
}

func TestDiscoverRule_nameSplits(t *testing.T) {
	rule := &DiscoverRule{Dir: "services/*/*/", Name: "services-{1}-{2}"}
	splits, complete := rule.nameSplits("services-a-b-c")
	want := [][]string{{"a", "b-c"}, {"a-b", "c"}}
	if !complete {
		t.Fatal("expected every split")
	}
	if diff := cmp.Diff(want, splits); diff != "" {
		t.Errorf("nameSplits() mismatch (-want +got):\n%s", diff)
	}
	if splits, _ := rule.nameSplits("other-a-b"); len(splits) != 0 {
		t.Errorf("expected no splits for a name the template cannot produce, got %v", splits)
	}

	repeated := &DiscoverRule{Dir: "services/*/", Name: "{1}-{1}"}
	if splits, _ := repeated.nameSplits("x-y-x-y"); !cmp.Equal(splits, [][]string{{"x-y"}}) {
		t.Errorf("a repeated placeholder must repeat its value, got %v", splits)
	}
}

func TestDiscoverRule_listRootsBraceAlternative(t *testing.T) {
	// The alternative spans a different number of path segments on each side,
	// so {1} and {2} cannot be located by position: list the whole rule.
	cfg := mustLoadDiscoverConfig(t, `---
discover:
  - dir: services/*/*/*/
    name: services-{1}-{2}-{3}
    organization: foo-corp
    triggerDirs:
      - "{shared/path,shared}/{1}/{2}"
`)
	got := cfg.Discover[0].listRoots([]string{"shared/api/staging/main.tf"})
	if diff := cmp.Diff([]string{"services"}, got); diff != "" {
		t.Errorf("listRoots() mismatch (-want +got):\n%s", diff)
	}
}

func TestProjectConfig_discoverWorkspacesExplicitDirWithoutSlash(t *testing.T) {
	cfg := mustLoadDiscoverConfig(t, tfbuddyYamlDiscover+`  - name: legacy-api-staging-west
    organization: foo-corp
    dir: services/api/staging/main-us-west-2
`)
	modified := []string{"services/api/staging/variables.tf"}
	if err := cfg.discoverWorkspaces(context.Background(), newFakeRepo().list, modified); err != nil {
		t.Fatal(err)
	}
	want := []string{"legacy-api-staging-west", "services-api-staging-global-us-east-1", "workspaces-root"}
	if diff := cmp.Diff(want, workspaceNames(cfg.Workspaces)); diff != "" {
		t.Errorf("workspaces mismatch (-want +got):\n%s", diff)
	}
}

func TestProjectConfig_discoverWorkspacesNameCollisionWithListedRoot(t *testing.T) {
	// The MR's own listing of services/a does not include services/a itself,
	// so it cannot show that services/a is absent.
	cfg := mustLoadDiscoverConfig(t, `---
discover:
  - dir: services/*/*/
    name: svc-{1}-{2}
    organization: foo-corp
  - dir: services/*/
    name: svc-{1}-b
    organization: foo-corp
`)
	repo := &fakeRepo{dirs: []string{"services", "services/a", "services/a/b"}}
	err := cfg.discoverWorkspaces(context.Background(), repo.list, []string{"services/a/b/main.tf"})
	if err == nil || !strings.Contains(err.Error(), `"services/a"`) {
		t.Fatalf("expected a collision with services/a, got %v", err)
	}
}

func TestDiscoverRule_nameSplitsBoundsFailedSearch(t *testing.T) {
	// Eight adjacent placeholders before a literal the name lacks: every way
	// of splitting the name fails, and there are hundreds of millions of them.
	r := &DiscoverRule{Dir: "d/*/*/*/*/*/*/*/*/", Name: "{1}{2}{3}{4}{5}{6}{7}{8}-x"}
	start := time.Now()
	splits, complete := r.nameSplits(strings.Repeat("abcdefghij", 6))
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("nameSplits took %v", elapsed)
	}
	if complete || len(splits) != 0 {
		t.Fatalf("expected an incomplete, empty search, got complete=%v splits=%d", complete, len(splits))
	}

	// A long name in the usual layout still gets every split.
	usual := &DiscoverRule{Dir: "services/*/*/*/", Name: "services-{1}-{2}-{3}"}
	if _, complete := usual.nameSplits("services-very-long-service-name-production-main-ap-southeast-2"); !complete {
		t.Fatal("expected every split of a long conventional name")
	}
}

func Test_loadProjectConfig_emptyEntries(t *testing.T) {
	tests := map[string]string{
		"null discover rule":  "discover: [null]\n",
		"blank discover rule": "discover:\n  -\n",
		"null workspace":      "workspaces: [null]\n",
		"blank workspace":     "workspaces:\n  - name: a\n    organization: foo-corp\n  -\n",
	}
	for name, yaml := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := loadProjectConfig(config.C, []byte(yaml))
			if err == nil || !strings.Contains(err.Error(), "is empty") {
				t.Fatalf("expected an empty-entry error, got %v", err)
			}
		})
	}
}

func Test_placeholderInClass(t *testing.T) {
	tests := map[string]bool{
		"shared/[{1}]":       true,
		"shared/x[a{1}]/y":   true,
		"shared/{1}":         false,
		"shared/[ab]/{1}":    false,
		`shared/\[{1}\]`:     false,
		"{shared,other}/{1}": false,
		"services/{1}/[!.]*": false,
	}
	for glob, want := range tests {
		if got := placeholderInClass(glob); got != want {
			t.Errorf("placeholderInClass(%q) = %v, want %v", glob, got, want)
		}
	}
}

func TestProjectConfig_discoverWorkspacesTwoNamesForOneDir(t *testing.T) {
	// Each rule is triggered by its own dependency. Without the check, a
	// change to blue/ would plan services/api/prod as blue-api-prod and a
	// change to green/ would plan it as green-api-prod.
	const rules = `---
discover:
  - dir: services/*/*/
    name: blue-{1}-{2}
    organization: foo-corp
    triggerDirs:
      - blue/{1}/{2}
  - dir: services/*/*/
    name: green-{1}-{2}
    organization: foo-corp
    triggerDirs:
      - green/{1}/{2}
`
	for _, modified := range []string{"blue/api/prod/x.tf", "green/api/prod/x.tf", "services/api/prod/main.tf"} {
		cfg := mustLoadDiscoverConfig(t, rules)
		repo := &fakeRepo{dirs: []string{"services", "services/api", "services/api/prod"}}
		err := cfg.discoverWorkspaces(context.Background(), repo.list, []string{modified})
		if err == nil || !strings.Contains(err.Error(), `for dir "services/api/prod"`) && !strings.Contains(err.Error(), `for dir "services/api/prod/"`) {
			t.Errorf("%s: expected an error naming services/api/prod, got %v", modified, err)
		}
	}

	// Excluding the directory from one rule resolves it.
	cfg := mustLoadDiscoverConfig(t, strings.Replace(rules, "    name: green-{1}-{2}\n", "    name: green-{1}-{2}\n    exclude: [services/api/prod]\n", 1))
	repo := &fakeRepo{dirs: []string{"services", "services/api", "services/api/prod"}}
	if err := cfg.discoverWorkspaces(context.Background(), repo.list, []string{"blue/api/prod/x.tf"}); err != nil {
		t.Fatalf("expected the exclusion to resolve it, got %v", err)
	}
}

func TestDiscoverRule_listRootsLeadingWildcardSuffix(t *testing.T) {
	// The suffix "foo/api" of "x/foo/api" may start part-way into a
	// directory name ("oo/api"), so the leading wildcard stays unset and the
	// whole repo is listed.
	cfg := mustLoadDiscoverConfig(t, `---
discover:
  - dir: "*/api/"
    name: "{1}-api"
    organization: foo-corp
`)
	if diff := cmp.Diff([]string{""}, cfg.Discover[0].listRoots([]string{"x/foo/api/main.tf"})); diff != "" {
		t.Errorf("listRoots() mismatch (-want +got):\n%s", diff)
	}
}

func TestProjectConfig_discoverWorkspacesSuffixMatchesExplicitConfig(t *testing.T) {
	// The example from review: an explicit services/api/ is triggered by a
	// change in archive/services/api/, so the discovered one must be too.
	modified := []string{"archive/services/api/main.tf"}
	explicit := mustLoadDiscoverConfig(t, "workspaces:\n  - name: services-api\n    organization: foo-corp\n    dir: services/api/\n")
	cfg := mustLoadDiscoverConfig(t, `---
discover:
  - dir: services/*/
    name: services-{1}
    organization: foo-corp
`)
	repo := &fakeRepo{dirs: []string{"services", "services/api", "archive", "archive/services", "archive/services/api"}}
	if err := cfg.discoverWorkspaces(context.Background(), repo.list, modified); err != nil {
		t.Fatal(err)
	}
	want := workspaceNames(explicit.triggeredWorkspaces(modified))
	if diff := cmp.Diff([]string{"services-api"}, want); diff != "" {
		t.Fatalf("explicit config no longer suffix-matches (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(want, workspaceNames(cfg.triggeredWorkspaces(modified))); diff != "" {
		t.Errorf("triggered workspaces mismatch (-explicit +discovered):\n%s", diff)
	}
}

func TestProjectConfig_discoverWorkspacesTwoOrganizationsForOneDir(t *testing.T) {
	// Same directory, same name, different organizations: two different
	// workspaces, chosen by whichever rule's dependency an MR touched.
	const rules = `---
discover:
  - dir: services/*/
    name: svc-{1}
    organization: org-a
    triggerDirs:
      - blue/{1}
  - dir: services/*/
    name: svc-{1}
    organization: org-b
    triggerDirs:
      - green/{1}
`
	dirs := []string{"services", "services/api"}
	for _, modified := range []string{"blue/api/x.tf", "green/api/x.tf", "services/api/main.tf"} {
		cfg := mustLoadDiscoverConfig(t, rules)
		err := cfg.discoverWorkspaces(context.Background(), (&fakeRepo{dirs: dirs}).list, []string{modified})
		if err == nil || !strings.Contains(err.Error(), `"org-a/svc-api"`) || !strings.Contains(err.Error(), `"org-b/svc-api"`) {
			t.Errorf("%s: expected an error naming both workspaces, got %v", modified, err)
		}
	}
	cfg := mustLoadDiscoverConfig(t, rules)
	if err := cfg.discoverAllWorkspaces(context.Background(), (&fakeRepo{dirs: dirs}).list); err == nil {
		t.Error("full discovery: expected an error for one directory in two organizations")
	}

	// An explicit entry for the directory still takes precedence.
	cfg = mustLoadDiscoverConfig(t, rules+"workspaces:\n  - name: svc-api\n    organization: org-a\n    dir: services/api\n")
	if err := cfg.discoverWorkspaces(context.Background(), (&fakeRepo{dirs: dirs}).list, []string{"green/api/x.tf"}); err != nil {
		t.Fatalf("expected the explicit entry to take precedence, got %v", err)
	}
	if got := cfg.triggeredWorkspaces([]string{"services/api/main.tf"}); len(got) != 1 || workspaceID(got[0]) != "org-a/svc-api" {
		t.Errorf("expected only the explicit org-a/svc-api, got %v", got)
	}
}

func TestProjectConfig_discoverWorkspacesSameNameOtherOrganization(t *testing.T) {
	// "a-b" + "c" and "a" + "b-c" fill svc-{1}-{2} and svc-{1} with the same
	// name, but in different organizations they are different workspaces.
	cfg := mustLoadDiscoverConfig(t, `---
discover:
  - dir: services/*/*/
    name: svc-{1}-{2}
    organization: org-a
  - dir: legacy/*/
    name: svc-{1}
    organization: org-b
`)
	repo := &fakeRepo{dirs: []string{"services/a", "services/a/b", "legacy", "legacy/a-b"}}
	if err := cfg.discoverWorkspaces(context.Background(), repo.list, []string{"services/a/b/main.tf"}); err != nil {
		t.Fatalf("expected no collision across organizations, got %v", err)
	}
}

func TestProjectConfig_discoverWorkspacesLeadingWildcardSameDepthSuffix(t *testing.T) {
	// The example from review: oldfoo/api/ ends with foo/api/, so the
	// explicit foo/api/ is triggered even though oldfoo/api is excluded.
	modified := []string{"oldfoo/api/main.tf"}
	explicit := mustLoadDiscoverConfig(t, "workspaces:\n  - name: foo-api\n    organization: foo-corp\n    dir: foo/api/\n")
	cfg := mustLoadDiscoverConfig(t, `---
discover:
  - dir: "*/api/"
    name: "{1}-api"
    organization: foo-corp
    exclude: [oldfoo/api]
`)
	repo := &fakeRepo{dirs: []string{"foo", "foo/api", "oldfoo", "oldfoo/api"}}
	if err := cfg.discoverWorkspaces(context.Background(), repo.list, modified); err != nil {
		t.Fatal(err)
	}
	want := workspaceNames(explicit.triggeredWorkspaces(modified))
	if diff := cmp.Diff([]string{"foo-api"}, want); diff != "" {
		t.Fatalf("explicit config no longer suffix-matches (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(want, workspaceNames(cfg.triggeredWorkspaces(modified))); diff != "" {
		t.Errorf("triggered workspaces mismatch (-explicit +discovered):\n%s", diff)
	}
}
