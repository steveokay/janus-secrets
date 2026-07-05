package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newSecretsDownloadCmd() *cobra.Command {
	var f secretFlags
	var format, output string
	var plain bool
	var raw bool
	cmd := &cobra.Command{
		Use:   "download",
		Short: "Download all secret values in env|json|yaml",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := formatSecrets(format, map[string]string{}); err != nil {
				return err // validate format name before any network call
			}
			// --plain guard: only the CLI writing a file needs it.
			if output != "" && !plain {
				return fmt.Errorf("refusing to write plaintext to %s without --plain", output)
			}
			c, cid, err := f.resolveCID()
			if err != nil {
				return err
			}
			var resp struct {
				Secrets map[string]string `json:"secrets"`
			}
			path := "/v1/configs/" + cid + "/secrets?reveal=true"
			if raw {
				path += "&raw=true"
			}
			if err := c.call("GET", path, nil, &resp); err != nil {
				return err
			}
			data, err := formatSecrets(format, resp.Secrets)
			if err != nil {
				return err
			}
			if output == "" {
				_, err := cmd.OutOrStdout().Write(data)
				return err
			}
			if err := writeSecretFile(output, data); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Wrote %d secret(s) to %s\n", len(resp.Secrets), output)
			return nil
		},
	}
	f.bind(cmd)
	cmd.Flags().StringVar(&format, "format", "env", "output format: env|json|yaml")
	cmd.Flags().StringVar(&output, "output", "", "write to a file instead of stdout (requires --plain)")
	cmd.Flags().BoolVar(&plain, "plain", false, "permit writing plaintext secrets to disk")
	cmd.Flags().BoolVar(&raw, "raw", false, "download stored values verbatim (do not resolve references)")
	return cmd
}

// writeSecretFile writes plaintext secrets to path atomically at mode 0600. It
// writes to a temp file opened O_EXCL (so it never follows a planted symlink and
// never inherits a pre-existing file's looser mode) then renames over the target,
// which also makes the write atomic — no truncated/partial secret file on failure.
// Mirrors saveAuth's pattern in config_store.go.
func writeSecretFile(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|os.O_EXCL, 0o600) // #nosec G304 -- caller-chosen output path, O_EXCL blocks symlink/preexisting follow
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
