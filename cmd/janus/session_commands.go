package main

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// sessionView mirrors the /v1/auth/sessions item shape (no credential material).
type sessionView struct {
	ID         string `json:"id"`
	CreatedAt  string `json:"created_at"`
	LastSeenAt string `json:"last_seen_at"`
	ExpiresAt  string `json:"expires_at"`
	IP         string `json:"ip"`
	UserAgent  string `json:"user_agent"`
	Current    bool   `json:"current"`
}

func newSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "List and revoke your own login sessions",
	}
	cmd.AddCommand(newSessionListCmd(), newSessionRevokeCmd())
	return cmd
}

func newSessionListCmd() *cobra.Command {
	var address, token string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List your active sessions",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var resp struct {
				Sessions []sessionView `json:"sessions"`
			}
			if err := c.call("GET", "/v1/auth/sessions", nil, &resp); err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(resp.Sessions)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tCURRENT\tIP\tLAST SEEN\tUSER AGENT")
			for _, s := range resp.Sessions {
				cur := ""
				if s.Current {
					cur = "*"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", s.ID, cur, orDash(s.IP), s.LastSeenAt, orDash(s.UserAgent))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&address, "address", "", "server address")
	cmd.Flags().StringVar(&token, "token", "", "service token")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

func newSessionRevokeCmd() *cobra.Command {
	var address, token string
	var others bool
	cmd := &cobra.Command{
		Use:   "revoke [id]",
		Short: "Revoke a session by id, or --others to sign out everywhere else",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if others == (len(args) == 1) {
				return fmt.Errorf("provide exactly one of a session id or --others")
			}
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			if others {
				var resp struct {
					Revoked int `json:"revoked"`
				}
				if err := c.call("DELETE", "/v1/auth/sessions", nil, &resp); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "revoked %d other session(s)\n", resp.Revoked)
				return nil
			}
			if err := c.call("DELETE", "/v1/auth/sessions/"+args[0], nil, nil); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "revoked")
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", "", "server address")
	cmd.Flags().StringVar(&token, "token", "", "service token")
	cmd.Flags().BoolVar(&others, "others", false, "revoke all sessions except the current one")
	return cmd
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
