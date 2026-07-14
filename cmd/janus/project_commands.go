package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newProjectCmd wires the owner-only project KEK lifecycle commands:
// rotate-kek (mint a new project KEK version), rewrap (lazily re-wrap DEKs
// onto the current version and retire drained versions), and kek-status
// (report the current version plus any versions still holding DEKs).
func newProjectCmd() *cobra.Command {
	var address, token string
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage projects (KEK rotation/rewrap/status)",
	}
	cmd.PersistentFlags().StringVar(&address, "address", "", "server address (default: stored/env)")
	cmd.PersistentFlags().StringVar(&token, "token", "", "service token (default: stored/env)")

	rotate := &cobra.Command{
		Use:   "rotate-kek <project-id>",
		Short: "Rotate a project's key-encryption key to a new version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out struct {
				KEKVersion int `json:"kek_version"`
			}
			if err := c.call("POST", "/v1/projects/"+args[0]+"/kek/rotate", nil, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "rotated project %s to KEK version %d\n", args[0], out.KEKVersion)
			return nil
		},
	}

	rewrap := &cobra.Command{
		Use:   "rewrap <project-id>",
		Short: "Re-wrap DEKs onto the current KEK version and retire drained versions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out struct {
				Rewrapped       int   `json:"rewrapped"`
				RetiredVersions []int `json:"retired_versions"`
				Remaining       int   `json:"remaining"`
			}
			if err := c.call("POST", "/v1/projects/"+args[0]+"/kek/rewrap", nil, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "rewrapped %d DEKs; retired versions %v (remaining %d)\n",
				out.Rewrapped, out.RetiredVersions, out.Remaining)
			return nil
		},
	}

	status := &cobra.Command{
		Use:   "kek-status <project-id>",
		Short: "Show the current KEK version and versions still holding DEKs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out struct {
				CurrentVersion int `json:"current_version"`
				Pending        []struct {
					Version  int `json:"version"`
					DEKCount int `json:"dek_count"`
				} `json:"pending"`
			}
			if err := c.call("GET", "/v1/projects/"+args[0]+"/kek", nil, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "current version %d; pending %v\n", out.CurrentVersion, out.Pending)
			return nil
		},
	}

	cmd.AddCommand(rotate, rewrap, status)
	return cmd
}
