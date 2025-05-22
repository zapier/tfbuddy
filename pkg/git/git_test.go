package git

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/go-git/go-git/v5"
)

func Test_getLastTag(t *testing.T) {
	_, testDir := createTestRepo(t, TestData{
		Commits: []TestCommit{
			TestCommit{
				Files: map[string]string{
					"file1": "this is file1",
				},
				Tag: "v0.1.0",
			},
			TestCommit{
				Files: map[string]string{
					"file2": "this is file2",
				},
				Tag: "v0.1.1",
			},
		},
	})
	type args struct {
		dir      string
		expected string
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "test1",
			args: args{
				dir:      testDir,
				expected: "v0.1.1",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tag := GetLastTag(tt.args.dir)
			assert.Equal(t, tag, tt.args.expected, "tags should be equal")

		})
	}
}

func Test_cleanTag(t *testing.T) {
	type args struct {
		tagRef string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "standard_tag",
			args: args{
				tagRef: "refs/tags/v0.1.1",
			},
			want: "v0.1.1",
		},
		{
			name: "tag_without_refs",
			args: args{
				tagRef: "v1.2.3",
			},
			want: "v1.2.3",
		},
		{
			name: "empty_tag",
			args: args{
				tagRef: "",
			},
			want: "",
		},
		{
			name: "tag_with_prefix_only",
			args: args{
				tagRef: "refs/tags/",
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CleanTagRefName(tt.args.tagRef); got != tt.want {
				t.Errorf("cleanTag() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetLastTag_NoTags(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}

	w, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}

	filename := filepath.Join(dir, "test.txt")
	err = os.WriteFile(filename, []byte("test"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	_, err = w.Add("test.txt")
	if err != nil {
		t.Fatal(err)
	}

	gitUser := &object.Signature{
		Name:  "Unit Test",
		Email: "unit@testdata.org",
		When:  time.Now(),
	}

	_, err = w.Commit("Initial Commit", &git.CommitOptions{
		Author: gitUser,
	})
	if err != nil {
		t.Fatal(err)
	}

	tag := GetLastTag(dir)
	assert.Empty(t, tag, "should return empty string when no tags exist")
}

func TestCleanTagReference_NilHandling(t *testing.T) {
	result := CleanTagReference(nil)
	assert.Empty(t, result, "should return empty string when tagRef is nil")
}

func TestFormatRef(t *testing.T) {
	_, testDir := createTestRepo(t, TestData{
		Commits: []TestCommit{
			TestCommit{
				Files: map[string]string{
					"file1": "this is file1",
				},
				Tag: "v1.0.0",
			},
		},
	})

	tagRef := GetLastTagRef(testDir)
	result := FormatRef(tagRef)

	assert.Contains(t, result, "refs/tags/v1.0.0")
	assert.Contains(t, result, "hash-reference")
}

type TestData struct {
	Commits []TestCommit
}

type TestCommit struct {
	Files map[string]string
	Tag   string
}

func createTestRepo(t *testing.T, td TestData) (repo *git.Repository, dir string) {
	dir = t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}

	w, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}

	for _, commit := range td.Commits {
		for name, contents := range commit.Files {
			filename := filepath.Join(dir, name)
			err = os.WriteFile(filename, []byte(contents), 0644)
			if err != nil {
				t.Fatal(err)
			}
			_, err = w.Add(name)
			if err != nil {
				t.Fatalf("Failed to add file %s: %v", name, err)
			}
		}

		gitUser := &object.Signature{
			Name:  "Unit Test",
			Email: "unit@testdata.org",
			When:  time.Now(),
		}

		_, err = w.Commit("Initial Commit",
			&git.CommitOptions{
				Author: gitUser,
			})
		if err != nil {
			t.Fatal(err)
		}
		h, err := repo.Head()
		if err != nil {
			t.Fatal(err)
		}

		_, err = repo.CreateTag(commit.Tag, h.Hash(), &git.CreateTagOptions{
			Message: commit.Tag,
			Tagger:  gitUser,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	return repo, dir
}
