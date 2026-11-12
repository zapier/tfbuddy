package tfc_trigger

import (
	"errors"
	"strings"
	"testing"

	"github.com/zapier/tfbuddy/pkg/utils"
	"github.com/zapier/tfbuddy/pkg/vcs"
)

const testAutoModeYAML = `
workspaces:
  - name: ignored-here
autoMode:
  authors:
    - username: tags-bot
      commitEmails: [tags-bot@example.com]
    - username: renovate-bot
      commitEmails: [renovate@example.com]
  classes:
    - name: tags
      authors: [tags-bot]
      paths: ["services/*/*/*/tags.auto.tfvars.json"]
      workspaces: ["services-*"]
    - name: renovate-safe-plan
      authors: [renovate-bot]
      paths: ["**/versions.tf", "**/.terraform.lock.hcl"]
      workspaces: ["services-*", "workspaces-services-*"]
`

func mustParseAutoMode(t *testing.T) *AutoModeConfig {
	t.Helper()
	cfg, err := parseAutoModeConfig([]byte(testAutoModeYAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		t.Fatal("expected an Auto Mode section")
	}
	return cfg
}

func commitsBy(emails ...string) []vcs.MRCommit {
	commits := make([]vcs.MRCommit, 0, len(emails))
	for i, e := range emails {
		commits = append(commits, vcs.MRCommit{SHA: strings.Repeat("a", i+1), AuthorEmail: e, CommitterEmail: e})
	}
	return commits
}

func TestAutoModeClassFor(t *testing.T) {
	cfg := mustParseAutoMode(t)
	tagsFile := "services/api/prod/main-us-east-1/tags.auto.tfvars.json"

	tests := []struct {
		name    string
		author  string
		files   []string
		commits []vcs.MRCommit
		// workspaces are the affected workspaces; nil means one services
		// workspace.
		workspaces []string
		want       string
	}{
		{
			name:    "every file matches the author's class",
			author:  "tags-bot",
			files:   []string{tagsFile, "services/api/staging/main-us-east-1/tags.auto.tfvars.json"},
			commits: commitsBy("tags-bot@example.com"),
			want:    "tags",
		},
		{
			name:    "usernames and emails match case-insensitively",
			author:  "Tags-Bot",
			files:   []string{tagsFile},
			commits: commitsBy("TAGS-BOT@example.com"),
			want:    "tags",
		},
		{
			name:    "one file outside the class paths",
			author:  "tags-bot",
			files:   []string{tagsFile, "services/api/prod/main-us-east-1/main.tf"},
			commits: commitsBy("tags-bot@example.com"),
			want:    ClassNone,
		},
		{
			name:    "a commit by another author",
			author:  "tags-bot",
			files:   []string{tagsFile},
			commits: commitsBy("tags-bot@example.com", "someone@example.com"),
			want:    ClassNone,
		},
		{
			name:   "a commit committed by another identity",
			author: "tags-bot",
			files:  []string{tagsFile},
			commits: []vcs.MRCommit{
				{SHA: "a", AuthorEmail: "tags-bot@example.com", CommitterEmail: "someone@example.com"},
			},
			want: ClassNone,
		},
		{
			name:    "the files fit a class the author is not on",
			author:  "renovate-bot",
			files:   []string{tagsFile},
			commits: commitsBy("renovate@example.com"),
			want:    ClassNone,
		},
		{
			name:    "first matching class wins",
			author:  "renovate-bot",
			files:   []string{"services/api/prod/main-us-east-1/versions.tf", "services/api/prod/main-us-east-1/.terraform.lock.hcl"},
			commits: commitsBy("renovate@example.com"),
			want:    "renovate-safe-plan",
		},
		{
			name:    "no commits never counts as the author's",
			author:  "tags-bot",
			files:   []string{tagsFile},
			commits: nil,
			want:    ClassNone,
		},
		{
			name:    "no changed files never matches a class",
			author:  "tags-bot",
			files:   nil,
			commits: commitsBy("tags-bot@example.com"),
			want:    ClassNone,
		},
		{
			name:       "every workspace is in the class",
			author:     "renovate-bot",
			files:      []string{"services/api/prod/main-us-east-1/versions.tf"},
			commits:    commitsBy("renovate@example.com"),
			workspaces: []string{"services-api-prod", "workspaces-services-api"},
			want:       "renovate-safe-plan",
		},
		{
			name:       "one workspace outside the class",
			author:     "tags-bot",
			files:      []string{tagsFile},
			commits:    commitsBy("tags-bot@example.com"),
			workspaces: []string{"services-api-prod", "platform-network"},
			want:       ClassNone,
		},
		{
			name:       "a workspace the author's class does not list, though another class does",
			author:     "tags-bot",
			files:      []string{tagsFile},
			commits:    commitsBy("tags-bot@example.com"),
			workspaces: []string{"workspaces-services-api"},
			want:       ClassNone,
		},
		{
			name:       "no affected workspace never matches a class",
			author:     "tags-bot",
			files:      []string{tagsFile},
			commits:    commitsBy("tags-bot@example.com"),
			workspaces: []string{},
			want:       ClassNone,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			author := cfg.Author(tt.author)
			if author == nil {
				t.Fatalf("%s should be an Auto Mode author", tt.author)
			}
			workspaces := tt.workspaces
			if workspaces == nil {
				workspaces = []string{"services-api-prod-main-us-east-1"}
			}
			got, reason := cfg.ClassFor(author, tt.files, tt.commits, workspaces)
			if got != tt.want {
				t.Errorf("ClassFor() = %q, want %q", got, tt.want)
			}
			// A merge request that fits no class says why: the reason goes in
			// the Manual Mode comment.
			if (got == ClassNone) == (reason == "") {
				t.Errorf("ClassFor() reason = %q for class %q", reason, got)
			}
		})
	}
}

// Whatever a class's paths say, a change to CI or Async Agent config puts the
// merge request in Manual Mode.
func TestAutoModeClassFor_DeniedPaths(t *testing.T) {
	cfg, err := parseAutoModeConfig([]byte(`autoMode:
  authors: [{username: tags-bot, commitEmails: [tags-bot@example.com]}]
  classes: [{name: tags, authors: [tags-bot], paths: ["**"], workspaces: ["**"]}]`))
	if err != nil {
		t.Fatal(err)
	}
	author := cfg.Author("tags-bot")
	tagsFile := "services/api/prod/main-us-east-1/tags.auto.tfvars.json"
	ws := []string{"services-api"}
	if got, _ := cfg.ClassFor(author, []string{tagsFile}, commitsBy("tags-bot@example.com"), ws); got != "tags" {
		t.Fatalf("ClassFor() = %q, want tags for a file that is not denied", got)
	}

	for _, denied := range []string{
		".gitlab-ci.yml",
		"services/api/.gitlab-ci.yml",
		".gitlab/ci/plan.yml",
		"services/api/.gitlab/CODEOWNERS",
		".async-agent/config.yaml",
		"services/api/.async-agent/rules.md",
		".GitLab-CI.yml",
		".Async-Agent/config.yaml",
		"/.gitlab-ci.yml",
	} {
		t.Run(denied, func(t *testing.T) {
			if !AutoModeDeniedPath(denied) {
				t.Errorf("%s should be a denied path", denied)
			}
			got, reason := cfg.ClassFor(author, []string{tagsFile, denied}, commitsBy("tags-bot@example.com"), ws)
			if got != ClassNone || !strings.Contains(reason, denied) {
				t.Errorf("ClassFor() = %q (%q), want Manual Mode for an MR that changes %s", got, reason, denied)
			}
		})
	}

	for _, allowed := range []string{"services/api/gitlab-ci.yml", "services/my.gitlab/main.tf", "docs/.gitlab-ci.yml.md", "async-agent/x"} {
		if AutoModeDeniedPath(allowed) {
			t.Errorf("%s should not be a denied path", allowed)
		}
	}
}

// A class that lists no workspaces matches no merge request.
func TestAutoModeClassFor_NoWorkspacesFailsClosed(t *testing.T) {
	cfg, err := parseAutoModeConfig([]byte(`autoMode:
  authors: [{username: tags-bot, commitEmails: [tags-bot@example.com]}]
  classes: [{name: tags, authors: [tags-bot], paths: ["**"]}]`))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := cfg.ClassFor(cfg.Author("tags-bot"), []string{"services/api/tags.auto.tfvars.json"}, commitsBy("tags-bot@example.com"), []string{"services-api"})
	if got != ClassNone {
		t.Fatalf("ClassFor() = %q, want no class for a class without workspaces", got)
	}
}

func TestAutoModeAuthorNotListed(t *testing.T) {
	cfg := mustParseAutoMode(t)
	if a := cfg.Author("a-person"); a != nil {
		t.Fatalf("a-person is not on the list, got %+v", a)
	}
	var none *AutoModeConfig
	if a := none.Author("tags-bot"); a != nil {
		t.Fatal("a repo without an Auto Mode section has no Auto Mode authors")
	}
}

func TestParseAutoModeConfig(t *testing.T) {
	cfg, err := parseAutoModeConfig([]byte("workspaces:\n  - name: ws\n"))
	if err != nil || cfg != nil {
		t.Fatalf("a file without Auto Mode has no Auto Mode config, got %+v, %v", cfg, err)
	}

	invalid := map[string]string{
		"name a policy cannot read": `autoMode:
  authors: [{username: bot}]
  classes: [{name: "tags]", authors: [bot], paths: ["**"]}]`,
		"repeated name": `autoMode:
  authors: [{username: bot}]
  classes:
    - {name: tags, authors: [bot], paths: ["a"]}
    - {name: tags, authors: [bot], paths: ["b"]}`,
		"no paths": `autoMode:
  authors: [{username: bot}]
  classes: [{name: tags, authors: [bot]}]`,
		"bad glob": `autoMode:
  authors: [{username: bot}]
  classes: [{name: tags, authors: [bot], paths: ["services/[a"]}]`,
		"bad workspace glob": `autoMode:
  authors: [{username: bot}]
  classes: [{name: tags, authors: [bot], paths: ["**"], workspaces: ["services-[a"]}]`,
		"author without username": `autoMode:
  authors: [{commitEmails: [a@example.com]}]`,
	}
	for name, body := range invalid {
		t.Run(name, func(t *testing.T) {
			_, err := parseAutoModeConfig([]byte(body))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !errors.Is(err, utils.ErrPermanent) {
				t.Errorf("a bad config is permanent, got %v", err)
			}
		})
	}
}

func TestRunMessage(t *testing.T) {
	mode1 := &TFCTrigger{cfg: &TFCTriggerOptions{MergeRequestIID: 7}}
	got, err := mode1.runMessage("Add bucket [tfbuddy-class=tags]")
	if err != nil {
		t.Fatal(err)
	}
	if got != "MR [!7]: Add bucket [tfbuddy-class=tags]" {
		t.Errorf("Mode 1 message = %q, want the title unchanged", got)
	}

	autoMode := &TFCTrigger{cfg: &TFCTriggerOptions{MergeRequestIID: 7, CommitSHA: "1a2b3c4", AutoMode: true, AutoModeAuthor: "tags-bot", ChangeClass: "tags"}}
	got, err = autoMode.runMessage("Sync tags [tfbuddy-class=renovate-safe-plan]")
	if err != nil {
		t.Fatal(err)
	}
	if got != "MR [!7] [tfbuddy-class=tags] sha=1a2b3c4 author=tags-bot" {
		t.Errorf("Auto Mode message = %q, want fixed fields only and no title", got)
	}

	noClass := &TFCTrigger{cfg: &TFCTriggerOptions{MergeRequestIID: 7, AutoMode: true}}
	if _, err := noClass.runMessage("Sync tags"); !errors.Is(err, utils.ErrPermanent) {
		t.Errorf("an Auto Mode run without a class must be refused, got %v", err)
	}
}

func TestAutoModePersonApprover(t *testing.T) {
	cfg, err := parseAutoModeConfig([]byte(`autoMode:
  authors: [{username: tags-bot, commitEmails: [tags-bot@example.com]}]
  automaticApprovers: [appsec-bot]
  classes: [{name: tags, authors: [tags-bot], paths: ["**"], workspaces: ["**"]}]`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		approvers []string
		want      string
	}{
		{approvers: nil, want: ""},
		{approvers: []string{"appsec-bot", "Tags-Bot", "APPSEC-BOT"}, want: ""},
		{approvers: []string{"appsec-bot", "alice"}, want: "alice"},
	} {
		if got := cfg.PersonApprover(tt.approvers); got != tt.want {
			t.Errorf("PersonApprover(%q) = %q, want %q", tt.approvers, got, tt.want)
		}
	}
	if _, err := parseAutoModeConfig([]byte("autoMode:\n  automaticApprovers: [\"\"]\n")); err == nil {
		t.Error("an empty automatic approver must be refused")
	}
	if _, err := parseAutoModeConfig([]byte("autoMode:\n  requiredJobs: [\" \"]\n")); err == nil {
		t.Error("an empty required job must be refused")
	}
}
