package main

import (
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"
)

func newBackupCmd() *cobra.Command {
	var address, token, out string
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Stream a full-instance backup (JSONL) to stdout or --out",
		Long: "Streams GET /v1/sys/backup: a key-preserving logical dump. The file\n" +
			"contains only wrapped keys and ciphertext — it is useless without the\n" +
			"original unseal shares/KMS key, and safe to store like any backup.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			body, err := c.stream("GET", "/v1/sys/backup")
			if err != nil {
				return err
			}
			defer body.Close()
			var w io.Writer = cmd.OutOrStdout()
			if out != "" {
				f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G304 -- operator-chosen output path
				if err != nil {
					return err
				}
				defer f.Close()
				w = f
			}
			n, err := io.Copy(w, body)
			if err != nil {
				return fmt.Errorf("backup stream interrupted after %d bytes: %w", n, err)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "backup complete (%d bytes)\n", n)
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", "", "server address (default: stored login address)")
	cmd.Flags().StringVar(&token, "token", "", "service token (overrides stored session)")
	cmd.Flags().StringVar(&out, "out", "", "write to file instead of stdout (created 0600)")
	return cmd
}

func newRestoreCmd() *cobra.Command {
	var address string
	cmd := &cobra.Command{
		Use:   "restore [file]",
		Args:  cobra.MaximumNArgs(1),
		Short: "Restore a backup into an EMPTY instance (reads stdin without a file arg)",
		Long: "POSTs the dump to /v1/sys/restore. Only valid against a freshly\n" +
			"migrated, uninitialized instance. Afterwards the instance is sealed:\n" +
			"unseal with the ORIGINAL shares or KMS key of the backed-up instance.",
		RunE: func(cmd *cobra.Command, args []string) error {
			var in io.Reader = cmd.InOrStdin()
			if len(args) == 1 {
				f, err := os.Open(args[0]) // #nosec G304 -- operator-supplied backup path
				if err != nil {
					return err
				}
				defer f.Close()
				in = f
			}
			req, err := http.NewRequest("POST", resolveAddress(address)+"/v1/sys/restore", in)
			if err != nil {
				return err
			}
			req.Header.Set("Content-Type", "application/x-ndjson")
			// No total timeout: large restores stream for a while.
			resp, err := (&http.Client{}).Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode >= 300 {
				return rewriteAPIError(decodeAPIError(resp))
			}
			cmd.Println("restored — the instance is sealed; unseal with the ORIGINAL shares (janus unseal)")
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", "", "server address")
	return cmd
}
