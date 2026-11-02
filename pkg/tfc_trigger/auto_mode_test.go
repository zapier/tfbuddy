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
    - name: safe-plan
      authors: [renovate-bot]
      paths: ["**/versions.tf", "**/.terraform.lock.hcl"]
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
			want:    "safe-plan",
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
				t.Fatalf("%s should be an Auto Mode author", tt.author)
			}
			if got := cfg.ClassFor(author, tt.files, tt.commits); got != tt.want {
				t.Errorf("ClassFor() = %q, want %q", got, tt.want)
			}
		})
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
		"reserved name": `autoMode:
  authors: [{username: bot}]
  classes: [{name: reviewed, authors: [bot], paths: ["**"]}]`,
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

	autoMode := &TFCTrigger{cfg: &TFCTriggerOptions{MergeRequestIID: 7, CommitSHA: "1a2b3c4", AutoMode: true, AutoModeActor: "tags-bot", ChangeClass: "tags"}}
	got, err = autoMode.runMessage("Sync tags [tfbuddy-class=reviewed]")
	if err != nil {
		t.Fatal(err)
	}
	if got != "MR [!7] [tfbuddy-class=tags] sha=1a2b3c4 actor=tags-bot" {
		t.Errorf("Auto Mode message = %q, want fixed fields only and no title", got)
	}

	noClass := &TFCTrigger{cfg: &TFCTriggerOptions{MergeRequestIID: 7, AutoMode: true}}
	if _, err := noClass.runMessage("Sync tags"); !errors.Is(err, utils.ErrPermanent) {
		t.Errorf("an Auto Mode run without a class must be refused, got %v", err)
	}
}
