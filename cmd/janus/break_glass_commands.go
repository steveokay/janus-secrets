package main

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

type breakGlassGrantView struct {
	ID            string `json:"id"`
	UserID        string `json:"user_id"`
	ScopeLevel    string `json:"scope_level"`
	ProjectID     string `json:"project_id"`
	EnvironmentID string `json:"environment_id"`
	ElevatedRole  string `json:"elevated_role"`
	Reason        string `json:"reason"`
	ActivatedAt   string `json:"activated_at"`
	ExpiresAt     string `json:"expires_at"`
}

// newBreakGlassCmd is the `janus break-glass` command group: guarded
// self-service time-boxed emergency role elevation over /v1/break-glass.
func newBreakGlassCmd() *cobra.Command {
	var address, token string
	cmd := &cobra.Command{
		Use:     "break-glass",
		Aliases: []string{"breakglass"},
		Short:   "Time-boxed emergency role elevation (guarded, loud, audited)",
	}
	cmd.PersistentFlags().StringVar(&address, "address", "", "server address")
	cmd.PersistentFlags().StringVar(&token, "token", "", "service token")
	newClient := func() (*apiClient, error) { return newAPIClient(address, token) }

	// activate
	var scope, projectID, envID, role, reason, ttl string
	activate := &cobra.Command{
		Use:   "activate",
		Short: "Activate break-glass on a scope you already hold a role on",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newClient()
			if err != nil {
				return err
			}
			body := map[string]any{
				"scope_level": scope,
				"role":        role,
				"reason":      reason,
			}
			if projectID != "" {
				body["project_id"] = projectID
			}
			if envID != "" {
				body["environment_id"] = envID
			}
			if ttl != "" {
				body["ttl"] = ttl
			}
			var out breakGlassGrantView
			if err := c.call("POST", "/v1/break-glass", body, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"activated break-glass %s: %s on %s until %s\n",
				out.ID, out.ElevatedRole, out.ScopeLevel, out.ExpiresAt)
			return nil
		},
	}
	activate.Flags().StringVar(&scope, "scope", "", "scope level: instance, project or environment (required)")
	activate.Flags().StringVar(&projectID, "project", "", "project id (scope=project)")
	activate.Flags().StringVar(&envID, "environment", "", "environment id (scope=environment)")
	activate.Flags().StringVar(&role, "role", "", "elevated role: developer, admin or owner (must exceed your held role; required)")
	activate.Flags().StringVar(&reason, "reason", "", "mandatory justification (non-secret; recorded in the audit chain)")
	activate.Flags().StringVar(&ttl, "ttl", "", "requested duration, e.g. 30m (clamped to the server max)")
	_ = activate.MarkFlagRequired("scope")
	_ = activate.MarkFlagRequired("role")
	_ = activate.MarkFlagRequired("reason")

	// list
	var asJSON bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List active break-glass grants (your own, or all if you are an admin)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newClient()
			if err != nil {
				return err
			}
			var out struct {
				Grants []breakGlassGrantView `json:"grants"`
			}
			if err := c.call("GET", "/v1/break-glass", nil, &out); err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(out.Grants)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tSCOPE\tROLE\tEXPIRES\tREASON")
			for _, g := range out.Grants {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
					g.ID, g.ScopeLevel, g.ElevatedRole, g.ExpiresAt, g.Reason)
			}
			return tw.Flush()
		},
	}
	list.Flags().BoolVar(&asJSON, "json", false, "output JSON")

	// revoke
	revoke := &cobra.Command{
		Use:   "revoke <id>",
		Short: "End a break-glass grant early (self or admin)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient()
			if err != nil {
				return err
			}
			if err := c.call("DELETE", "/v1/break-glass/"+args[0], nil, nil); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "revoked")
			return nil
		},
	}

	cmd.AddCommand(activate, list, revoke)
	return cmd
}
