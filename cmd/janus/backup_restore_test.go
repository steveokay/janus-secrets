package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupStreamsToStdoutWithAuth(t *testing.T) {
	t.Setenv("JANUS_CONFIG_DIR", t.TempDir())
	t.Setenv("JANUS_TOKEN", "")
	const dump = "{\"janus_backup\":1}\n{\"table\":\"projects\",\"row\":{}}\n"
	var gotAuth string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/sys/backup", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprint(w, dump)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	var out bytes.Buffer
	cmd := newBackupCmd()
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--address", ts.URL, "--token", "janus_svc_abc"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer janus_svc_abc" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if out.String() != dump {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestBackupWritesFile0600(t *testing.T) {
	t.Setenv("JANUS_CONFIG_DIR", t.TempDir())
	t.Setenv("JANUS_TOKEN", "")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/sys/backup", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "{\"janus_backup\":1}\n")
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	out := filepath.Join(t.TempDir(), "b.jsonl")
	cmd := newBackupCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--address", ts.URL, "--token", "tk", "--out", out})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil || !strings.Contains(string(b), "janus_backup") {
		t.Fatalf("file: %v %q", err, b)
	}
	// Permission check is meaningful on POSIX only; on Windows Go approximates.
	if fi, _ := os.Stat(out); fi != nil && fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Fatalf("perms too open: %v", fi.Mode())
	}
}

func TestRestoreSendsBodyAndPrintsUnsealHint(t *testing.T) {
	t.Setenv("JANUS_CONFIG_DIR", t.TempDir())
	var gotBody string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/sys/restore", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"restored":true,"sealed":true}`)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	var out bytes.Buffer
	cmd := newRestoreCmd()
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetIn(strings.NewReader("{\"janus_backup\":1}\n"))
	cmd.SetArgs([]string{"--address", ts.URL})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotBody, "janus_backup") {
		t.Fatalf("body = %q", gotBody)
	}
	if !strings.Contains(out.String(), "unseal") {
		t.Fatalf("output missing unseal hint: %q", out.String())
	}
}

// TestRestoreNotEmptyErrorIsActionable pins the operator-facing rendering of
// the most likely restore failure: the passthrough `message (code, HTTP n)`
// format must survive future rewriteAPIError changes.
func TestRestoreNotEmptyErrorIsActionable(t *testing.T) {
	t.Setenv("JANUS_CONFIG_DIR", t.TempDir())
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/sys/restore", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"error":{"code":"not_empty","message":"restore requires an empty instance (fresh database, before init)"}}`)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	cmd := newRestoreCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetIn(strings.NewReader("{\"janus_backup\":1}\n"))
	cmd.SetArgs([]string{"--address", ts.URL})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "empty instance") || !strings.Contains(err.Error(), "not_empty") {
		t.Fatalf("want actionable not_empty error, got %v", err)
	}
}
