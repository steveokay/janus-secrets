package main

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestDynamicCmdStructure(t *testing.T) {
	cmd := newDynamicCmd()
	if cmd.Use != "dynamic" {
		t.Fatalf("Use = %q", cmd.Use)
	}

	names := map[string]bool{}
	var roles *cobra.Command
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
		if c.Name() == "roles" {
			roles = c
		}
	}
	for _, want := range []string{"roles", "creds", "renew", "revoke", "leases"} {
		if !names[want] {
			t.Errorf("missing subcommand %q", want)
		}
	}

	if roles == nil {
		t.Fatal("roles subcommand not found")
	}
	sub := map[string]bool{}
	var rolesCreate *cobra.Command
	for _, c := range roles.Commands() {
		sub[c.Name()] = true
		if c.Name() == "create" {
			rolesCreate = c
		}
	}
	for _, want := range []string{"create", "list", "get", "delete"} {
		if !sub[want] {
			t.Errorf("roles missing subcommand %q", want)
		}
	}

	if rolesCreate == nil {
		t.Fatal("roles create subcommand not found")
	}
	wantFlags := []string{
		"config", "name", "default-ttl-seconds", "max-ttl-seconds",
		"admin-dsn", "creation", "revocation", "renew",
	}
	for _, name := range wantFlags {
		if rolesCreate.Flags().Lookup(name) == nil {
			t.Errorf("roles create subcommand missing flag %q", name)
		}
	}

	// leases requires --role flag
	var leases *cobra.Command
	for _, c := range cmd.Commands() {
		if c.Name() == "leases" {
			leases = c
		}
	}
	if leases == nil {
		t.Fatal("leases subcommand not found")
	}
	if leases.Flags().Lookup("role") == nil {
		t.Error("leases subcommand missing flag \"role\"")
	}
}
