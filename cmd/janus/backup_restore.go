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
			var n int64
			if out == "" {
				n, err = io.Copy(cmd.OutOrStdout(), body)
				if err != nil {
					return fmt.Errorf("backup stream interrupted after %d bytes: %w", n, err)
				}
			} else if n, err = writeStreamFile(out, body); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "backup complete (%d bytes)\n", n)
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", "", "server address (default: stored login address)")
	cmd.Flags().StringVar(&token, "token", "", "service token (overrides stored session)")
	cmd.Flags().StringVar(&out, "out", "", "write to file instead of stdout (created 0600, written atomically)")
	cmd.AddCommand(newBackupRehearseCmd())
	return cmd
}

// newBackupRehearseCmd verifies a scheduled S3 backup restores WITHOUT touching
// the live instance. It POSTs /v1/sys/backup/rehearse: the server downloads the
// backup (latest, or --object-key) and streams it through a structural +
// decryptability check, then discards it. Nothing live is overwritten.
func newBackupRehearseCmd() *cobra.Command {
	var address, token, objectKey string
	cmd := &cobra.Command{
		Use:   "rehearse",
		Short: "Verify a scheduled S3 backup restores, without touching the live instance",
		Long: "POSTs /v1/sys/backup/rehearse. The server downloads the latest scheduled\n" +
			"S3 backup (or --object-key) and verifies it restores — validating the\n" +
			"archive structure and that its wrapped key material decrypts under the\n" +
			"current unseal — then discards it. It NEVER overwrites live data.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			body := map[string]string{}
			if objectKey != "" {
				body["object_key"] = objectKey
			}
			var res struct {
				ObjectKey   string `json:"object_key"`
				Verified    bool   `json:"verified"`
				Records     int    `json:"records"`
				Tables      int    `json:"tables"`
				SizeBytes   int64  `json:"size_bytes"`
				SchemaVer   int64  `json:"schema_version"`
				Decryptable bool   `json:"decryptable"`
				Note        string `json:"note"`
			}
			if err := c.call("POST", "/v1/sys/backup/rehearse", body, &res); err != nil {
				return err
			}
			status := "VERIFIED"
			if !res.Verified {
				status = "NOT VERIFIED"
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"%s  object=%s  records=%d tables=%d bytes=%d schema=%d decryptable=%t\n",
				status, res.ObjectKey, res.Records, res.Tables, res.SizeBytes, res.SchemaVer, res.Decryptable)
			if res.Note != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "note: %s\n", res.Note)
			}
			if !res.Verified {
				return fmt.Errorf("backup rehearsal did not verify")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", "", "server address (default: stored login address)")
	cmd.Flags().StringVar(&token, "token", "", "service token (overrides stored session)")
	cmd.Flags().StringVar(&objectKey, "object-key", "", "verify a specific object key (default: the latest backup)")
	return cmd
}

// writeStreamFile streams r to path atomically at mode 0600: temp file opened
// O_EXCL (never follows a planted symlink, never inherits a pre-existing
// file's looser mode), Close error checked (a corrupt DR artifact must not
// report success), then renamed over the target — a truncated file never
// lands at the final path. Streaming sibling of writeSecretFile.
func writeStreamFile(path string, r io.Reader) (int64, error) {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|os.O_EXCL, 0o600) // #nosec G304 -- operator-chosen output path, O_EXCL blocks symlink/preexisting follow
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, r)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return n, fmt.Errorf("backup stream interrupted after %d bytes: %w", n, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return n, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return n, err
	}
	return n, nil
}

func newRestoreCmd() *cobra.Command {
	var address string
	cmd := &cobra.Command{
		Use:   "restore [file]",
		Args:  cobra.MaximumNArgs(1),
		Short: "Restore a backup into an EMPTY instance (reads stdin when no file is given)",
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
			resp, err := streamClient().Do(req)
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
	cmd.Flags().StringVar(&address, "address", "", "server address (default: stored login address)")
	return cmd
}
