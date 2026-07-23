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
	var gitlabURL, glProject, glEnvScope, glToken string
	var awsRegion, awsPathPrefix, awsAccessKeyID, awsSecretAccessKey, awsSessionToken string
	var cfAccountID, cfScriptName, cfAPIToken string
	var smRegion, smPathPrefix, smAccessKeyID, smSecretAccessKey, smSessionToken string
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a sync target",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			// aws_secrets shares the region/path_prefix addr fields and
			// access_key_id/secret_access_key/session_token creds with aws_ssm.
			region, pathPrefix := awsRegion, awsPathPrefix
			accessKeyID, secretAccessKey, sessionToken := awsAccessKeyID, awsSecretAccessKey, awsSessionToken
			if provider == "aws_secrets" {
				region, pathPrefix = smRegion, smPathPrefix
				accessKeyID, secretAccessKey, sessionToken = smAccessKeyID, smSecretAccessKey, smSessionToken
			}
			body := map[string]any{
				"config_id":        configID,
				"provider":         provider,
				"prune":            prune,
				"interval_seconds": intervalSeconds,
				"addr": map[string]any{
					"owner": owner, "repo": repo, "environment": environment,
					"namespace": namespace, "secret_name": secretName,
					"gitlab_url": gitlabURL, "project": glProject, "environment_scope": glEnvScope,
					"region": region, "path_prefix": pathPrefix,
					"account_id": cfAccountID, "script_name": cfScriptName,
				},
				"creds": map[string]any{
					"pat": pat, "api_url": apiURL, "ca_cert": caCert, "token": k8sToken,
					"access_key_id": accessKeyID, "secret_access_key": secretAccessKey,
					"session_token": sessionToken,
					"api_token":     cfAPIToken,
				},
			}
			// GitLab uses the shared `token` creds field (PRIVATE-TOKEN).
			if provider == "gitlab" && glToken != "" {
				body["creds"].(map[string]any)["token"] = glToken
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
	create.Flags().StringVar(&provider, "provider", "", "sync provider: github|k8s|gitlab|aws_ssm|cloudflare|aws_secrets (required)")
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
	create.Flags().StringVar(&gitlabURL, "gitlab-url", "", "GitLab base URL (gitlab type; default https://gitlab.com)")
	create.Flags().StringVar(&glProject, "project", "", "GitLab project id or URL-encoded group/proj (gitlab type)")
	create.Flags().StringVar(&glEnvScope, "environment-scope", "", "GitLab CI/CD variable environment scope (gitlab type, optional)")
	create.Flags().StringVar(&glToken, "gitlab-token", "", "GitLab PAT/project access token with api scope (gitlab type)")
	create.Flags().StringVar(&awsRegion, "aws-region", "", "AWS region (aws_ssm type)")
	create.Flags().StringVar(&awsPathPrefix, "path-prefix", "", "SSM parameter path prefix, e.g. /janus/app/prod (aws_ssm type)")
	create.Flags().StringVar(&awsAccessKeyID, "aws-access-key-id", "", "AWS access key id (aws_ssm type)")
	create.Flags().StringVar(&awsSecretAccessKey, "aws-secret-access-key", "", "AWS secret access key (aws_ssm type)")
	create.Flags().StringVar(&awsSessionToken, "aws-session-token", "", "AWS session token (aws_ssm type, optional)")
	create.Flags().StringVar(&cfAccountID, "cf-account-id", "", "Cloudflare account id (cloudflare type)")
	create.Flags().StringVar(&cfScriptName, "cf-script-name", "", "Cloudflare Worker script name (cloudflare type)")
	create.Flags().StringVar(&cfAPIToken, "cf-api-token", "", "Cloudflare API token with Workers Scripts Edit (cloudflare type)")
	create.Flags().StringVar(&smRegion, "sm-region", "", "AWS region (aws_secrets type)")
	create.Flags().StringVar(&smPathPrefix, "sm-path-prefix", "", "Secrets Manager name prefix, e.g. janus/app/prod (aws_secrets type)")
	create.Flags().StringVar(&smAccessKeyID, "sm-access-key-id", "", "AWS access key id (aws_secrets type)")
	create.Flags().StringVar(&smSecretAccessKey, "sm-secret-access-key", "", "AWS secret access key (aws_secrets type)")
	create.Flags().StringVar(&smSessionToken, "sm-session-token", "", "AWS session token (aws_secrets type, optional)")

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
	var setGitlabURL, setGLProject, setGLEnvScope, setGLToken string
	var setAWSRegion, setAWSPathPrefix, setAWSAccessKeyID, setAWSSecretAccessKey, setAWSSessionToken string
	var setCFAccountID, setCFScriptName, setCFAPIToken string
	var setSMRegion, setSMPathPrefix, setSMAccessKeyID, setSMSecretAccessKey, setSMSessionToken string
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
			// aws_secrets reuses the shared region/path_prefix + AWS creds fields.
			setRegion, setPathPrefix := setAWSRegion, setAWSPathPrefix
			if setSMRegion != "" {
				setRegion = setSMRegion
			}
			if setSMPathPrefix != "" {
				setPathPrefix = setSMPathPrefix
			}
			setAKID, setSAK, setST := setAWSAccessKeyID, setAWSSecretAccessKey, setAWSSessionToken
			if setSMAccessKeyID != "" {
				setAKID = setSMAccessKeyID
			}
			if setSMSecretAccessKey != "" {
				setSAK = setSMSecretAccessKey
			}
			if setSMSessionToken != "" {
				setST = setSMSessionToken
			}
			if setOwner != "" || setRepo != "" || setEnvironment != "" || setNamespace != "" || setSecretName != "" ||
				setGitlabURL != "" || setGLProject != "" || setGLEnvScope != "" || setRegion != "" || setPathPrefix != "" ||
				setCFAccountID != "" || setCFScriptName != "" {
				body["addr"] = map[string]any{
					"owner": setOwner, "repo": setRepo, "environment": setEnvironment,
					"namespace": setNamespace, "secret_name": setSecretName,
					"gitlab_url": setGitlabURL, "project": setGLProject, "environment_scope": setGLEnvScope,
					"region": setRegion, "path_prefix": setPathPrefix,
					"account_id": setCFAccountID, "script_name": setCFScriptName,
				}
			}
			if setPAT != "" || setAPIURL != "" || setCACert != "" || setK8sToken != "" || setGLToken != "" ||
				setAKID != "" || setSAK != "" || setST != "" || setCFAPIToken != "" {
				token := setK8sToken
				if setGLToken != "" {
					token = setGLToken // gitlab reuses the shared token creds field
				}
				body["creds"] = map[string]any{
					"pat": setPAT, "api_url": setAPIURL, "ca_cert": setCACert, "token": token,
					"access_key_id": setAKID, "secret_access_key": setSAK,
					"session_token": setST, "api_token": setCFAPIToken,
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
	update.Flags().StringVar(&setGitlabURL, "gitlab-url", "", "GitLab base URL (gitlab type)")
	update.Flags().StringVar(&setGLProject, "project", "", "GitLab project id or URL-encoded group/proj (gitlab type)")
	update.Flags().StringVar(&setGLEnvScope, "environment-scope", "", "GitLab variable environment scope (gitlab type)")
	update.Flags().StringVar(&setGLToken, "gitlab-token", "", "GitLab PAT/project access token (gitlab type)")
	update.Flags().StringVar(&setAWSRegion, "aws-region", "", "AWS region (aws_ssm type)")
	update.Flags().StringVar(&setAWSPathPrefix, "path-prefix", "", "SSM parameter path prefix (aws_ssm type)")
	update.Flags().StringVar(&setAWSAccessKeyID, "aws-access-key-id", "", "AWS access key id (aws_ssm type)")
	update.Flags().StringVar(&setAWSSecretAccessKey, "aws-secret-access-key", "", "AWS secret access key (aws_ssm type)")
	update.Flags().StringVar(&setAWSSessionToken, "aws-session-token", "", "AWS session token (aws_ssm type)")
	update.Flags().StringVar(&setCFAccountID, "cf-account-id", "", "Cloudflare account id (cloudflare type)")
	update.Flags().StringVar(&setCFScriptName, "cf-script-name", "", "Cloudflare Worker script name (cloudflare type)")
	update.Flags().StringVar(&setCFAPIToken, "cf-api-token", "", "Cloudflare API token (cloudflare type)")
	update.Flags().StringVar(&setSMRegion, "sm-region", "", "AWS region (aws_secrets type)")
	update.Flags().StringVar(&setSMPathPrefix, "sm-path-prefix", "", "Secrets Manager name prefix (aws_secrets type)")
	update.Flags().StringVar(&setSMAccessKeyID, "sm-access-key-id", "", "AWS access key id (aws_secrets type)")
	update.Flags().StringVar(&setSMSecretAccessKey, "sm-secret-access-key", "", "AWS secret access key (aws_secrets type)")
	update.Flags().StringVar(&setSMSessionToken, "sm-session-token", "", "AWS session token (aws_secrets type)")

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
