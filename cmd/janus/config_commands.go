package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// newConfigCmd wires the config lifecycle commands (create/list/delete/restore)
// scoped to a project+environment, resolved via --project/--env/.janus.yaml.
func newConfigCmd() *cobra.Command {
	var address, token, project, env string
	var name, inheritsFrom string
	var asJSON, yes bool

	cmd := &cobra.Command{Use: "config", Short: "Manage configs"}
	cmd.PersistentFlags().StringVar(&address, "address", "", "server address")
	cmd.PersistentFlags().StringVar(&token, "token", "", "service token")
	cmd.PersistentFlags().StringVar(&project, "project", "", "project slug (overrides .janus.yaml)")
	cmd.PersistentFlags().StringVar(&env, "env", "", "environment slug (overrides .janus.yaml)")

	resolveEnv := func() (*apiClient, string, string, error) {
		c, err := newAPIClient(address, token)
		if err != nil {
			return nil, "", "", err
		}
		dir, err := os.Getwd()
		if err != nil {
			return nil, "", "", err
		}
		p, e, _, err := bindingValues(dir, project, env, "")
		if err != nil {
			return nil, "", "", err
		}
		if p == "" || e == "" {
			return nil, "", "", fmt.Errorf("no project/environment — pass --project/--env or run `janus setup`")
		}
		pid, eid, err := c.resolveEnvID(p, e)
		if err != nil {
			return nil, "", "", err
		}
		return c, pid, eid, nil
	}
	// resolveCID resolves a live (non-deleted) config name to its id; deleted
	// (true) switches to the trash-based resolver so `restore` can find a
	// soft-deleted config that the live configs list no longer returns.
	resolveCID := func(configName string, deleted bool) (*apiClient, string, error) {
		c, err := newAPIClient(address, token)
		if err != nil {
			return nil, "", err
		}
		dir, _ := os.Getwd()
		p, e, _, err := bindingValues(dir, project, env, "")
		if err != nil {
			return nil, "", err
		}
		var cid string
		if deleted {
			cid, err = c.resolveDeletedConfigID(p, e, configName)
		} else {
			cid, err = c.resolveConfigID(p, e, configName)
		}
		if err != nil {
			return nil, "", err
		}
		return c, cid, nil
	}

	create := &cobra.Command{
		Use: "create", Short: "Create a config",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, pid, eid, err := resolveEnv()
			if err != nil {
				return err
			}
			body := map[string]any{"name": name}
			if inheritsFrom != "" {
				body["inherits_from"] = inheritsFrom
			}
			var out struct {
				ID, Name string
			}
			if err := c.call("POST", "/v1/projects/"+pid+"/environments/"+eid+"/configs", body, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created config %s (%s)\n", out.Name, out.ID)
			return nil
		},
	}
	create.Flags().StringVar(&name, "name", "", "config name (required)")
	create.Flags().StringVar(&inheritsFrom, "inherits-from", "", "base config name in the same environment")
	_ = create.MarkFlagRequired("name")

	list := &cobra.Command{
		Use: "list", Short: "List configs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, pid, eid, err := resolveEnv()
			if err != nil {
				return err
			}
			var resp struct {
				Configs []struct {
					ID           string  `json:"id"`
					Name         string  `json:"name"`
					InheritsFrom *string `json:"inherits_from"`
				} `json:"configs"`
			}
			if err := c.call("GET", "/v1/projects/"+pid+"/environments/"+eid+"/configs", nil, &resp); err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(resp.Configs)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tINHERITS\tID")
			for _, cf := range resp.Configs {
				inh := ""
				if cf.InheritsFrom != nil {
					inh = *cf.InheritsFrom
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", cf.Name, inh, cf.ID)
			}
			return tw.Flush()
		},
	}
	list.Flags().BoolVar(&asJSON, "json", false, "output JSON")

	del := &cobra.Command{
		Use: "delete <name>", Short: "Soft-delete a config", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cid, err := resolveCID(args[0], false)
			if err != nil {
				return err
			}
			if !yes && isTerminalCmd(cmd) {
				ok, err := promptLine(cmd, fmt.Sprintf("Delete config %q? [y/N]: ", args[0]))
				if err != nil {
					return err
				}
				if ok != "y" && ok != "Y" {
					return nil
				}
			}
			if err := c.call("DELETE", "/v1/configs/"+cid, nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "deleted config %s\n", args[0])
			return nil
		},
	}
	del.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")

	restore := &cobra.Command{
		Use: "restore <name>", Short: "Restore a soft-deleted config", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cid, err := resolveCID(args[0], true)
			if err != nil {
				return err
			}
			if err := c.call("POST", "/v1/configs/"+cid+"/restore", nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "restored config %s\n", args[0])
			return nil
		},
	}

	cmd.AddCommand(create, list, del, restore)
	return cmd
}
