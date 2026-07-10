package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newRotationCmd() *cobra.Command {
	var address, token string
	cmd := &cobra.Command{
		Use:   "rotation",
		Short: "Manage secret rotation policies",
	}
	cmd.PersistentFlags().StringVar(&address, "address", "", "server address (default: stored/env)")
	cmd.PersistentFlags().StringVar(&token, "token", "", "service token (default: stored/env)")

	// create
	var configID, secretKey, typ string
	var intervalSeconds int64
	var adminDSN, role string
	var passwordLen int
	var url, hmacKey, notifyURL, notifyHMACKey string
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a rotation policy",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			body := map[string]any{
				"config_id": configID, "secret_key": secretKey, "type": typ,
				"interval_seconds": intervalSeconds,
				"config": map[string]any{
					"admin_dsn": adminDSN, "role": role, "password_len": passwordLen,
					"url": url, "hmac_key": hmacKey,
					"notify_url": notifyURL, "notify_hmac_key": notifyHMACKey,
				},
			}
			var out map[string]any
			if err := c.call("POST", "/v1/rotation/policies", body, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created rotation policy %v (status %v)\n", out["id"], out["status"])
			return nil
		},
	}
	create.Flags().StringVar(&configID, "config", "", "target config id (required)")
	create.Flags().StringVar(&secretKey, "key", "", "secret key to rotate (required)")
	create.Flags().StringVar(&typ, "type", "", "rotator type: postgres|webhook (required)")
	create.Flags().Int64Var(&intervalSeconds, "interval-seconds", 0, "rotation interval in seconds (required)")
	create.Flags().StringVar(&adminDSN, "admin-dsn", "", "postgres admin DSN (postgres type)")
	create.Flags().StringVar(&role, "role", "", "postgres role to rotate (postgres type)")
	create.Flags().IntVar(&passwordLen, "password-len", 32, "generated password length")
	create.Flags().StringVar(&url, "url", "", "webhook URL (webhook type)")
	create.Flags().StringVar(&hmacKey, "hmac-key", "", "webhook HMAC signing key (webhook type)")
	create.Flags().StringVar(&notifyURL, "notify-url", "", "optional post-rotation notify URL")
	create.Flags().StringVar(&notifyHMACKey, "notify-hmac-key", "", "optional notify HMAC key")

	// list
	var projectID string
	list := &cobra.Command{
		Use:   "list",
		Short: "List rotation policies for a project",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out struct {
				Policies []struct {
					ID             string `json:"id"`
					SecretKey      string `json:"secret_key"`
					Type           string `json:"type"`
					Status         string `json:"status"`
					NextRotationAt string `json:"next_rotation_at"`
				} `json:"policies"`
			}
			if err := c.call("GET", "/v1/rotation/policies?project_id="+projectID, nil, &out); err != nil {
				return err
			}
			for _, p := range out.Policies {
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %-20s %-8s %-8s next=%s\n", p.ID, p.SecretKey, p.Type, p.Status, p.NextRotationAt)
			}
			return nil
		},
	}
	list.Flags().StringVar(&projectID, "project", "", "project id (required)")

	// get
	get := &cobra.Command{
		Use: "get <id>", Short: "Show a rotation policy", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out map[string]any
			if err := c.call("GET", "/v1/rotation/policies/"+args[0], nil, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%+v\n", out)
			return nil
		},
	}

	// delete
	del := &cobra.Command{
		Use: "delete <id>", Short: "Delete a rotation policy", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			return c.call("DELETE", "/v1/rotation/policies/"+args[0], nil, nil)
		},
	}

	// update
	var setInterval int64
	var setStatus string
	update := &cobra.Command{
		Use: "update <id>", Short: "Update interval or status", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			body := map[string]any{}
			if setInterval > 0 {
				body["interval_seconds"] = setInterval
			}
			if setStatus != "" {
				body["status"] = setStatus
			}
			return c.call("PATCH", "/v1/rotation/policies/"+args[0], body, nil)
		},
	}
	update.Flags().Int64Var(&setInterval, "interval-seconds", 0, "new interval")
	update.Flags().StringVar(&setStatus, "status", "", "new status: active|paused")

	// rotate
	rotate := &cobra.Command{
		Use: "rotate <id>", Short: "Rotate now", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out struct {
				ConfigVersion int `json:"config_version"`
			}
			if err := c.call("POST", "/v1/rotation/policies/"+args[0]+"/rotate", nil, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "rotated → config version %d\n", out.ConfigVersion)
			return nil
		},
	}

	cmd.AddCommand(create, list, get, update, del, rotate)
	return cmd
}
