package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newLoginCmd() *cobra.Command {
	var address, email string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with email + password and store a session",
		RunE: func(cmd *cobra.Command, _ []string) error {
			addr := resolveAddress(address)
			if email == "" {
				e, err := promptLine(cmd, "Email: ")
				if err != nil {
					return err
				}
				email = strings.TrimSpace(e)
			}
			pw, err := promptHidden(cmd, "Password: ")
			if err != nil {
				return err
			}
			session, err := doLogin(addr, email, pw, "")
			// If the server requires a second factor, prompt for the code and retry.
			var ae *apiError
			if errors.As(err, &ae) && ae.Code == "totp_required" {
				code, perr := promptLine(cmd, "Two-factor code: ")
				if perr != nil {
					return perr
				}
				session, err = doLogin(addr, email, pw, strings.TrimSpace(code))
			}
			if err != nil {
				if errors.As(err, &ae) {
					return rewriteAPIError(ae)
				}
				return err
			}
			if err := saveAuth(&authState{Address: addr, Session: session, Email: email}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Logged in as %s\n", email)
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", "", "server address")
	cmd.Flags().StringVar(&email, "email", "", "account email (prompted if omitted)")
	return cmd
}

// doLogin posts credentials and returns the janus_session cookie value. On an
// API error it returns the raw *apiError so the caller can detect a
// totp_required challenge; other errors pass through.
func doLogin(address, email, password, totpCode string) (string, error) {
	payload := map[string]string{"email": email, "password": password}
	if totpCode != "" {
		payload["totp_code"] = totpCode
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", address+"/v1/auth/login", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	hc := &http.Client{Timeout: 30 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", decodeAPIError(resp)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == "janus_session" {
			return ck.Value, nil
		}
	}
	return "", fmt.Errorf("login succeeded but no session cookie was returned")
}

func newLogoutCmd() *cobra.Command {
	var address, token string
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Clear the stored session (and revoke it server-side)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err == nil {
				_ = c.call("POST", "/v1/auth/logout", nil, nil) // best-effort
			}
			if err := clearSession(); err != nil {
				return err
			}
			fmt.Fprintln(cmd.ErrOrStderr(), "Logged out")
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", "", "server address")
	cmd.Flags().StringVar(&token, "token", "", "service token (overrides stored session)")
	return cmd
}

// promptLine reads a plain line from stdin (prompt to stderr).
func promptLine(cmd *cobra.Command, label string) (string, error) {
	fmt.Fprint(cmd.ErrOrStderr(), label)
	r := bufio.NewReader(cmd.InOrStdin())
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// promptHidden reads a secret: echo-off on a TTY, plain line when piped.
func promptHidden(cmd *cobra.Command, label string) (string, error) {
	if f, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(cmd.ErrOrStderr(), label)
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(cmd.ErrOrStderr())
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	return promptLine(cmd, label)
}
