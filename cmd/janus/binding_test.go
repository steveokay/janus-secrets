package main

import (
	"path/filepath"
	"testing"
)

func TestBindingRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := &bindingFile{Project: "acme", Environment: "dev", Config: "dev"}
	if err := writeBinding(dir, in); err != nil {
		t.Fatal(err)
	}
	got, err := readBinding(dir)
	if err != nil {
		t.Fatal(err)
	}
	if *got != *in {
		t.Fatalf("roundtrip: %+v", got)
	}
	if filepath.Base(bindingPath(dir)) != ".janus.yaml" {
		t.Fatalf("binding file name = %q", bindingPath(dir))
	}
}

func TestResolveBindingPrecedence(t *testing.T) {
	dir := t.TempDir()
	_ = writeBinding(dir, &bindingFile{Project: "fp", Environment: "fe", Config: "fc"})
	t.Setenv("JANUS_PROJECT", "")
	t.Setenv("JANUS_ENV", "")
	t.Setenv("JANUS_CONFIG", "")

	// File only.
	p, e, c, err := resolveBinding(dir, "", "", "")
	if err != nil || p != "fp" || e != "fe" || c != "fc" {
		t.Fatalf("file: %q %q %q %v", p, e, c, err)
	}
	// Env overrides file for the fields it sets.
	t.Setenv("JANUS_CONFIG", "envc")
	p, e, c, _ = resolveBinding(dir, "", "", "")
	if c != "envc" || p != "fp" {
		t.Fatalf("env override: %q %q %q", p, e, c)
	}
	// Flag beats env and file.
	p, e, c, _ = resolveBinding(dir, "flagp", "", "flagc")
	if p != "flagp" || c != "flagc" || e != "fe" {
		t.Fatalf("flag override: %q %q %q", p, e, c)
	}
}

func TestResolveBindingMissing(t *testing.T) {
	dir := t.TempDir() // no file
	t.Setenv("JANUS_PROJECT", "")
	t.Setenv("JANUS_ENV", "")
	t.Setenv("JANUS_CONFIG", "")
	if _, _, _, err := resolveBinding(dir, "", "", ""); err == nil {
		t.Fatal("expected error when nothing configured")
	}
}
