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
	// mysql
	var myAddr, myAdminUser, myAdminPassword, myDBName, myTLS, myUser, myHost string
	// redis
	var rdAddr, rdAdminUser, rdAdminPassword, rdUser, rdRules string
	var rdTLS, rdSkipVerify bool
	// oauth
	var oaTokenURL, oaClientID, oaClientSecret, oaScope, oaAudience string
	// aws_iam
	var iamUser, iamRegion, iamAccessKeyID, iamSecretAccessKey, iamSessionToken string
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
					"mysql_addr": myAddr, "mysql_admin_user": myAdminUser,
					"mysql_admin_password": myAdminPassword, "mysql_db_name": myDBName,
					"mysql_tls": myTLS, "mysql_user": myUser, "mysql_host": myHost,
					"redis_addr": rdAddr, "redis_admin_user": rdAdminUser,
					"redis_admin_password": rdAdminPassword, "redis_tls": rdTLS,
					"redis_skip_verify": rdSkipVerify, "redis_user": rdUser, "redis_rules": rdRules,
					"oauth_token_url": oaTokenURL, "oauth_client_id": oaClientID,
					"oauth_client_secret": oaClientSecret, "oauth_scope": oaScope, "oauth_audience": oaAudience,
					"iam_user": iamUser, "iam_region": iamRegion, "iam_access_key_id": iamAccessKeyID,
					"iam_secret_access_key": iamSecretAccessKey, "iam_session_token": iamSessionToken,
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
	create.Flags().StringVar(&typ, "type", "", "rotator type: postgres|webhook|mysql|redis|oauth|aws_iam (required)")
	create.Flags().Int64Var(&intervalSeconds, "interval-seconds", 0, "rotation interval in seconds (required)")
	create.Flags().StringVar(&adminDSN, "admin-dsn", "", "postgres admin DSN (postgres type)")
	create.Flags().StringVar(&role, "role", "", "postgres role to rotate (postgres type)")
	create.Flags().IntVar(&passwordLen, "password-len", 32, "generated password length")
	create.Flags().StringVar(&url, "url", "", "webhook URL (webhook type)")
	create.Flags().StringVar(&hmacKey, "hmac-key", "", "webhook HMAC signing key (webhook type)")
	// mysql
	create.Flags().StringVar(&myAddr, "mysql-addr", "", "mysql host:port (mysql type, discrete form)")
	create.Flags().StringVar(&myAdminUser, "mysql-admin-user", "", "mysql admin user (mysql type, discrete form)")
	create.Flags().StringVar(&myAdminPassword, "mysql-admin-password", "", "mysql admin password (mysql type, discrete form)")
	create.Flags().StringVar(&myDBName, "mysql-db", "", "mysql default database (mysql type, optional)")
	create.Flags().StringVar(&myTLS, "mysql-tls", "", "mysql TLS mode: true|skip-verify|preferred|false (mysql type)")
	create.Flags().StringVar(&myUser, "mysql-user", "", "mysql target account user to rotate (mysql type)")
	create.Flags().StringVar(&myHost, "mysql-host", "", "mysql target account host, default '%' (mysql type)")
	// redis
	create.Flags().StringVar(&rdAddr, "redis-addr", "", "redis host:port (redis type)")
	create.Flags().StringVar(&rdAdminUser, "redis-admin-user", "", "redis AUTH user (redis type, Redis 6+ ACL)")
	create.Flags().StringVar(&rdAdminPassword, "redis-admin-password", "", "redis AUTH password (redis type)")
	create.Flags().BoolVar(&rdTLS, "redis-tls", false, "dial redis over TLS (redis type)")
	create.Flags().BoolVar(&rdSkipVerify, "redis-skip-verify", false, "skip redis TLS verification (redis type)")
	create.Flags().StringVar(&rdUser, "redis-user", "", "redis target ACL username to rotate (redis type)")
	create.Flags().StringVar(&rdRules, "redis-rules", "", "space-separated ACL rules to preserve (redis type, e.g. \"~app:* +@read\")")
	// oauth (GENERATING rotator: the provider mints the token)
	create.Flags().StringVar(&oaTokenURL, "oauth-token-url", "", "OAuth token endpoint (oauth type)")
	create.Flags().StringVar(&oaClientID, "oauth-client-id", "", "OAuth client id (oauth type)")
	create.Flags().StringVar(&oaClientSecret, "oauth-client-secret", "", "OAuth client secret (oauth type, write-only)")
	create.Flags().StringVar(&oaScope, "oauth-scope", "", "OAuth scope (oauth type, optional)")
	create.Flags().StringVar(&oaAudience, "oauth-audience", "", "OAuth audience (oauth type, optional)")
	// aws_iam (GENERATING rotator: AWS mints a new access key)
	create.Flags().StringVar(&iamUser, "iam-user", "", "target IAM user to rotate (aws_iam type)")
	create.Flags().StringVar(&iamRegion, "iam-region", "", "AWS region (aws_iam type)")
	create.Flags().StringVar(&iamAccessKeyID, "iam-access-key-id", "", "admin access key id (aws_iam type, write-only)")
	create.Flags().StringVar(&iamSecretAccessKey, "iam-secret-access-key", "", "admin secret access key (aws_iam type, write-only)")
	create.Flags().StringVar(&iamSessionToken, "iam-session-token", "", "admin session token (aws_iam type, optional, write-only)")
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
