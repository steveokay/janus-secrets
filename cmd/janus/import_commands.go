package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// newImportCmd wires `janus import <source>` — inbound one-shot importers that
// read secrets from an external system (Doppler, Vault KV v2, AWS Secrets
// Manager) and write them into a target Janus project/env/config as ONE batched
// config version, using the existing authenticated Janus client.
//
// This is CLI-first and client-side: Janus never stores the external creds and
// gains no new endpoints. Source creds come from flags/env and are used only for
// the fetch — they are never logged. Values are never printed; dry-run and
// summaries surface key NAMES and counts only. A real write requires --confirm
// (or the interactive prompt).
func newImportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import secrets from Doppler / Vault KV / AWS Secrets Manager into a Janus config",
		Long: "Read secrets from an external system and create the corresponding " +
			"project → environment → config → secrets in Janus via the existing API.\n\n" +
			"Values are never printed (names + counts only). Source credentials come " +
			"from flags/env and are never stored or logged. --dry-run (default) shows " +
			"what would be imported; pass --confirm to write.",
	}
	cmd.AddCommand(newImportDopplerCmd(), newImportVaultCmd(), newImportAWSSMCmd())
	return cmd
}

// importTarget holds the Janus destination flags shared by every subcommand.
type importTarget struct {
	address, token          string
	project, env, configNm  string
	dryRun, confirm, create bool
	message                 string
}

// bindTarget registers the destination + behavior flags.
func (t *importTarget) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&t.address, "address", "", "Janus server address (default: stored/env)")
	cmd.Flags().StringVar(&t.token, "token", "", "Janus service token (default: stored/env)")
	cmd.Flags().StringVar(&t.project, "project", "", "target Janus project slug (required)")
	cmd.Flags().StringVar(&t.env, "env", "", "target Janus environment slug (required)")
	cmd.Flags().StringVar(&t.configNm, "config", "", "target Janus config name (required)")
	cmd.Flags().BoolVar(&t.dryRun, "dry-run", true, "print the key names + count that would be imported, without writing")
	cmd.Flags().BoolVar(&t.confirm, "confirm", false, "actually write the secrets (turns off --dry-run)")
	cmd.Flags().BoolVar(&t.create, "create", false, "create the target project/environment/config if missing")
	cmd.Flags().StringVar(&t.message, "message", "", "config-version message for the imported save")
}

// runImport is the shared pipeline: validate the target, render/execute the
// dry-run, or ensure the target exists and write the fetched secrets as ONE
// config version. It NEVER prints a secret value.
func runImport(cmd *cobra.Command, t *importTarget, source importSource, fetched fetchedSecrets) error {
	if t.project == "" || t.env == "" || t.configNm == "" {
		return fmt.Errorf("--project, --env and --config are all required")
	}
	keys := fetched.keys()
	target := fmt.Sprintf("%s/%s/%s", t.project, t.env, t.configNm)

	// A real write requires --confirm. --dry-run is on by default; the presence
	// of --confirm is the explicit opt-in to actually write.
	write := t.confirm

	// Dry-run (default): summarize names + count only, write nothing.
	if !write {
		printImportPlan(cmd, source, target, keys, false)
		return nil
	}

	if len(keys) == 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "import(%s): no secrets found — nothing to write\n", source)
		return nil
	}

	// Echo the value-free plan before writing so the operator sees what is about
	// to land (names + count only, never a value).
	printImportPlan(cmd, source, target, keys, true)

	c, err := newAPIClient(t.address, t.token)
	if err != nil {
		return err
	}
	cid, err := t.ensureConfig(c)
	if err != nil {
		return err
	}

	changes := make([]secretChange, 0, len(keys))
	for _, k := range keys {
		changes = append(changes, secretChange{Key: k, Value: fetched.pairs[k]})
	}
	msg := t.message
	if msg == "" {
		msg = fmt.Sprintf("import from %s", source)
	}
	req := map[string]any{"message": msg, "changes": changes}
	var resp struct {
		Version int `json:"version"`
	}
	if err := c.call("PUT", "/v1/configs/"+cid+"/secrets", req, &resp); err != nil {
		return err
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Imported %d secret(s) from %s into %s as v%d\n",
		len(changes), source, target, resp.Version)
	return nil
}

// printImportPlan writes the value-free plan (source, target, count, key names)
// to stderr so stdout stays clean. It never prints a value.
func printImportPlan(cmd *cobra.Command, source importSource, target string, keys []string, forConfirm bool) {
	w := cmd.ErrOrStderr()
	label := "DRY RUN"
	if forConfirm {
		label = "PLAN"
	}
	fmt.Fprintf(w, "%s: import %d secret(s) from %s → %s\n", label, len(keys), source, target)
	for _, k := range keys {
		fmt.Fprintf(w, "  + %s\n", k)
	}
	if !forConfirm {
		fmt.Fprintf(w, "No changes written. Re-run with --confirm to import.\n")
	}
}

// ensureConfig resolves the target config id, creating the project, environment,
// and config along the way when --create was passed. Without --create, a missing
// resource is a hard error.
func (t *importTarget) ensureConfig(c *apiClient) (string, error) {
	if !t.create {
		return c.resolveConfigID(t.project, t.env, t.configNm)
	}
	pid, err := t.ensureProject(c)
	if err != nil {
		return "", err
	}
	eid, err := t.ensureEnv(c, pid)
	if err != nil {
		return "", err
	}
	return t.ensureConfigIn(c, pid, eid)
}

func (t *importTarget) ensureProject(c *apiClient) (string, error) {
	pid, err := c.resolveProjectID(t.project)
	if err == nil {
		return pid, nil
	}
	var out struct{ ID, Slug, Name string }
	if cerr := c.call("POST", "/v1/projects", map[string]string{"slug": t.project, "name": t.project}, &out); cerr != nil {
		return "", cerr
	}
	return out.ID, nil
}

func (t *importTarget) ensureEnv(c *apiClient, pid string) (string, error) {
	_, eid, err := c.resolveEnvID(t.project, t.env)
	if err == nil {
		return eid, nil
	}
	var out struct{ ID, Slug, Name string }
	if cerr := c.call("POST", "/v1/projects/"+pid+"/environments",
		map[string]string{"slug": t.env, "name": t.env}, &out); cerr != nil {
		return "", cerr
	}
	return out.ID, nil
}

func (t *importTarget) ensureConfigIn(c *apiClient, pid, eid string) (string, error) {
	cid, err := c.resolveConfigID(t.project, t.env, t.configNm)
	if err == nil {
		return cid, nil
	}
	var out struct{ ID, Name string }
	if cerr := c.call("POST", "/v1/projects/"+pid+"/environments/"+eid+"/configs",
		map[string]any{"name": t.configNm}, &out); cerr != nil {
		return "", cerr
	}
	return out.ID, nil
}

// envOr returns the first non-empty of the flag value or the named env var.
func envOr(flagVal, envKey string) string {
	if flagVal != "" {
		return flagVal
	}
	return os.Getenv(envKey)
}

func newImportDopplerCmd() *cobra.Command {
	var t importTarget
	var dc dopplerConfig
	cmd := &cobra.Command{
		Use:   "doppler",
		Short: "Import a Doppler config's secrets",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dc.token = envOr(dc.token, "DOPPLER_TOKEN")
			fetched, err := fetchDoppler(context.Background(), dc)
			if err != nil {
				return err
			}
			return runImport(cmd, &t, sourceDoppler, fetched)
		},
	}
	t.bind(cmd)
	cmd.Flags().StringVar(&dc.token, "doppler-token", "", "Doppler service token (or DOPPLER_TOKEN); never stored")
	cmd.Flags().StringVar(&dc.project, "doppler-project", "", "Doppler project name")
	cmd.Flags().StringVar(&dc.config, "doppler-config", "", "Doppler config name")
	cmd.Flags().StringVar(&dc.apiBase, "doppler-api", "", "Doppler API base URL (default https://api.doppler.com)")
	return cmd
}

func newImportVaultCmd() *cobra.Command {
	var t importTarget
	var vc vaultConfig
	cmd := &cobra.Command{
		Use:   "vault",
		Short: "Import a Vault KV v2 path's keys",
		RunE: func(cmd *cobra.Command, _ []string) error {
			vc.addr = envOr(vc.addr, "VAULT_ADDR")
			vc.token = envOr(vc.token, "VAULT_TOKEN")
			fetched, err := fetchVault(context.Background(), vc)
			if err != nil {
				return err
			}
			return runImport(cmd, &t, sourceVault, fetched)
		},
	}
	t.bind(cmd)
	cmd.Flags().StringVar(&vc.addr, "vault-addr", "", "Vault address (or VAULT_ADDR), e.g. https://vault:8200")
	cmd.Flags().StringVar(&vc.token, "vault-token", "", "Vault token (or VAULT_TOKEN); never stored")
	cmd.Flags().StringVar(&vc.mount, "vault-mount", "secret", "Vault KV v2 mount")
	cmd.Flags().StringVar(&vc.path, "vault-path", "", "secret path under the mount, e.g. myapp/prod")
	return cmd
}

func newImportAWSSMCmd() *cobra.Command {
	var t importTarget
	var ac awsSMConfig
	cmd := &cobra.Command{
		Use:   "aws-sm",
		Short: "Import AWS Secrets Manager secrets under a name prefix",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ac.accessKeyID = envOr(ac.accessKeyID, "AWS_ACCESS_KEY_ID")
			ac.secretAccessKey = envOr(ac.secretAccessKey, "AWS_SECRET_ACCESS_KEY")
			ac.sessionToken = envOr(ac.sessionToken, "AWS_SESSION_TOKEN")
			ac.region = envOr(ac.region, "AWS_REGION")
			fetched, err := fetchAWSSM(context.Background(), ac)
			if err != nil {
				return err
			}
			return runImport(cmd, &t, sourceAWSSM, fetched)
		},
	}
	t.bind(cmd)
	cmd.Flags().StringVar(&ac.region, "aws-region", "", "AWS region (or AWS_REGION)")
	cmd.Flags().StringVar(&ac.accessKeyID, "aws-access-key-id", "", "AWS access key id (or AWS_ACCESS_KEY_ID); never stored")
	cmd.Flags().StringVar(&ac.secretAccessKey, "aws-secret-access-key", "", "AWS secret access key (or AWS_SECRET_ACCESS_KEY); never stored")
	cmd.Flags().StringVar(&ac.sessionToken, "aws-session-token", "", "AWS session token (or AWS_SESSION_TOKEN); never stored")
	cmd.Flags().StringVar(&ac.prefix, "aws-prefix", "", "Secrets Manager name prefix to import under, e.g. prod/myapp/")
	return cmd
}
