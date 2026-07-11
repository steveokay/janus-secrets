package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newDynamicCmd() *cobra.Command {
	var address, token string
	cmd := &cobra.Command{
		Use:   "dynamic",
		Short: "Manage dynamic Postgres credentials",
	}
	cmd.PersistentFlags().StringVar(&address, "address", "", "server address (default: stored/env)")
	cmd.PersistentFlags().StringVar(&token, "token", "", "service token (default: stored/env)")

	roles := &cobra.Command{Use: "roles", Short: "Manage dynamic roles"}

	// roles create
	var configID, name, adminDSN, creation, revocation, renew string
	var defaultTTL, maxTTL int64
	rolesCreate := &cobra.Command{
		Use:   "create",
		Short: "Create a dynamic role",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			body := map[string]any{
				"config_id": configID, "name": name,
				"default_ttl_seconds": defaultTTL, "max_ttl_seconds": maxTTL,
				"config": map[string]any{
					"admin_dsn": adminDSN, "creation_statements": creation,
					"revocation_statements": revocation, "renew_statements": renew,
				},
			}
			var out map[string]any
			if err := c.call("POST", "/v1/dynamic/roles", body, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created dynamic role %v (%v)\n", out["id"], out["name"])
			return nil
		},
	}
	rolesCreate.Flags().StringVar(&configID, "config", "", "target config id (required)")
	rolesCreate.Flags().StringVar(&name, "name", "", "role name (required)")
	rolesCreate.Flags().Int64Var(&defaultTTL, "default-ttl-seconds", 3600, "default lease TTL")
	rolesCreate.Flags().Int64Var(&maxTTL, "max-ttl-seconds", 86400, "maximum lease TTL")
	rolesCreate.Flags().StringVar(&adminDSN, "admin-dsn", "", "postgres admin DSN (required)")
	rolesCreate.Flags().StringVar(&creation, "creation", "", "creation SQL with {{name}}/{{password}}/{{expiration}} (required)")
	rolesCreate.Flags().StringVar(&revocation, "revocation", "", "revocation SQL (optional; default DROP ROLE IF EXISTS)")
	rolesCreate.Flags().StringVar(&renew, "renew", "", "renew SQL (optional; default ALTER ROLE ... VALID UNTIL)")

	// roles list
	var listConfig string
	rolesList := &cobra.Command{
		Use:   "list",
		Short: "List dynamic roles for a config",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out struct {
				Roles []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"roles"`
			}
			if err := c.call("GET", "/v1/dynamic/roles?config_id="+listConfig, nil, &out); err != nil {
				return err
			}
			for _, r := range out.Roles {
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %s\n", r.ID, r.Name)
			}
			return nil
		},
	}
	rolesList.Flags().StringVar(&listConfig, "config", "", "config id (required)")

	// roles delete
	rolesDelete := &cobra.Command{
		Use: "delete <id>", Short: "Delete a dynamic role (revokes its leases)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			return c.call("DELETE", "/v1/dynamic/roles/"+args[0], nil, nil)
		},
	}

	// roles get
	rolesGet := &cobra.Command{
		Use: "get <id>", Short: "Show a dynamic role", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out map[string]any
			if err := c.call("GET", "/v1/dynamic/roles/"+args[0], nil, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%+v\n", out)
			return nil
		},
	}

	roles.AddCommand(rolesCreate, rolesList, rolesGet, rolesDelete)

	// creds
	creds := &cobra.Command{
		Use: "creds <role-id>", Short: "Issue dynamic credentials", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out struct {
				LeaseID   string `json:"lease_id"`
				Username  string `json:"username"`
				Password  string `json:"password"`
				ExpiresAt string `json:"expires_at"`
			}
			if err := c.call("POST", "/v1/dynamic/roles/"+args[0]+"/creds", nil, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "lease=%s\nusername=%s\npassword=%s\nexpires=%s\n",
				out.LeaseID, out.Username, out.Password, out.ExpiresAt)
			return nil
		},
	}

	// renew
	renewCmd := &cobra.Command{
		Use: "renew <lease-id>", Short: "Renew a lease", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out struct {
				ExpiresAt string `json:"expires_at"`
			}
			if err := c.call("POST", "/v1/dynamic/leases/"+args[0]+"/renew", nil, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "renewed → expires %s\n", out.ExpiresAt)
			return nil
		},
	}

	// revoke
	revokeCmd := &cobra.Command{
		Use: "revoke <lease-id>", Short: "Revoke a lease", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			return c.call("POST", "/v1/dynamic/leases/"+args[0]+"/revoke", nil, nil)
		},
	}

	// leases
	var leaseRole string
	leases := &cobra.Command{
		Use: "leases", Short: "List leases for a role",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out struct {
				Leases []struct {
					ID         string `json:"id"`
					Status     string `json:"status"`
					DBUsername string `json:"db_username"`
					ExpiresAt  string `json:"expires_at"`
				} `json:"leases"`
			}
			if err := c.call("GET", "/v1/dynamic/leases?role_id="+leaseRole, nil, &out); err != nil {
				return err
			}
			for _, l := range out.Leases {
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %-12s %-24s exp=%s\n", l.ID, l.Status, l.DBUsername, l.ExpiresAt)
			}
			return nil
		},
	}
	leases.Flags().StringVar(&leaseRole, "role", "", "role id (required)")

	cmd.AddCommand(roles, creds, renewCmd, revokeCmd, leases)
	return cmd
}
