package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionListAndRevoke(t *testing.T) {
	var deleted []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/auth/sessions", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"sessions": []map[string]any{
			{"id": "s1", "ip": "203.0.113.7", "last_seen_at": "2026-07-20T10:00:00Z", "user_agent": "Chrome", "current": true},
			{"id": "s2", "ip": "198.51.100.4", "last_seen_at": "2026-07-19T09:00:00Z", "user_agent": "curl/8", "current": false},
		}})
	})
	mux.HandleFunc("DELETE /v1/auth/sessions/s2", func(w http.ResponseWriter, _ *http.Request) {
		deleted = append(deleted, "s2")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /v1/auth/sessions", func(w http.ResponseWriter, _ *http.Request) {
		deleted = append(deleted, "others")
		_ = json.NewEncoder(w).Encode(map[string]int{"revoked": 3})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	// list marks the current session and shows both ids.
	out, err := runCLI(t, "", "session", "list", "--address", ts.URL, "--token", "janus_svc_x")
	if err != nil || !strings.Contains(out, "s1") || !strings.Contains(out, "s2") || !strings.Contains(out, "*") {
		t.Fatalf("session list: %q %v", out, err)
	}

	// revoke a single id.
	if _, err := runCLI(t, "", "session", "revoke", "s2", "--address", ts.URL, "--token", "janus_svc_x"); err != nil {
		t.Fatalf("revoke one: %v", err)
	}

	// revoke --others reports the count.
	out, err = runCLI(t, "", "session", "revoke", "--others", "--address", ts.URL, "--token", "janus_svc_x")
	if err != nil || !strings.Contains(out, "3") {
		t.Fatalf("revoke others: %q %v", out, err)
	}

	if len(deleted) != 2 || deleted[0] != "s2" || deleted[1] != "others" {
		t.Fatalf("unexpected deletes: %v", deleted)
	}

	// mutually-exclusive args are rejected without a call.
	if _, err := runCLI(t, "", "session", "revoke", "s2", "--others", "--address", ts.URL, "--token", "janus_svc_x"); err == nil {
		t.Fatal("expected error when both id and --others are given")
	}
	if _, err := runCLI(t, "", "session", "revoke", "--address", ts.URL, "--token", "janus_svc_x"); err == nil {
		t.Fatal("expected error when neither id nor --others is given")
	}
}
