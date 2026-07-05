package main

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestSetupValidatesAndWritesBinding(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("JANUS_CONFIG_DIR", cfg)
	t.Setenv("JANUS_ADDR", "")
	t.Setenv("JANUS_TOKEN", "")

	ts := newResolveServer() // from resolve_test.go: acme/dev/dev -> c1
	defer ts.Close()

	work := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	cmd := newSetupCmd()
	cmd.SetArgs([]string{"--address", ts.URL, "--project", "acme", "--env", "dev", "--config", "dev"})
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	bf, err := readBinding(work)
	if err != nil || bf == nil {
		t.Fatalf("binding not written: %v", err)
	}
	if bf.Project != "acme" || bf.Environment != "dev" || bf.Config != "dev" {
		t.Fatalf("binding: %+v", bf)
	}
}

func TestSetupRejectsUnknownConfig(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("JANUS_CONFIG_DIR", cfg)
	ts := newResolveServer()
	defer ts.Close()
	work := t.TempDir()
	cwd, _ := os.Getwd()
	_ = os.Chdir(work)
	defer os.Chdir(cwd)

	cmd := newSetupCmd()
	cmd.SetArgs([]string{"--address", ts.URL, "--project", "acme", "--env", "dev", "--config", "nope"})
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for unknown config")
	}
	if _, err := os.Stat(bindingPath(work)); !os.IsNotExist(err) {
		t.Fatal("binding should not be written on validation failure")
	}
}

var _ = http.MethodGet // keep net/http import if unused elsewhere
