package tfc_api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/go-tfe"
)

func TestDiscardRunPostsTheDiscardAction(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/ping":
			w.WriteHeader(http.StatusNoContent)
		case "/api/v2/runs/run-123/actions/discard":
			if r.Method != http.MethodPost {
				t.Errorf("method = %s, want POST", r.Method)
			}
			json.NewDecoder(r.Body).Decode(&got)
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	client, err := tfe.NewClient(&tfe.Config{Address: srv.URL, Token: "test-token"})
	if err != nil {
		t.Fatal(err)
	}

	if err := (&TFCClient{Client: client}).DiscardRun(context.Background(), "run-123", "Auto Mode never overrides a policy"); err != nil {
		t.Fatal(err)
	}
	if got["comment"] != "Auto Mode never overrides a policy" {
		t.Errorf("discard body = %v, want the comment", got)
	}
}
