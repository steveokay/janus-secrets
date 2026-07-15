package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubMasterKey scripts the owner-only master-key API for CLI tests and records
// the paths it received on the wire. The rekey ceremony completes on the third
// submit.
func stubMasterKey(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	var submits int
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/sys/master-key", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"unseal_type":        "awskms",
			"master_key_version": 1,
			"rotated_at":         nil,
			"rekey_in_progress":  false,
			"submitted":          0,
			"required":           3,
		})
	})
	mux.HandleFunc("POST /v1/sys/master-key/rotate", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"master_key_version": 2})
	})
	mux.HandleFunc("POST /v1/sys/master-key/rekey/init", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"nonce": "n1", "required": 3, "submitted": 0,
		})
	})
	mux.HandleFunc("POST /v1/sys/master-key/rekey/submit", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		submits++
		if submits < 3 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"complete": false, "submitted": submits, "required": 3,
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"complete":           true,
			"master_key_version": 2,
			"new_shares":         []string{"d1", "d2", "d3"},
		})
	})
	mux.HandleFunc("DELETE /v1/sys/master-key/rekey", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &paths
}

func TestMasterKeyCmdStructure(t *testing.T) {
	cmd := newMasterKeyCmd()
	if cmd.Use != "master-key" {
		t.Fatalf("Use = %q", cmd.Use)
	}
	want := map[string]bool{"status": false, "rotate": false, "rekey": false}
	for _, sub := range cmd.Commands() {
		if _, ok := want[sub.Name()]; ok {
			want[sub.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing subcommand %q", name)
		}
	}
}

func TestMasterKeyStatus(t *testing.T) {
	ts, paths := stubMasterKey(t)
	out, err := runCLI(t, "", "master-key", "status", "--address", ts.URL, "--token", "janus_svc_test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "awskms") {
		t.Fatalf("output missing unseal type awskms: %q", out)
	}
	if !strings.Contains(out, "1") {
		t.Fatalf("output missing master key version 1: %q", out)
	}
	if len(*paths) != 1 || (*paths)[0] != "/v1/sys/master-key" {
		t.Fatalf("wire paths = %v, want [/v1/sys/master-key]", *paths)
	}
}

func TestMasterKeyRotate(t *testing.T) {
	ts, paths := stubMasterKey(t)
	out, err := runCLI(t, "", "master-key", "rotate", "--address", ts.URL, "--token", "janus_svc_test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2") {
		t.Fatalf("output missing new version 2: %q", out)
	}
	if len(*paths) != 1 || (*paths)[0] != "/v1/sys/master-key/rotate" {
		t.Fatalf("wire paths = %v, want [/v1/sys/master-key/rotate]", *paths)
	}
}

func TestMasterKeyRekey(t *testing.T) {
	ts, paths := stubMasterKey(t)
	out, err := runCLI(t, "",
		"master-key", "rekey",
		"--share", "AA", "--share", "BB", "--share", "CC",
		"--address", ts.URL, "--token", "janus_svc_test")
	if err != nil {
		t.Fatal(err)
	}
	// init once + submit three times.
	want := []string{
		"/v1/sys/master-key/rekey/init",
		"/v1/sys/master-key/rekey/submit",
		"/v1/sys/master-key/rekey/submit",
		"/v1/sys/master-key/rekey/submit",
	}
	if len(*paths) != len(want) {
		t.Fatalf("wire paths = %v, want %v", *paths, want)
	}
	for i := range want {
		if (*paths)[i] != want[i] {
			t.Fatalf("wire path[%d] = %q, want %q", i, (*paths)[i], want[i])
		}
	}
	for _, sh := range []string{"d1", "d2", "d3"} {
		if !strings.Contains(out, sh) {
			t.Fatalf("output missing new share %q: %q", sh, out)
		}
	}
}
