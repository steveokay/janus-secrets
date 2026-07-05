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
			if err := c.call("GET", "/v1/configs/"+cid+"/secrets?reveal=true", nil, &resp); err != nil {
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
			if err := os.WriteFile(output, data, 0o600); err != nil {
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
	return cmd
}
