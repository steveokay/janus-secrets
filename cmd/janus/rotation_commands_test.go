package main

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestRotationCmdStructure(t *testing.T) {
	cmd := newRotationCmd()
	if cmd.Use != "rotation" {
		t.Fatalf("Use = %q", cmd.Use)
	}
	want := map[string]bool{"create": false, "list": false, "get": false, "update": false, "delete": false, "rotate": false}
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
		"config", "key", "type", "interval-seconds", "admin-dsn", "role",
		"password-len", "url", "hmac-key", "notify-url", "notify-hmac-key",
	}
	for _, name := range wantFlags {
		if create.Flags().Lookup(name) == nil {
			t.Errorf("create subcommand missing flag %q", name)
		}
	}
}
