package tfc_trigger

import (
	"errors"
	"strings"
	"testing"

	"github.com/zapier/tfbuddy/pkg/utils"
	"github.com/zapier/tfbuddy/pkg/vcs"
)

const testAutomodeYAML = `
workspaces:
  - name: ignored-here
automode:
  authors:
    - username: tags-bot
      commitEmails: [tags-bot@example.com]
    - username: new-service-bot
      commitEmails: [new-service-bot@example.com]
    - username: renovate-bot
      commitEmails: [renovate@example.com]
  classes:
    - name: tags
      authors: [tags-bot]
      paths: ["services/*/*/*/tags.auto.tfvars.json"]
    - name: new-service
      authors: [new-service-bot]
      paths: ["services/**"]
    - name: empty-plan
      authors: [renovate-bot]
      paths: ["**/versions.tf", "**/.terraform.lock.hcl"]
`

func mustParseAutomode(t *testing.T) *AutomodeConfig {
	t.Helper()
	cfg, err := parseAutomodeConfig([]byte(testAutomodeYAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		t.Fatal("expected a automode section")
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

func TestAutomodeClassFor(t *testing.T) {
	cfg := mustParseAutomode(t)
	tagsFile := "services/api/prod/main-us-east-1/tags.auto.tfvars.json"

	tests := []struct {
		name    string
		author  string
		files   []string
		commits []vcs.MRCommit
		want    string
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
			want:    ClassReviewed,
		},
		{
			name:    "a commit by another author",
			author:  "tags-bot",
			files:   []string{tagsFile},
			commits: commitsBy("tags-bot@example.com", "someone@example.com"),
			want:    ClassReviewed,
		},
		{
			name:   "a commit committed by another identity",
			author: "tags-bot",
			files:  []string{tagsFile},
			commits: []vcs.MRCommit{
				{SHA: "a", AuthorEmail: "tags-bot@example.com", CommitterEmail: "someone@example.com"},
			},
			want: ClassReviewed,
		},
		{
			name:    "the files fit a class the author is not on",
			author:  "renovate-bot",
			files:   []string{tagsFile},
			commits: commitsBy("renovate@example.com"),
			want:    ClassReviewed,
		},
		{
			name:    "first matching class wins",
			author:  "renovate-bot",
			files:   []string{"services/api/prod/main-us-east-1/versions.tf", "services/api/prod/main-us-east-1/.terraform.lock.hcl"},
			commits: commitsBy("renovate@example.com"),
			want:    "empty-plan",
		},
		{
			name:    "no commits never counts as the author's",
			author:  "tags-bot",
			files:   []string{tagsFile},
			commits: nil,
			want:    ClassReviewed,
		},
		{
			name:    "no changed files never matches a class",
			author:  "tags-bot",
			files:   nil,
			commits: commitsBy("tags-bot@example.com"),
			want:    ClassReviewed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			author := cfg.Author(tt.author)
			if author == nil {
				t.Fatalf("%s should be an automode author", tt.author)
			}
			if got := cfg.ClassFor(author, tt.files, tt.commits); got != tt.want {
				t.Errorf("ClassFor() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAutomodeAuthorNotListed(t *testing.T) {
	cfg := mustParseAutomode(t)
	if a := cfg.Author("a-person"); a != nil {
		t.Fatalf("a-person is not on the list, got %+v", a)
	}
	var none *AutomodeConfig
	if a := none.Author("tags-bot"); a != nil {
		t.Fatal("a repo without a automode section has no automode authors")
	}
}

func TestParseAutomodeConfig(t *testing.T) {
	cfg, err := parseAutomodeConfig([]byte("workspaces:\n  - name: ws\n"))
	if err != nil || cfg != nil {
		t.Fatalf("a file without automode has no automode config, got %+v, %v", cfg, err)
	}

	invalid := map[string]string{
		"reserved name": `automode:
  authors: [{username: bot}]
  classes: [{name: reviewed, authors: [bot], paths: ["**"]}]`,
		"name a policy cannot read": `automode:
  authors: [{username: bot}]
  classes: [{name: "tags]", authors: [bot], paths: ["**"]}]`,
		"repeated name": `automode:
  authors: [{username: bot}]
  classes:
    - {name: tags, authors: [bot], paths: ["a"]}
    - {name: tags, authors: [bot], paths: ["b"]}`,
		"no paths": `automode:
  authors: [{username: bot}]
  classes: [{name: tags, authors: [bot]}]`,
		"bad glob": `automode:
  authors: [{username: bot}]
  classes: [{name: tags, authors: [bot], paths: ["services/[a"]}]`,
		"author without username": `automode:
  authors: [{commitEmails: [a@example.com]}]`,
	}
	for name, body := range invalid {
		t.Run(name, func(t *testing.T) {
			_, err := parseAutomodeConfig([]byte(body))
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

	automode := &TFCTrigger{cfg: &TFCTriggerOptions{MergeRequestIID: 7, CommitSHA: "1a2b3c4", Automode: true, AutomodeActor: "tags-bot", ChangeClass: "tags"}}
	got, err = automode.runMessage("Sync tags [tfbuddy-class=reviewed]")
	if err != nil {
		t.Fatal(err)
	}
	if got != "MR [!7] [tfbuddy-class=tags] sha=1a2b3c4 actor=tags-bot" {
		t.Errorf("automode message = %q, want fixed fields only and no title", got)
	}

	noClass := &TFCTrigger{cfg: &TFCTriggerOptions{MergeRequestIID: 7, Automode: true}}
	if _, err := noClass.runMessage("Sync tags"); !errors.Is(err, utils.ErrPermanent) {
		t.Errorf("an automode run without a class must be refused, got %v", err)
	}
}
