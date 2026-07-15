package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// secretFlags carries the address/token/binding flags shared by secrets/run cmds.
type secretFlags struct {
	address, token       string
	project, env, config string
}

func (f *secretFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.address, "address", "", "server address")
	cmd.Flags().StringVar(&f.token, "token", "", "service token (overrides stored session)")
	cmd.Flags().StringVar(&f.project, "project", "", "project slug (overrides .janus.yaml)")
	cmd.Flags().StringVar(&f.env, "env", "", "environment slug (overrides .janus.yaml)")
	cmd.Flags().StringVar(&f.config, "config", "", "config name (overrides .janus.yaml)")
}

// resolveCID builds an API client and resolves the bound config id from cwd.
func (f *secretFlags) resolveCID() (*apiClient, string, error) {
	c, err := newAPIClient(f.address, f.token)
	if err != nil {
		return nil, "", err
	}
	dir, err := os.Getwd()
	if err != nil {
		return nil, "", err
	}
	project, env, config, err := resolveBinding(dir, f.project, f.env, f.config)
	if err != nil {
		return nil, "", err
	}
	cid, err := c.resolveConfigID(project, env, config)
	if err != nil {
		return nil, "", err
	}
	return c, cid, nil
}

func newSecretsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "secrets", Short: "Read and write secrets"}
	cmd.AddCommand(newSecretsListCmd(), newSecretsGetCmd(), newSecretsSetCmd(), newSecretsDeleteCmd(), newSecretsDownloadCmd(), newSecretsLockCmd(), newSecretsUnlockCmd())
	return cmd
}

func newSecretsListCmd() *cobra.Command {
	var f secretFlags
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List secret keys with masked metadata (no values, not audited)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, cid, err := f.resolveCID()
			if err != nil {
				return err
			}
			var resp struct {
				Secrets map[string]struct {
					ValueVersion int    `json:"value_version"`
					CreatedAt    string `json:"created_at"`
					Origin       string `json:"origin"`
				} `json:"secrets"`
			}
			if err := c.call("GET", "/v1/configs/"+cid+"/secrets", nil, &resp); err != nil {
				return err
			}
			if asJSON {
				return writeJSONOut(cmd, resp)
			}
			keys := make([]string, 0, len(resp.Secrets))
			for k := range resp.Secrets {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "KEY\tVERSION\tORIGIN\tUPDATED")
			for _, k := range keys {
				m := resp.Secrets[k]
				fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n", k, m.ValueVersion, m.Origin, m.CreatedAt)
			}
			return tw.Flush()
		},
	}
	f.bind(cmd)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of a table")
	return cmd
}

func newSecretsGetCmd() *cobra.Command {
	var f secretFlags
	var version int
	var raw bool
	cmd := &cobra.Command{
		Use:   "get KEY",
		Short: "Print one secret value to stdout (audited)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cid, err := f.resolveCID()
			if err != nil {
				return err
			}
			path := "/v1/configs/" + cid + "/secrets/" + url.PathEscape(args[0])
			q := url.Values{}
			if version > 0 {
				q.Set("version", fmt.Sprint(version))
			}
			if raw {
				q.Set("raw", "true")
			}
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			var resp struct {
				Value string `json:"value"`
			}
			if err := c.call("GET", path, nil, &resp); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), resp.Value)
			return nil
		},
	}
	f.bind(cmd)
	cmd.Flags().IntVar(&version, "version", 0, "fetch a historical value version")
	cmd.Flags().BoolVar(&raw, "raw", false, "return the stored value verbatim (do not resolve references)")
	return cmd
}

// writeJSONOut encodes v as indented JSON to the command's stdout.
func writeJSONOut(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
