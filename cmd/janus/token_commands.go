package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

func newTokenCmd() *cobra.Command {
	var address, token, project, env, config string
	var name, access, ttl string
	var asJSON, yes bool

	cmd := &cobra.Command{Use: "token", Short: "Manage service tokens"}
	cmd.PersistentFlags().StringVar(&address, "address", "", "server address")
	cmd.PersistentFlags().StringVar(&token, "token", "", "service token")

	mint := &cobra.Command{
		Use:   "mint",
		Short: "Mint a scoped service token (shown once)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			acc := ""
			switch access {
			case "read", "r":
				acc = "read"
			case "readwrite", "rw":
				acc = "readwrite"
			default:
				return fmt.Errorf("--access must be read|rw")
			}
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			dir, _ := os.Getwd()
			p, e, cfg, err := bindingValues(dir, project, env, config)
			if err != nil {
				return err
			}
			if p == "" || e == "" {
				return fmt.Errorf("no project/environment — pass --project/--env or run `janus setup`")
			}
			var kind, id string
			if cfg != "" {
				kind = "config"
				if id, err = c.resolveConfigID(p, e, cfg); err != nil {
					return err
				}
			} else {
				kind = "environment"
				if _, id, err = c.resolveEnvID(p, e); err != nil {
					return err
				}
			}
			body := map[string]any{"name": name, "scope": map[string]string{"kind": kind, "id": id}, "access": acc}
			if ttl != "" {
				d, err := time.ParseDuration(ttl)
				if err != nil {
					return fmt.Errorf("invalid --ttl: %w", err)
				}
				body["ttl_seconds"] = int64(d.Seconds())
			}
			var out struct {
				Token, ID, Name, Access string
				Scope                   struct{ Kind, ID string }
				ExpiresAt               *string `json:"expires_at"`
			}
			if err := c.call("POST", "/v1/tokens", body, &out); err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
			}
			fmt.Fprintln(cmd.OutOrStdout(), out.Token)
			fmt.Fprintf(cmd.ErrOrStderr(), "minted %s (%s) scope=%s/%s access=%s — shown once\n",
				out.Name, out.ID, out.Scope.Kind, out.Scope.ID, out.Access)
			return nil
		},
	}
	mint.Flags().StringVar(&name, "name", "", "token name (required)")
	mint.Flags().StringVar(&project, "project", "", "project slug (overrides .janus.yaml)")
	mint.Flags().StringVar(&env, "env", "", "environment slug (overrides .janus.yaml)")
	mint.Flags().StringVar(&config, "config", "", "scope to this config (name); omit for environment scope")
	mint.Flags().StringVar(&access, "access", "read", "read|rw")
	mint.Flags().StringVar(&ttl, "ttl", "", "lifetime, e.g. 24h (default: no expiry)")
	mint.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	_ = mint.MarkFlagRequired("name")

	list := &cobra.Command{
		Use: "list", Short: "List service tokens (metadata only)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var resp struct {
				Tokens []struct {
					ID        string  `json:"id"`
					Name      string  `json:"name"`
					ScopeKind string  `json:"scope_kind"`
					ScopeID   string  `json:"scope_id"`
					Access    string  `json:"access"`
					ExpiresAt *string `json:"expires_at"`
				} `json:"tokens"`
			}
			if err := c.call("GET", "/v1/tokens", nil, &resp); err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(resp.Tokens)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tNAME\tSCOPE\tACCESS\tEXPIRES")
			for _, tk := range resp.Tokens {
				exp := "never"
				if tk.ExpiresAt != nil {
					exp = *tk.ExpiresAt
				}
				fmt.Fprintf(tw, "%s\t%s\t%s/%s\t%s\t%s\n", tk.ID, tk.Name, tk.ScopeKind, tk.ScopeID, tk.Access, exp)
			}
			return tw.Flush()
		},
	}
	list.Flags().BoolVar(&asJSON, "json", false, "output JSON")

	revoke := &cobra.Command{
		Use: "revoke <id>", Short: "Revoke a service token", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			if !yes && isTerminalCmd(cmd) {
				ok, err := promptLine(cmd, fmt.Sprintf("Revoke token %q? [y/N]: ", args[0]))
				if err != nil {
					return err
				}
				if ok != "y" && ok != "Y" {
					return nil
				}
			}
			if err := c.call("DELETE", "/v1/tokens/"+args[0], nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "revoked token %s\n", args[0])
			return nil
		},
	}
	revoke.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")

	cmd.AddCommand(mint, list, revoke)
	return cmd
}
