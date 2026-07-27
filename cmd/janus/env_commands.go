package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// newEnvCmd wires the environment lifecycle commands (create/list/delete/restore/protect)
// scoped to a project, resolved via --project/JANUS_PROJECT/.janus.yaml.
func newEnvCmd() *cobra.Command {
	var address, token, project string
	var slug, name string
	var asJSON, yes, protectOff bool

	cmd := &cobra.Command{Use: "env", Aliases: []string{"environment"}, Short: "Manage environments"}
	cmd.PersistentFlags().StringVar(&address, "address", "", "server address")
	cmd.PersistentFlags().StringVar(&token, "token", "", "service token")
	cmd.PersistentFlags().StringVar(&project, "project", "", "project slug (overrides .janus.yaml)")

	resolveProject := func() (*apiClient, string, string, error) {
		c, err := newAPIClient(address, token)
		if err != nil {
			return nil, "", "", err
		}
		dir, err := os.Getwd()
		if err != nil {
			return nil, "", "", err
		}
		p, _, _, err := bindingValues(dir, project, "", "")
		if err != nil {
			return nil, "", "", err
		}
		if p == "" {
			return nil, "", "", fmt.Errorf("no project — pass --project or run `janus setup`")
		}
		pid, err := c.resolveProjectID(p)
		if err != nil {
			return nil, "", "", err
		}
		return c, pid, p, nil
	}

	create := &cobra.Command{
		Use: "create", Short: "Create an environment",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, pid, _, err := resolveProject()
			if err != nil {
				return err
			}
			var out struct{ ID, Slug, Name string }
			if err := c.call("POST", "/v1/projects/"+pid+"/environments", map[string]string{"slug": slug, "name": name}, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created environment %s (%s)\n", out.Slug, out.ID)
			return nil
		},
	}
	create.Flags().StringVar(&slug, "slug", "", "environment slug (required)")
	create.Flags().StringVar(&name, "name", "", "human-readable name")
	_ = create.MarkFlagRequired("slug")

	list := &cobra.Command{
		Use: "list", Short: "List environments",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, pid, _, err := resolveProject()
			if err != nil {
				return err
			}
			var resp struct {
				Environments []struct{ ID, Slug, Name string } `json:"environments"`
			}
			if err := c.call("GET", "/v1/projects/"+pid+"/environments", nil, &resp); err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(resp.Environments)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "SLUG\tNAME\tID")
			for _, e := range resp.Environments {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", e.Slug, e.Name, e.ID)
			}
			return tw.Flush()
		},
	}
	list.Flags().BoolVar(&asJSON, "json", false, "output JSON")

	del := &cobra.Command{
		Use: "delete <slug>", Short: "Soft-delete an environment", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			dir, _ := os.Getwd()
			p, _, _, _ := bindingValues(dir, project, "", "")
			pid, eid, err := c.resolveEnvID(p, args[0])
			if err != nil {
				return err
			}
			if !yes && isTerminalCmd(cmd) {
				ok, err := promptLine(cmd, fmt.Sprintf("Delete environment %q? [y/N]: ", args[0]))
				if err != nil {
					return err
				}
				if ok != "y" && ok != "Y" {
					return nil
				}
			}
			if err := c.call("DELETE", "/v1/projects/"+pid+"/environments/"+eid, nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "deleted environment %s\n", args[0])
			return nil
		},
	}
	del.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")

	restore := &cobra.Command{
		Use: "restore <slug>", Short: "Restore a soft-deleted environment", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			dir, _ := os.Getwd()
			p, _, _, _ := bindingValues(dir, project, "", "")
			pid, eid, err := c.resolveDeletedEnvID(p, args[0])
			if err != nil {
				return err
			}
			if err := c.call("POST", "/v1/projects/"+pid+"/environments/"+eid+"/restore", nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "restored environment %s\n", args[0])
			return nil
		},
	}

	protect := &cobra.Command{
		Use: "protect <slug>", Short: "Require four-eyes approval for every config in an environment",
		Args: cobra.ExactArgs(1),
		Long: "Direct saves to any config in the environment stop committing: each becomes an\n" +
			"edit request a different reviewer must approve. It covers configs created\n" +
			"LATER too, which is the point -- per-config protection defaults to off, so a\n" +
			"new production config would otherwise start unprotected.\n\n" +
			"Protection is a union: a config can add protection its environment does not\n" +
			"require, but can never remove protection the environment does.\n\n" +
			"Use --off to stop requiring it.",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			dir, _ := os.Getwd()
			p, _, _, err := bindingValues(dir, project, "", "")
			if err != nil {
				return err
			}
			if p == "" {
				return fmt.Errorf("no project -- pass --project or run `janus setup`")
			}
			pid, eid, err := c.resolveEnvID(p, args[0])
			if err != nil {
				return err
			}
			enabled := !protectOff
			if err := c.call("PUT",
				"/v1/projects/"+url.PathEscape(pid)+"/environments/"+url.PathEscape(eid)+"/require-approval",
				map[string]any{"enabled": enabled}, nil); err != nil {
				return err
			}
			if enabled {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s now requires approval for every config\n", args[0])
			} else {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s no longer requires approval (individually protected configs are unaffected)\n", args[0])
			}
			return nil
		},
	}
	protect.Flags().BoolVar(&protectOff, "off", false, "stop requiring approval")

	cmd.AddCommand(create, list, del, restore, protect)
	return cmd
}
