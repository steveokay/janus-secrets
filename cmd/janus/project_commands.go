package main

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

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

	var pSlug, pName string
	var pJSON, pYes bool

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

	create := &cobra.Command{
		Use:   "create",
		Short: "Create a project",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out struct{ ID, Slug, Name string }
			if err := c.call("POST", "/v1/projects", map[string]string{"slug": pSlug, "name": pName}, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created project %s (%s)\n", out.Slug, out.ID)
			return nil
		},
	}
	create.Flags().StringVar(&pSlug, "slug", "", "project slug (required)")
	create.Flags().StringVar(&pName, "name", "", "human-readable name")
	_ = create.MarkFlagRequired("slug")

	list := &cobra.Command{
		Use:   "list",
		Short: "List projects",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var resp struct {
				Projects []struct{ ID, Slug, Name string } `json:"projects"`
			}
			if err := c.call("GET", "/v1/projects", nil, &resp); err != nil {
				return err
			}
			if pJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(resp.Projects)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "SLUG\tNAME\tID")
			for _, p := range resp.Projects {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", p.Slug, p.Name, p.ID)
			}
			return tw.Flush()
		},
	}
	list.Flags().BoolVar(&pJSON, "json", false, "output JSON")

	del := &cobra.Command{
		Use:   "delete <slug>",
		Short: "Soft-delete a project (restore-able)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			pid, err := c.resolveProjectID(args[0])
			if err != nil {
				return err
			}
			if !pYes && isTerminalCmd(cmd) {
				ok, err := promptLine(cmd, fmt.Sprintf("Delete project %q? [y/N]: ", args[0]))
				if err != nil {
					return err
				}
				if ok != "y" && ok != "Y" {
					return nil
				}
			}
			if err := c.call("DELETE", "/v1/projects/"+pid, nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "deleted project %s\n", args[0])
			return nil
		},
	}
	del.Flags().BoolVar(&pYes, "yes", false, "skip the confirmation prompt")

	restore := &cobra.Command{
		Use:   "restore <slug>",
		Short: "Restore a soft-deleted project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			pid, err := c.resolveDeletedProjectID(args[0])
			if err != nil {
				return err
			}
			if err := c.call("POST", "/v1/projects/"+pid+"/restore", nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "restored project %s\n", args[0])
			return nil
		},
	}

	cmd.AddCommand(rotate, rewrap, status, create, list, del, restore)
	return cmd
}
