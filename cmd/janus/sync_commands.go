package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	var address, token string
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Manage secret sync targets",
	}
	cmd.PersistentFlags().StringVar(&address, "address", "", "server address (default: stored/env)")
	cmd.PersistentFlags().StringVar(&token, "token", "", "service token (default: stored/env)")

	// create
	var configID, provider string
	var prune bool
	var intervalSeconds int64
	var owner, repo, environment, pat string
	var apiURL, caCert, k8sToken, namespace, secretName string
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a sync target",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			body := map[string]any{
				"config_id":        configID,
				"provider":         provider,
				"prune":            prune,
				"interval_seconds": intervalSeconds,
				"addr": map[string]any{
					"owner": owner, "repo": repo, "environment": environment,
					"namespace": namespace, "secret_name": secretName,
				},
				"creds": map[string]any{
					"pat": pat, "api_url": apiURL, "ca_cert": caCert, "token": k8sToken,
				},
			}
			var out map[string]any
			if err := c.call("POST", "/v1/sync/targets", body, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created sync target %v (status %v)\n", out["id"], out["status"])
			return nil
		},
	}
	create.Flags().StringVar(&configID, "config", "", "target config id (required)")
	create.Flags().StringVar(&provider, "provider", "", "sync provider: github|k8s (required)")
	create.Flags().BoolVar(&prune, "prune", true, "prune remote keys not present in the config")
	create.Flags().Int64Var(&intervalSeconds, "interval-seconds", 0, "sync interval in seconds (required)")
	create.Flags().StringVar(&owner, "owner", "", "GitHub repo owner (github type)")
	create.Flags().StringVar(&repo, "repo", "", "GitHub repo name (github type)")
	create.Flags().StringVar(&environment, "environment", "", "GitHub environment name (github type)")
	create.Flags().StringVar(&pat, "pat", "", "GitHub personal access token (github type)")
	create.Flags().StringVar(&apiURL, "api-url", "", "Kubernetes API server URL (k8s type)")
	create.Flags().StringVar(&caCert, "ca-cert", "", "Kubernetes CA certificate (k8s type)")
	create.Flags().StringVar(&k8sToken, "k8s-token", "", "Kubernetes bearer token (k8s type)")
	create.Flags().StringVar(&namespace, "namespace", "", "Kubernetes namespace (k8s type)")
	create.Flags().StringVar(&secretName, "secret-name", "", "Kubernetes Secret name (k8s type)")

	// list
	var projectID string
	list := &cobra.Command{
		Use:   "list",
		Short: "List sync targets for a project",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out struct {
				Targets []struct {
					ID         string `json:"id"`
					Provider   string `json:"provider"`
					Status     string `json:"status"`
					ConfigID   string `json:"config_id"`
					NextSyncAt string `json:"next_sync_at"`
				} `json:"targets"`
			}
			if err := c.call("GET", "/v1/sync/targets?project_id="+projectID, nil, &out); err != nil {
				return err
			}
			for _, t := range out.Targets {
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %-8s %-8s config=%s next=%s\n", t.ID, t.Provider, t.Status, t.ConfigID, t.NextSyncAt)
			}
			return nil
		},
	}
	list.Flags().StringVar(&projectID, "project", "", "project id (required)")

	// get
	get := &cobra.Command{
		Use: "get <id>", Short: "Show a sync target", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out map[string]any
			if err := c.call("GET", "/v1/sync/targets/"+args[0], nil, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%+v\n", out)
			return nil
		},
	}

	// update
	var setInterval int64
	var setPrune bool
	var setStatus string
	var setOwner, setRepo, setEnvironment, setPAT string
	var setAPIURL, setCACert, setK8sToken, setNamespace, setSecretName string
	update := &cobra.Command{
		Use: "update <id>", Short: "Update a sync target", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			body := map[string]any{}
			if cmd.Flags().Changed("interval-seconds") {
				body["interval_seconds"] = setInterval
			}
			if cmd.Flags().Changed("prune") {
				body["prune"] = setPrune
			}
			if cmd.Flags().Changed("status") {
				body["status"] = setStatus
			}
			if setOwner != "" || setRepo != "" || setEnvironment != "" || setNamespace != "" || setSecretName != "" {
				body["addr"] = map[string]any{
					"owner": setOwner, "repo": setRepo, "environment": setEnvironment,
					"namespace": setNamespace, "secret_name": setSecretName,
				}
			}
			if setPAT != "" || setAPIURL != "" || setCACert != "" || setK8sToken != "" {
				body["creds"] = map[string]any{
					"pat": setPAT, "api_url": setAPIURL, "ca_cert": setCACert, "token": setK8sToken,
				}
			}
			if err := c.call("PATCH", "/v1/sync/targets/"+args[0], body, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "updated sync target %s\n", args[0])
			return nil
		},
	}
	update.Flags().Int64Var(&setInterval, "interval-seconds", 0, "new sync interval in seconds")
	update.Flags().BoolVar(&setPrune, "prune", false, "prune remote keys not present in the config")
	update.Flags().StringVar(&setStatus, "status", "", "new status: active|paused")
	update.Flags().StringVar(&setOwner, "owner", "", "GitHub repo owner (github type)")
	update.Flags().StringVar(&setRepo, "repo", "", "GitHub repo name (github type)")
	update.Flags().StringVar(&setEnvironment, "environment", "", "GitHub environment name (github type)")
	update.Flags().StringVar(&setPAT, "pat", "", "GitHub personal access token (github type)")
	update.Flags().StringVar(&setAPIURL, "api-url", "", "Kubernetes API server URL (k8s type)")
	update.Flags().StringVar(&setCACert, "ca-cert", "", "Kubernetes CA certificate (k8s type)")
	update.Flags().StringVar(&setK8sToken, "k8s-token", "", "Kubernetes bearer token (k8s type)")
	update.Flags().StringVar(&setNamespace, "namespace", "", "Kubernetes namespace (k8s type)")
	update.Flags().StringVar(&setSecretName, "secret-name", "", "Kubernetes Secret name (k8s type)")

	// delete
	del := &cobra.Command{
		Use: "delete <id>", Short: "Delete a sync target", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			if err := c.call("DELETE", "/v1/sync/targets/"+args[0], nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted sync target %s\n", args[0])
			return nil
		},
	}

	// sync
	syncNow := &cobra.Command{
		Use: "sync <id>", Short: "Trigger a sync now", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out struct {
				Synced bool `json:"synced"`
			}
			if err := c.call("POST", "/v1/sync/targets/"+args[0]+"/sync", nil, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "sync triggered for %s\n", args[0])
			return nil
		},
	}

	cmd.AddCommand(create, list, get, update, del, syncNow)
	return cmd
}
