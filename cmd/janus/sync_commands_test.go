package main

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestSyncCmdStructure(t *testing.T) {
	cmd := newSyncCmd()
	if cmd.Use != "sync" {
		t.Fatalf("Use = %q", cmd.Use)
	}
	want := map[string]bool{"create": false, "list": false, "get": false, "update": false, "delete": false, "sync": false}
	var create *cobra.Command
	for _, sub := range cmd.Commands() {
		name := sub.Name()
		if _, ok := want[name]; ok {
			want[name] = true
		}
		if name == "create" {
			create = sub
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing subcommand %q", name)
		}
	}

	if create == nil {
		t.Fatal("create subcommand not found")
	}
	wantFlags := []string{
		"config", "provider", "prune", "interval-seconds",
		"owner", "repo", "pat", "api-url", "ca-cert", "k8s-token", "namespace", "secret-name",
	}
	for _, name := range wantFlags {
		if create.Flags().Lookup(name) == nil {
			t.Errorf("create subcommand missing flag %q", name)
		}
	}
}
