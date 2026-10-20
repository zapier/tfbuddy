package tfc_api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/hashicorp/go-tfe"
)

// tagServer serves a workspace's tags split across pages, one tag per entry of
// pages. A page whose tag is "" answers with a server error instead.
func tagServer(t *testing.T, pages []string) *TFCClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/ping":
			w.WriteHeader(http.StatusNoContent)
			return
		case "/api/v2/workspaces/ws-test/relationships/tags":
		default:
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("name"); got != "tfbuddylock" {
			t.Errorf("tag query = %q, want %q", got, "tfbuddylock")
		}
		page := 1
		if p := r.URL.Query().Get("page[number]"); p != "" {
			fmt.Sscanf(p, "%d", &page)
		}
		if page < 1 || page > len(pages) || pages[page-1] == "" {
			http.Error(w, `{"errors":[{"status":"500","title":"boom"}]}`, http.StatusInternalServerError)
			return
		}
		next := "null"
		if page < len(pages) {
			next = fmt.Sprintf("%d", page+1)
		}
		w.Header().Set("Content-Type", "application/vnd.api+json")
		fmt.Fprintf(w, `{"data":[{"id":"tag-%d","type":"tags","attributes":{"name":%q}}],`+
			`"meta":{"pagination":{"current-page":%d,"prev-page":null,"next-page":%s,"total-pages":%d,"total-count":%d}}}`,
			page, pages[page-1], page, next, len(pages), len(pages))
	}))
	t.Cleanup(srv.Close)

	client, err := tfe.NewClient(&tfe.Config{Address: srv.URL, Token: "test-token"})
	if err != nil {
		t.Fatalf("tfe.NewClient: %v", err)
	}
	return &TFCClient{Client: client}
}

// TestGetTagsByQuery_ReadsEveryPage verifies that a lock tag on a later page is
// returned, since lock checks would otherwise miss it.
func TestGetTagsByQuery_ReadsEveryPage(t *testing.T) {
	client := tagServer(t, []string{"tfbuddylock-101", "tfbuddylock-selfserve-payments-api"})

	tags, err := client.GetTagsByQuery(context.Background(), "ws-test", "tfbuddylock")
	if err != nil {
		t.Fatalf("GetTagsByQuery() error = %v", err)
	}
	want := []string{"tfbuddylock-101", "tfbuddylock-selfserve-payments-api"}
	if !slices.Equal(tags, want) {
		t.Fatalf("GetTagsByQuery() = %v, want %v", tags, want)
	}
}

// TestGetTagsByQuery_LaterPageFailure verifies that a failure on a later page
// is an error, not a partial list that looks unlocked.
func TestGetTagsByQuery_LaterPageFailure(t *testing.T) {
	client := tagServer(t, []string{"tfbuddylock-101", ""})

	tags, err := client.GetTagsByQuery(context.Background(), "ws-test", "tfbuddylock")
	if err == nil {
		t.Fatalf("GetTagsByQuery() = %v, want an error for the failed page", tags)
	}
	if tags != nil {
		t.Fatalf("GetTagsByQuery() returned partial tags %v alongside the error", tags)
	}
}
