package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gogithub "github.com/google/go-github/v69/github"
	"github.com/zapier/tfbuddy/internal/config"
)

type fakeTreeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
}

type fakeContentEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

// newListDirsTestClient serves contents listings and trees for zapier/tfbuddy.
func newListDirsTestClient(t *testing.T, contents map[string][]fakeContentEntry, trees map[string][]fakeTreeEntry, truncated bool) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/zapier/tfbuddy/contents/", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("ref"); got != "feature" {
			t.Errorf("expected ref feature, got %q", got)
		}
		entries, ok := contents[strings.TrimPrefix(r.URL.Path, "/repos/zapier/tfbuddy/contents/")]
		if !ok {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(entries)
	})
	mux.HandleFunc("/repos/zapier/tfbuddy/git/trees/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("recursive") != "1" {
			t.Errorf("expected a recursive tree request, got %s", r.URL.RawQuery)
		}
		entries, ok := trees[strings.TrimPrefix(r.URL.Path, "/repos/zapier/tfbuddy/git/trees/")]
		if !ok {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": "x", "tree": entries, "truncated": truncated})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	ghClient := gogithub.NewClient(nil)
	ghClient.BaseURL, _ = ghClient.BaseURL.Parse(server.URL + "/")
	return &Client{client: ghClient, ctx: context.Background(), token: "test-token", cfg: config.C}
}

func TestListRepoDirectoriesResolvesSubdirectoryTree(t *testing.T) {
	client := newListDirsTestClient(t,
		map[string][]fakeContentEntry{"services/api": {
			{Path: "services/api/README.md", Type: "file", SHA: "readme"},
			{Path: "services/api/staging", Type: "dir", SHA: "staging-sha"},
		}},
		map[string][]fakeTreeEntry{"staging-sha": {
			{Path: "main-us-west-2", Type: "tree"},
			{Path: "main-us-west-2/main.tf", Type: "blob"},
			{Path: "files", Type: "tree"},
		}}, false)

	dirs, err := client.ListRepoDirectories(context.Background(), "zapier/tfbuddy", "services/api/staging", "feature")
	if err != nil {
		t.Fatal(err)
	}
	want := "services/api/staging/main-us-west-2,services/api/staging/files"
	if got := strings.Join(dirs, ","); got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestListRepoDirectoriesRepoRootUsesRef(t *testing.T) {
	client := newListDirsTestClient(t, nil,
		map[string][]fakeTreeEntry{"feature": {{Path: "services", Type: "tree"}, {Path: "README.md", Type: "blob"}}}, false)

	dirs, err := client.ListRepoDirectories(context.Background(), "zapier/tfbuddy", "", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(dirs, ",") != "services" {
		t.Fatalf("expected [services], got %v", dirs)
	}
}

func TestListRepoDirectoriesMissingDirIsEmpty(t *testing.T) {
	client := newListDirsTestClient(t,
		map[string][]fakeContentEntry{"services/api": {{Path: "services/api/production", Type: "dir", SHA: "p"}}},
		nil, false)

	for _, dir := range []string{"services/api/staging", "services/gone/staging"} {
		dirs, err := client.ListRepoDirectories(context.Background(), "zapier/tfbuddy", dir, "feature")
		if err != nil || len(dirs) != 0 {
			t.Fatalf("%s: expected no directories and no error, got %v, %v", dir, dirs, err)
		}
	}
}

func TestListRepoDirectoriesFailsWhenParentListingIsCapped(t *testing.T) {
	// A parent at the contents API's limit may have left the directory out,
	// so its absence from the listing proves nothing.
	var full []fakeContentEntry
	for i := 0; i < contentsListingLimit; i++ {
		full = append(full, fakeContentEntry{Path: fmt.Sprintf("services/svc-%04d", i), Type: "dir", SHA: "s"})
	}
	client := newListDirsTestClient(t, map[string][]fakeContentEntry{"services": full}, nil, false)

	_, err := client.ListRepoDirectories(context.Background(), "zapier/tfbuddy", "services/zzz", "feature")
	if err == nil || !strings.Contains(err.Error(), "too many to tell") {
		t.Fatalf("expected a capped-listing error, got %v", err)
	}
}

func TestListRepoDirectoriesFailsOnTruncatedTree(t *testing.T) {
	client := newListDirsTestClient(t, nil,
		map[string][]fakeTreeEntry{"feature": {{Path: "services", Type: "tree"}}}, true)

	_, err := client.ListRepoDirectories(context.Background(), "zapier/tfbuddy", "", "feature")
	if err == nil || !strings.Contains(err.Error(), "too large to list") {
		t.Fatalf("expected a truncated-tree error, got %v", err)
	}
}
