package main

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// newMasterKeyCmd wires the owner-only master-key lifecycle commands: status
// (report the current version + rekey progress), rotate (KMS-only single-step
// rotation), and rekey (the Shamir rekey ceremony: init then submit one share
// at a time until a fresh set of unseal shares is returned).
func newMasterKeyCmd() *cobra.Command {
	var address, token string
	cmd := &cobra.Command{
		Use:   "master-key",
		Short: "Rotate the master key (rekey ceremony / KMS)",
	}
	cmd.PersistentFlags().StringVar(&address, "address", "", "server address (default: stored/env)")
	cmd.PersistentFlags().StringVar(&token, "token", "", "service token (default: stored/env)")

	status := &cobra.Command{
		Use:   "status",
		Short: "Show the master key version, unseal type, and rekey progress",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out struct {
				UnsealType      string  `json:"unseal_type"`
				Version         int     `json:"master_key_version"`
				RotatedAt       *string `json:"rotated_at"`
				RekeyInProgress bool    `json:"rekey_in_progress"`
				Submitted       int     `json:"submitted"`
				Required        int     `json:"required"`
			}
			if err := c.call("GET", "/v1/sys/master-key", nil, &out); err != nil {
				return err
			}
			rotated := "never"
			if out.RotatedAt != nil {
				rotated = *out.RotatedAt
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"seal %s; master key version %d; last rotated %s; rekey in progress: %v\n",
				out.UnsealType, out.Version, rotated, out.RekeyInProgress)
			if out.RekeyInProgress {
				fmt.Fprintf(cmd.OutOrStdout(), "rekey shares submitted: %d/%d\n", out.Submitted, out.Required)
			}
			return nil
		},
	}

	rotate := &cobra.Command{
		Use:   "rotate",
		Short: "Rotate the master key in a single step (KMS seals only)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out struct {
				Version int `json:"master_key_version"`
			}
			if err := c.call("POST", "/v1/sys/master-key/rotate", nil, &out); err != nil {
				// The server returns 400 "shamir seal requires a rekey ceremony"
				// for a Shamir seal; point the operator at the ceremony command.
				return fmt.Errorf("%w (use `janus master-key rekey`)", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "rotated master key to version %d\n", out.Version)
			return nil
		},
	}

	var shares []string
	var cancel bool
	rekey := &cobra.Command{
		Use:   "rekey",
		Short: "Run the Shamir rekey ceremony (init + submit shares → fresh shares)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			if cancel {
				if err := c.call("DELETE", "/v1/sys/master-key/rekey", nil, nil); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "rekey ceremony canceled")
				return nil
			}

			var initOut struct {
				Nonce     string `json:"nonce"`
				Required  int    `json:"required"`
				Submitted int    `json:"submitted"`
			}
			if err := c.call("POST", "/v1/sys/master-key/rekey/init", nil, &initOut); err != nil {
				return err
			}

			// Prefer shares supplied via --share; otherwise prompt on stdin for
			// the required count, one line per share.
			if len(shares) == 0 {
				reader := bufio.NewReader(cmd.InOrStdin())
				for i := 0; i < initOut.Required; i++ {
					fmt.Fprintf(cmd.ErrOrStderr(), "Share %d of %d: ", i+1, initOut.Required)
					line, rerr := reader.ReadString('\n')
					share := strings.TrimSpace(line)
					if share != "" {
						shares = append(shares, share)
					}
					if rerr != nil {
						break
					}
				}
			}

			for i, share := range shares {
				var subOut struct {
					Complete  bool     `json:"complete"`
					Submitted int      `json:"submitted"`
					Required  int      `json:"required"`
					Version   int      `json:"master_key_version"`
					NewShares []string `json:"new_shares"`
				}
				body := map[string]string{"nonce": initOut.Nonce, "share": share}
				if err := c.call("POST", "/v1/sys/master-key/rekey/submit", body, &subOut); err != nil {
					return err
				}
				if subOut.Complete {
					fmt.Fprintln(cmd.OutOrStdout(),
						"WARNING: New unseal shares — store these now, they will NOT be shown again:")
					for _, sh := range subOut.NewShares {
						fmt.Fprintln(cmd.OutOrStdout(), sh)
					}
					fmt.Fprintf(cmd.OutOrStdout(), "master key rotated to version %d\n", subOut.Version)
					return nil
				}
				// Not complete and this was our last available share.
				if i == len(shares)-1 {
					more := subOut.Required - subOut.Submitted
					return fmt.Errorf("rekey incomplete: %d more share(s) required — rerun with additional `--share` values", more)
				}
			}
			return nil
		},
	}
	rekey.Flags().StringArrayVar(&shares, "share", nil, "an unseal share to submit (repeatable); prefer stdin over a flag visible in shell history")
	rekey.Flags().BoolVar(&cancel, "cancel", false, "cancel an in-progress rekey ceremony")

	cmd.AddCommand(status, rotate, rekey)
	return cmd
}
