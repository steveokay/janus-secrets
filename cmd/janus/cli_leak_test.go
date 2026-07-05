package main

import (
	"os"
	"strings"
	"testing"
)

// TestCLINoSecretLeakInDiagnostics asserts secret values never appear on stderr
// or in error strings across get/list/failed paths — only on explicit stdout data.
func TestCLINoSecretLeakInDiagnostics(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("JANUS_CONFIG_DIR", cfgDir)
	t.Setenv("JANUS_ADDR", "")
	t.Setenv("JANUS_TOKEN", "")
	base, session := bootServer(t)
	proj, env, cfg := seedConfig(t, base, session)
	_ = saveAuth(&authState{Address: base, Session: session, Email: "root@corp.io"})

	work := t.TempDir()
	cwd, _ := os.Getwd()
	_ = os.Chdir(work)
	defer os.Chdir(cwd)
	_, _, _ = runCmd(t, newSetupCmd(), "--project", proj, "--env", env, "--config", cfg)
	_, _, _ = runCmd(t, newSecretsCmd(), "set", "SEKRIT=topsecretvalue")

	const secret = "topsecretvalue"

	// list → stderr must not contain the value (stdout is masked metadata).
	listOut, listErr, _ := runCmd(t, newSecretsCmd(), "list")
	if strings.Contains(listErr, secret) || strings.Contains(listOut, secret) {
		t.Fatalf("list leaked the value")
	}
	// get → value is allowed on stdout, never on stderr.
	getOut, getErr, _ := runCmd(t, newSecretsCmd(), "get", "SEKRIT")
	if !strings.Contains(getOut, secret) {
		t.Fatalf("get should print the value to stdout")
	}
	if strings.Contains(getErr, secret) {
		t.Fatalf("get leaked the value to stderr")
	}
	// A forced error path (unknown key) must not echo any value.
	_, errOut, err := runCmd(t, newSecretsCmd(), "get", "NOPE")
	if err != nil && strings.Contains(err.Error(), secret) {
		t.Fatalf("error string leaked a value")
	}
	if strings.Contains(errOut, secret) {
		t.Fatalf("stderr leaked a value on the error path")
	}
}
