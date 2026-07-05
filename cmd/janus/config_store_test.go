package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAuthStateRoundTripAndMode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir) // os.UserConfigDir honors this on linux/mac
	t.Setenv("JANUS_ADDR", "")
	t.Setenv("JANUS_TOKEN", "")

	in := &authState{Address: "http://h:8200", Session: "sess-abc", Email: "me@corp.io"}
	if err := saveAuth(in); err != nil {
		t.Fatal(err)
	}
	got, err := loadAuth()
	if err != nil {
		t.Fatal(err)
	}
	if got.Address != in.Address || got.Session != in.Session || got.Email != in.Email {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	if runtime.GOOS != "windows" {
		p, _ := authPath()
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("auth.json mode = %v, want 0600", fi.Mode().Perm())
		}
	}
}

func TestResolveAddressPrecedence(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("JANUS_ADDR", "")
	t.Setenv("JANUS_TOKEN", "")
	_ = saveAuth(&authState{Address: "http://file:8200"})

	if got := resolveAddress("http://flag:8200"); got != "http://flag:8200" {
		t.Fatalf("flag should win, got %q", got)
	}
	t.Setenv("JANUS_ADDR", "http://env:8200")
	if got := resolveAddress(""); got != "http://env:8200" {
		t.Fatalf("env should win over file, got %q", got)
	}
	t.Setenv("JANUS_ADDR", "")
	if got := resolveAddress(""); got != "http://file:8200" {
		t.Fatalf("file should win over default, got %q", got)
	}
	_ = os.Remove(filepath.Join(dir, "janus", "auth.json"))
	if got := resolveAddress(""); got != "http://127.0.0.1:8200" {
		t.Fatalf("default, got %q", got)
	}
}

func TestResolveCredentialPrecedence(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("JANUS_TOKEN", "")
	_ = saveAuth(&authState{Session: "sess-file"})

	if c, _ := resolveCredential("janus_svc_flag"); c.Bearer != "janus_svc_flag" || c.Cookie != "" {
		t.Fatalf("flag token should win: %+v", c)
	}
	t.Setenv("JANUS_TOKEN", "janus_svc_env")
	if c, _ := resolveCredential(""); c.Bearer != "janus_svc_env" {
		t.Fatalf("env token should win over session: %+v", c)
	}
	t.Setenv("JANUS_TOKEN", "")
	if c, _ := resolveCredential(""); c.Cookie != "sess-file" || c.Bearer != "" {
		t.Fatalf("session should be used when no token: %+v", c)
	}
}
