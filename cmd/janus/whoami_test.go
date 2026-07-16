package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWhoami(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/auth/me", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"kind": "user", "id": "u1", "name": "root@corp.io"})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	out, err := runCLI(t, "", "whoami", "--address", ts.URL, "--token", "janus_svc_test")
	if err != nil || !strings.Contains(out, "root@corp.io") || !strings.Contains(out, "user") {
		t.Fatalf("whoami: %q %v", out, err)
	}
}

func TestCompletionGenerates(t *testing.T) {
	out, err := runCLI(t, "", "completion", "bash")
	if err != nil || !strings.Contains(out, "janus") {
		t.Fatalf("completion bash: %v", err)
	}
}
