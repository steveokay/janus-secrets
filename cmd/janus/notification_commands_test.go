package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNotificationsCLI(t *testing.T) {
	var created map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/notifications/channels", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &created)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "ch1", "name": "alerts", "type": "webhook"})
	})
	mux.HandleFunc("GET /v1/notifications/channels", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"channels": []map[string]any{
			{"id": "ch1", "name": "alerts", "type": "webhook", "enabled": true, "events": []string{"access.denied"}},
		}})
	})
	var tested bool
	mux.HandleFunc("POST /v1/notifications/channels/ch1/test", func(w http.ResponseWriter, _ *http.Request) {
		tested = true
		_ = json.NewEncoder(w).Encode(map[string]any{"delivered": true})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	// create parses --events CSV and posts the right body.
	out, err := runCLI(t, "", "notifications", "create", "--name", "alerts", "--type", "webhook",
		"--url", "https://hooks.example/x", "--events", "access.denied,sync.failed",
		"--address", ts.URL, "--token", "janus_svc_x")
	if err != nil || !strings.Contains(out, "ch1") {
		t.Fatalf("create: %q %v", out, err)
	}
	evs, _ := created["events"].([]any)
	if len(evs) != 2 {
		t.Fatalf("events not parsed from CSV: %+v", created["events"])
	}
	if created["url"] != "https://hooks.example/x" {
		t.Fatalf("url not sent: %+v", created)
	}

	// list renders the channel.
	out, err = runCLI(t, "", "notifications", "list", "--address", ts.URL, "--token", "janus_svc_x")
	if err != nil || !strings.Contains(out, "alerts") || !strings.Contains(out, "access.denied") {
		t.Fatalf("list: %q %v", out, err)
	}

	// test hits the test endpoint.
	if _, err := runCLI(t, "", "notifications", "test", "ch1", "--address", ts.URL, "--token", "janus_svc_x"); err != nil {
		t.Fatalf("test: %v", err)
	}
	if !tested {
		t.Fatal("test endpoint not called")
	}
}
