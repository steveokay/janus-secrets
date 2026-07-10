package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type sealStatus struct {
	Initialized bool   `json:"initialized"`
	Sealed      bool   `json:"sealed"`
	Type        string `json:"type"`
	Threshold   int    `json:"threshold"`
	Shares      int    `json:"shares"`
	Progress    *struct {
		Submitted int `json:"submitted"`
		Required  int `json:"required"`
	} `json:"progress"`
}

func newInitCmd() *cobra.Command {
	var address string
	var shares, threshold int
	var asJSON bool
	var adminEmail string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize the seal (returns Shamir shares exactly once)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			req := map[string]any{}
			if shares != 0 {
				req["shares"] = shares
			}
			if threshold != 0 {
				req["threshold"] = threshold
			}
			if adminEmail != "" {
				req["admin_email"] = adminEmail
			}
			var resp struct {
				Type   string   `json:"type"`
				Shares []string `json:"shares"`
				Admin  *struct {
					Email    string `json:"email"`
					Password string `json:"password"`
				} `json:"admin"`
			}
			if err := sysCall(address, "POST", "/v1/sys/init", req, &resp); err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				return enc.Encode(resp)
			}
			cmd.Printf("Seal initialized (type: %s).\n", resp.Type)
			if len(resp.Shares) > 0 {
				cmd.Println("\nUnseal shares — store each in a separate secure location.")
				cmd.Println("They WILL NOT BE SHOWN AGAIN.")
				for i, sh := range resp.Shares {
					cmd.Printf("  Share %d: %s\n", i+1, sh)
				}
			}
			if resp.Admin != nil {
				cmd.Println("\nInitial admin credential — change it after first login.")
				cmd.Println("It WILL NOT BE SHOWN AGAIN.")
				cmd.Printf("  Email:    %s\n", resp.Admin.Email)
				cmd.Printf("  Password: %s\n", resp.Admin.Password)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", defaultAddress(), "server address")
	cmd.Flags().IntVar(&shares, "shares", 0, "number of Shamir shares (default 5)")
	cmd.Flags().IntVar(&threshold, "threshold", 0, "unseal threshold (default 3)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the raw JSON response")
	cmd.Flags().StringVar(&adminEmail, "admin-email", "", "email for the initial admin (default admin@localhost)")
	return cmd
}

func newUnsealCmd() *cobra.Command {
	var address, share string
	var reset bool
	cmd := &cobra.Command{
		Use:   "unseal",
		Short: "Submit an unseal share (or trigger a KMS unseal retry)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if reset {
				// Discard all submitted shares — the recovery path the server
				// points at after a poisoned reconstruction (key_check_failed).
				var resp struct {
					Sealed bool `json:"sealed"`
				}
				if err := sysCall(address, "POST", "/v1/sys/unseal/reset", nil, &resp); err != nil {
					return err
				}
				cmd.Println("submitted shares discarded; resubmit from scratch")
				return nil
			}

			var st sealStatus
			if err := sysCall(address, "GET", "/v1/sys/seal-status", nil, &st); err != nil {
				return err
			}

			var req any
			if st.Type == "awskms" {
				req = nil // empty-body retry
			} else {
				if share == "" {
					s, err := readShare(cmd)
					if err != nil {
						return err
					}
					share = s
				}
				if share == "" {
					return fmt.Errorf("share is required for a shamir seal")
				}
				req = map[string]string{"share": share}
			}

			var resp struct {
				Sealed   bool `json:"sealed"`
				Progress *struct {
					Submitted int `json:"submitted"`
					Required  int `json:"required"`
				} `json:"progress"`
			}
			if err := sysCall(address, "POST", "/v1/sys/unseal", req, &resp); err != nil {
				return err
			}
			if resp.Sealed {
				if resp.Progress != nil {
					cmd.Printf("sealed — %d/%d shares\n", resp.Progress.Submitted, resp.Progress.Required)
				} else {
					cmd.Println("sealed")
				}
				return nil
			}
			cmd.Println("unsealed")
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", defaultAddress(), "server address")
	cmd.Flags().StringVar(&share, "share", "", "unseal share (hex); prefer stdin — a flag value is visible in process lists and shell history")
	cmd.Flags().BoolVar(&reset, "reset", false, "discard all submitted shares (recover from a bad share)")
	return cmd
}

// readShare reads a share from the command's stdin: echo-off prompt on a TTY,
// plain line read when piped.
func readShare(cmd *cobra.Command) (string, error) {
	if f, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(cmd.ErrOrStderr(), "Share: ")
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(cmd.ErrOrStderr())
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	r := bufio.NewReader(cmd.InOrStdin())
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func newSealStatusCmd() *cobra.Command {
	var address string
	cmd := &cobra.Command{
		Use:   "seal-status",
		Short: "Show seal status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var st sealStatus
			if err := sysCall(address, "GET", "/v1/sys/seal-status", nil, &st); err != nil {
				return err
			}
			cmd.Printf("initialized: %v\nsealed:      %v\ntype:        %s\n", st.Initialized, st.Sealed, st.Type)
			if st.Type == "shamir" && st.Initialized {
				cmd.Printf("threshold:   %d of %d\n", st.Threshold, st.Shares)
			}
			if st.Progress != nil {
				cmd.Printf("progress:    %d/%d shares\n", st.Progress.Submitted, st.Progress.Required)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", defaultAddress(), "server address")
	return cmd
}

func newSealCmd() *cobra.Command {
	var address, token string
	cmd := &cobra.Command{
		Use:   "seal",
		Short: "Seal the server (wipes the in-memory master key)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Sealing requires sys:seal (admin) — unlike init/unseal/seal-status,
			// which must work pre-auth. Uses the same credential resolution as
			// the secrets commands: --token > JANUS_TOKEN > stored session.
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			if err := c.call("POST", "/v1/sys/seal", nil, nil); err != nil {
				return err
			}
			cmd.Println("sealed")
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", "", "server address (default: stored login address)")
	cmd.Flags().StringVar(&token, "token", "", "service token (overrides stored session)")
	return cmd
}
