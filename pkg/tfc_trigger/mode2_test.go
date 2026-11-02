package tfc_trigger

import (
	"errors"
	"strings"
	"testing"

	"github.com/zapier/tfbuddy/pkg/utils"
	"github.com/zapier/tfbuddy/pkg/vcs"
)

const testMode2YAML = `
workspaces:
  - name: ignored-here
mode2:
  authors:
    - username: service-atlas-bot
      commitEmails: [atlas@example.com]
    - username: selfserve-bot
      commitEmails: [selfserve@example.com]
    - username: zapier-renovate-bot
      commitEmails: [renovate@example.com]
  classes:
    - name: tags
      authors: [service-atlas-bot]
      paths: ["services/*/*/*/tags.auto.tfvars.json"]
    - name: new-service
      authors: [selfserve-bot]
      paths: ["services/**"]
    - name: empty-plan
      authors: [zapier-renovate-bot]
      paths: ["**/versions.tf", "**/.terraform.lock.hcl"]
`

func mustParseMode2(t *testing.T) *Mode2Config {
	t.Helper()
	cfg, err := parseMode2Config([]byte(testMode2YAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		t.Fatal("expected a mode2 section")
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

func TestMode2ClassFor(t *testing.T) {
	cfg := mustParseMode2(t)
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
			author:  "service-atlas-bot",
			files:   []string{tagsFile, "services/api/staging/main-us-east-1/tags.auto.tfvars.json"},
			commits: commitsBy("atlas@example.com"),
			want:    "tags",
		},
		{
			name:    "usernames and emails match case-insensitively",
			author:  "Service-Atlas-Bot",
			files:   []string{tagsFile},
			commits: commitsBy("ATLAS@example.com"),
			want:    "tags",
		},
		{
			name:    "one file outside the class paths",
			author:  "service-atlas-bot",
			files:   []string{tagsFile, "services/api/prod/main-us-east-1/main.tf"},
			commits: commitsBy("atlas@example.com"),
			want:    ClassReviewed,
		},
		{
			name:    "a commit by another author",
			author:  "service-atlas-bot",
			files:   []string{tagsFile},
			commits: commitsBy("atlas@example.com", "someone@example.com"),
			want:    ClassReviewed,
		},
		{
			name:   "a commit committed by another identity",
			author: "service-atlas-bot",
			files:  []string{tagsFile},
			commits: []vcs.MRCommit{
				{SHA: "a", AuthorEmail: "atlas@example.com", CommitterEmail: "someone@example.com"},
			},
			want: ClassReviewed,
		},
		{
			name:    "the files fit a class the author is not on",
			author:  "zapier-renovate-bot",
			files:   []string{tagsFile},
			commits: commitsBy("renovate@example.com"),
			want:    ClassReviewed,
		},
		{
			name:    "first matching class wins",
			author:  "zapier-renovate-bot",
			files:   []string{"services/api/prod/main-us-east-1/versions.tf", "services/api/prod/main-us-east-1/.terraform.lock.hcl"},
			commits: commitsBy("renovate@example.com"),
			want:    "empty-plan",
		},
		{
			name:    "no commits never counts as the author's",
			author:  "service-atlas-bot",
			files:   []string{tagsFile},
			commits: nil,
			want:    ClassReviewed,
		},
		{
			name:    "no changed files never matches a class",
			author:  "service-atlas-bot",
			files:   nil,
			commits: commitsBy("atlas@example.com"),
			want:    ClassReviewed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			author := cfg.Author(tt.author)
			if author == nil {
				t.Fatalf("%s should be a Mode 2 author", tt.author)
			}
			if got := cfg.ClassFor(author, tt.files, tt.commits); got != tt.want {
				t.Errorf("ClassFor() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMode2AuthorNotListed(t *testing.T) {
	cfg := mustParseMode2(t)
	if a := cfg.Author("a-person"); a != nil {
		t.Fatalf("a-person is not on the list, got %+v", a)
	}
	var none *Mode2Config
	if a := none.Author("service-atlas-bot"); a != nil {
		t.Fatal("a repo without a mode2 section has no Mode 2 authors")
	}
}

func TestParseMode2Config(t *testing.T) {
	cfg, err := parseMode2Config([]byte("workspaces:\n  - name: ws\n"))
	if err != nil || cfg != nil {
		t.Fatalf("a file without mode2 has no Mode 2 config, got %+v, %v", cfg, err)
	}

	invalid := map[string]string{
		"reserved name": `mode2:
  authors: [{username: bot}]
  classes: [{name: reviewed, authors: [bot], paths: ["**"]}]`,
		"name a policy cannot read": `mode2:
  authors: [{username: bot}]
  classes: [{name: "tags]", authors: [bot], paths: ["**"]}]`,
		"repeated name": `mode2:
  authors: [{username: bot}]
  classes:
    - {name: tags, authors: [bot], paths: ["a"]}
    - {name: tags, authors: [bot], paths: ["b"]}`,
		"no paths": `mode2:
  authors: [{username: bot}]
  classes: [{name: tags, authors: [bot]}]`,
		"bad glob": `mode2:
  authors: [{username: bot}]
  classes: [{name: tags, authors: [bot], paths: ["services/[a"]}]`,
		"author without username": `mode2:
  authors: [{commitEmails: [a@example.com]}]`,
	}
	for name, body := range invalid {
		t.Run(name, func(t *testing.T) {
			_, err := parseMode2Config([]byte(body))
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

	mode2 := &TFCTrigger{cfg: &TFCTriggerOptions{MergeRequestIID: 7, CommitSHA: "1a2b3c4", Mode2: true, Mode2Actor: "service-atlas-bot", ChangeClass: "tags"}}
	got, err = mode2.runMessage("Sync tags [tfbuddy-class=reviewed]")
	if err != nil {
		t.Fatal(err)
	}
	if got != "MR [!7] [tfbuddy-class=tags] sha=1a2b3c4 actor=service-atlas-bot" {
		t.Errorf("Mode 2 message = %q, want fixed fields only and no title", got)
	}

	noClass := &TFCTrigger{cfg: &TFCTriggerOptions{MergeRequestIID: 7, Mode2: true}}
	if _, err := noClass.runMessage("Sync tags"); !errors.Is(err, utils.ErrPermanent) {
		t.Errorf("a Mode 2 run without a class must be refused, got %v", err)
	}
}
