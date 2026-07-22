package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

type notifChannelView struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Enabled bool     `json:"enabled"`
	Events  []string `json:"events"`
}

type notifDeliveryView struct {
	EventKind   string `json:"event_kind"`
	Status      string `json:"status"`
	Attempts    int    `json:"attempts"`
	LastError   string `json:"last_error"`
	CreatedAt   string `json:"created_at"`
	DeliveredAt string `json:"delivered_at"`
}

func newNotificationsCmd() *cobra.Command {
	var address, token string
	cmd := &cobra.Command{
		Use:     "notifications",
		Aliases: []string{"notify"},
		Short:   "Manage outbound alerting channels (webhook / Slack)",
	}
	cmd.PersistentFlags().StringVar(&address, "address", "", "server address")
	cmd.PersistentFlags().StringVar(&token, "token", "", "service token")

	newClient := func() (*apiClient, error) { return newAPIClient(address, token) }

	// create
	var name, ctype, url, hmacKey, events string
	var smtpHost, smtpFrom, smtpUser, smtpPass, smtpTLS string
	var smtpTo []string
	var smtpPort int
	var smtpInsecure bool
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a notification channel (webhook / slack / smtp)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newClient()
			if err != nil {
				return err
			}
			body := map[string]any{
				"name":   name,
				"type":   ctype,
				"events": splitCSV(events),
			}
			if ctype == "smtp" {
				// Recipients: repeatable --smtp-to and/or comma-separated tokens.
				var to []string
				for _, t := range smtpTo {
					to = append(to, splitCSV(t)...)
				}
				// Password: flag, else prompt on stdin (never echoed).
				pw := smtpPass
				if pw == "" && smtpUser != "" {
					pw, err = promptHidden(cmd, "SMTP password: ")
					if err != nil {
						return err
					}
				}
				body["smtp_host"] = smtpHost
				body["smtp_port"] = smtpPort
				body["smtp_from"] = smtpFrom
				body["smtp_to"] = to
				if smtpUser != "" {
					body["smtp_username"] = smtpUser
				}
				if pw != "" {
					body["smtp_password"] = pw
				}
				if smtpTLS != "" {
					body["smtp_tls_mode"] = smtpTLS
				}
				if smtpInsecure {
					body["smtp_insecure_skip_verify"] = true
				}
			} else {
				body["url"] = url
				if hmacKey != "" {
					body["hmac_key"] = hmacKey
				}
			}
			var out notifChannelView
			if err := c.call("POST", "/v1/notifications/channels", body, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created channel %s (%s)\n", out.ID, out.Name)
			return nil
		},
	}
	create.Flags().StringVar(&name, "name", "", "channel name (required)")
	create.Flags().StringVar(&ctype, "type", "webhook", "channel type: webhook, slack or smtp")
	create.Flags().StringVar(&url, "url", "", "destination URL (webhook/slack; required for those types)")
	create.Flags().StringVar(&hmacKey, "hmac-key", "", "webhook HMAC signing key (optional)")
	create.Flags().StringVar(&events, "events", "", "comma-separated event kinds: rotation.failed,sync.failed,promotion.pending,access.denied")
	create.Flags().StringVar(&smtpHost, "smtp-host", "", "SMTP server host (type=smtp)")
	create.Flags().IntVar(&smtpPort, "smtp-port", 587, "SMTP server port (type=smtp)")
	create.Flags().StringVar(&smtpFrom, "smtp-from", "", "envelope/from address (type=smtp)")
	create.Flags().StringArrayVar(&smtpTo, "smtp-to", nil, "recipient address (repeatable or comma-separated; type=smtp)")
	create.Flags().StringVar(&smtpUser, "smtp-username", "", "SMTP auth username (type=smtp; optional)")
	create.Flags().StringVar(&smtpPass, "smtp-password", "", "SMTP auth password (type=smtp; prefer stdin prompt over a flag visible in shell history)")
	create.Flags().StringVar(&smtpTLS, "smtp-tls", "", "SMTP TLS mode: starttls (default), implicit or none")
	create.Flags().BoolVar(&smtpInsecure, "smtp-insecure-skip-verify", false, "skip SMTP TLS certificate verification (footgun; self-hosted relays only)")
	_ = create.MarkFlagRequired("name")
	_ = create.MarkFlagRequired("events")

	// list
	var asJSON bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List notification channels",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newClient()
			if err != nil {
				return err
			}
			var out struct {
				Channels []notifChannelView `json:"channels"`
			}
			if err := c.call("GET", "/v1/notifications/channels", nil, &out); err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(out.Channels)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tNAME\tTYPE\tENABLED\tEVENTS")
			for _, ch := range out.Channels {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%t\t%s\n", ch.ID, ch.Name, ch.Type, ch.Enabled, strings.Join(ch.Events, ","))
			}
			return tw.Flush()
		},
	}
	list.Flags().BoolVar(&asJSON, "json", false, "output JSON")

	// update
	var enable, disable bool
	var upEvents, upURL, upHMAC string
	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a channel (enable/disable, events, url)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if enable && disable {
				return fmt.Errorf("--enable and --disable are mutually exclusive")
			}
			c, err := newClient()
			if err != nil {
				return err
			}
			body := map[string]any{}
			if enable {
				body["enabled"] = true
			}
			if disable {
				body["enabled"] = false
			}
			if cmd.Flags().Changed("events") {
				body["events"] = splitCSV(upEvents)
			}
			if cmd.Flags().Changed("url") {
				body["url"] = upURL
				if upHMAC != "" {
					body["hmac_key"] = upHMAC
				}
			}
			if len(body) == 0 {
				return fmt.Errorf("nothing to update")
			}
			if err := c.call("PATCH", "/v1/notifications/channels/"+args[0], body, nil); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "updated")
			return nil
		},
	}
	update.Flags().BoolVar(&enable, "enable", false, "enable the channel")
	update.Flags().BoolVar(&disable, "disable", false, "disable the channel")
	update.Flags().StringVar(&upEvents, "events", "", "replace subscribed event kinds (comma-separated)")
	update.Flags().StringVar(&upURL, "url", "", "replace destination URL")
	update.Flags().StringVar(&upHMAC, "hmac-key", "", "replace webhook HMAC key (with --url)")

	// delete
	del := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a channel",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient()
			if err != nil {
				return err
			}
			if err := c.call("DELETE", "/v1/notifications/channels/"+args[0], nil, nil); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "deleted")
			return nil
		},
	}

	// test
	test := &cobra.Command{
		Use:   "test <id>",
		Short: "Send a test notification to a channel",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient()
			if err != nil {
				return err
			}
			if err := c.call("POST", "/v1/notifications/channels/"+args[0]+"/test", nil, nil); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "test delivered")
			return nil
		},
	}

	// deliveries
	deliveries := &cobra.Command{
		Use:   "deliveries <id>",
		Short: "Show recent delivery history for a channel",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient()
			if err != nil {
				return err
			}
			var out struct {
				Deliveries []notifDeliveryView `json:"deliveries"`
			}
			if err := c.call("GET", "/v1/notifications/channels/"+args[0]+"/deliveries", nil, &out); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "EVENT\tSTATUS\tATTEMPTS\tCREATED\tERROR")
			for _, d := range out.Deliveries {
				fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", d.EventKind, d.Status, d.Attempts, d.CreatedAt, orDash(d.LastError))
			}
			return tw.Flush()
		},
	}

	cmd.AddCommand(create, list, update, del, test, deliveries)
	return cmd
}

// splitCSV splits a comma-separated flag into trimmed, non-empty tokens.
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
