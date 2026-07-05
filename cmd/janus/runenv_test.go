package main

import (
	"sort"
	"testing"
)

func TestBuildChildEnvSecretsWin(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "DB_URL=old", "HOME=/home/me"}
	secrets := map[string]string{"DB_URL": "new", "API_KEY": "k"}
	got := buildChildEnv(parent, secrets, false)

	m := envToMap(got)
	if m["DB_URL"] != "new" {
		t.Fatalf("secret should override parent: DB_URL=%q", m["DB_URL"])
	}
	if m["PATH"] != "/usr/bin" || m["HOME"] != "/home/me" {
		t.Fatalf("non-secret vars must pass through: %+v", m)
	}
	if m["API_KEY"] != "k" {
		t.Fatalf("new secret missing: %+v", m)
	}
}

func TestBuildChildEnvPreserveEnv(t *testing.T) {
	parent := []string{"DB_URL=old"}
	secrets := map[string]string{"DB_URL": "new", "API_KEY": "k"}
	got := buildChildEnv(parent, secrets, true)
	m := envToMap(got)
	if m["DB_URL"] != "old" {
		t.Fatalf("--preserve-env: parent should win, got %q", m["DB_URL"])
	}
	if m["API_KEY"] != "k" {
		t.Fatalf("--preserve-env should still add new keys: %+v", m)
	}
}

func envToMap(env []string) map[string]string {
	m := map[string]string{}
	for _, e := range env {
		for i := 0; i < len(e); i++ {
			if e[i] == '=' {
				m[e[:i]] = e[i+1:]
				break
			}
		}
	}
	return m
}

func TestBuildChildEnvDeterministicNoDup(t *testing.T) {
	parent := []string{"A=1"}
	secrets := map[string]string{"A": "2"}
	got := buildChildEnv(parent, secrets, false)
	sort.Strings(got)
	count := 0
	for _, e := range got {
		if len(e) >= 2 && e[:2] == "A=" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("A should appear once, got %d (%v)", count, got)
	}
}
