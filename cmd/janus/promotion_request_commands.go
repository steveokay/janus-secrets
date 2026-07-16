package main

import (
	"fmt"
	"net/url"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// promoteRequestApplyResult mirrors the ApplyResult summary returned by
// approve — the same shape as the direct-apply /v1/promote response.
type promoteRequestApplyResult struct {
	TargetVersion int      `json:"target_version"`
	Applied       []string `json:"applied"`
	Skipped       []string `json:"skipped"`
}

// addPromoteRequestSubcommands attaches request/requests/approve/reject/cancel
// to the parent `promote` command, covering the approval-workflow REST
// endpoints (/v1/promote/requests...). Value-free throughout: only key
// names, statuses, and counts are ever printed.
func addPromoteRequestSubcommands(parent *cobra.Command) {
	parent.AddCommand(
		newPromoteRequestCmd(),
		newPromoteRequestsListCmd(),
		newPromoteRequestApproveCmd(),
		newPromoteRequestRejectCmd(),
		newPromoteRequestCancelCmd(),
	)
}

// newPromoteRequestCmd submits a promotion request for review rather than
// applying it directly.
func newPromoteRequestCmd() *cobra.Command {
	var f secretFlags
	var toEnv, toName, note string
	var keys []string
	var all, createTarget bool
	var sourceVersion int

	cmd := &cobra.Command{
		Use:   "request --to <env>",
		Short: "Request promotion of the bound config's secrets to another environment",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if toEnv == "" {
				return fmt.Errorf("--to <env-slug> is required")
			}
			if all && len(keys) > 0 {
				return fmt.Errorf("--all and --key are mutually exclusive")
			}
			if len(keys) == 0 {
				// v1: --all still requires explicit --key selection; the
				// selections list drives what the approver applies, so an
				// empty list would produce a no-op request.
				return fmt.Errorf("select keys with --key <K> (repeatable)")
			}

			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			project, env, config, err := resolveBinding(dir, f.project, f.env, f.config)
			if err != nil {
				return err
			}
			c, err := newAPIClient(f.address, f.token)
			if err != nil {
				return err
			}

			fromCID, err := c.resolveConfigID(project, env, config)
			if err != nil {
				return err
			}

			selections := make([]promoteSelection, 0, len(keys))
			for _, k := range keys {
				selections = append(selections, promoteSelection{Key: k, Action: "set"})
			}

			body := map[string]any{
				"from_config":    fromCID,
				"to_env":         toEnv,
				"create":         createTarget,
				"source_version": sourceVersion,
				"note":           note,
				"selections":     selections,
			}
			if toName != "" {
				body["to_name"] = toName
			}

			var res struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}
			if err := c.call("POST", "/v1/promote/requests", body, &res); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "requested promotion %s (%s)\n", res.ID, res.Status)
			return nil
		},
	}
	f.bind(cmd)
	cmd.Flags().StringVar(&toEnv, "to", "", "target environment slug (required)")
	cmd.Flags().StringArrayVar(&keys, "key", nil, "request a specific key (repeatable)")
	cmd.Flags().BoolVar(&all, "all", false, "reserved: requires explicit --key selection in v1")
	cmd.Flags().StringVar(&note, "note", "", "note to attach to the request")
	cmd.Flags().BoolVar(&createTarget, "create-target", false, "create the target config if it does not exist")
	cmd.Flags().StringVar(&toName, "to-name", "", "target config name (with --create-target, if different)")
	cmd.Flags().IntVar(&sourceVersion, "source-version", 0, "source config version to promote from")
	return cmd
}

// newPromoteRequestsListCmd lists pending/decided promotion requests.
func newPromoteRequestsListCmd() *cobra.Command {
	var address, token, project, status string
	var mine bool

	cmd := &cobra.Command{
		Use:   "requests",
		Short: "List promotion requests",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if project == "" {
				return fmt.Errorf("--project is required")
			}
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			q := url.Values{}
			q.Set("project", project)
			if status != "" {
				q.Set("status", status)
			}
			if mine {
				q.Set("mine", "true")
			}
			var resp struct {
				Requests []struct {
					ID          string   `json:"id"`
					Status      string   `json:"status"`
					TargetEnvID string   `json:"target_env_id"`
					TargetName  string   `json:"target_name"`
					Keys        []string `json:"keys"`
					Note        string   `json:"note"`
					RequestedBy string   `json:"requested_by"`
					CreatedAt   string   `json:"created_at"`
				} `json:"requests"`
			}
			if err := c.call("GET", "/v1/promote/requests?"+q.Encode(), nil, &resp); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tSTATUS\tTARGET\tKEYS\tREQUESTED_BY\tCREATED_AT")
			for _, r := range resp.Requests {
				target := r.TargetEnvID
				if r.TargetName != "" {
					target = r.TargetEnvID + "/" + r.TargetName
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n",
					r.ID, r.Status, target, len(r.Keys), r.RequestedBy, r.CreatedAt)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&address, "address", "", "server address")
	cmd.Flags().StringVar(&token, "token", "", "service token")
	cmd.Flags().StringVar(&project, "project", "", "project slug (required)")
	cmd.Flags().StringVar(&status, "status", "", "filter by status (pending|approved|rejected|cancelled)")
	cmd.Flags().BoolVar(&mine, "mine", false, "only show requests I created")
	return cmd
}

// newPromoteRequestApproveCmd approves a pending request and applies it.
func newPromoteRequestApproveCmd() *cobra.Command {
	var address, token string
	cmd := &cobra.Command{
		Use:   "approve <id>",
		Short: "Approve and apply a pending promotion request",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var res promoteRequestApplyResult
			if err := c.call("POST", "/v1/promote/requests/"+args[0]+"/approve", nil, &res); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"approved %s: applied=%v skipped=%v (target v%d)\n",
				args[0], res.Applied, res.Skipped, res.TargetVersion)
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", "", "server address")
	cmd.Flags().StringVar(&token, "token", "", "service token")
	return cmd
}

// newPromoteRequestRejectCmd rejects a pending request with a note.
func newPromoteRequestRejectCmd() *cobra.Command {
	var address, token, note string
	var yes bool
	cmd := &cobra.Command{
		Use:   "reject <id>",
		Short: "Reject a pending promotion request",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes && isTerminalCmd(cmd) {
				ok, err := promptLine(cmd, fmt.Sprintf("Reject promotion request %q? [y/N]: ", args[0]))
				if err != nil {
					return err
				}
				if ok != "y" && ok != "Y" {
					return nil
				}
			}
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var res struct {
				Status string `json:"status"`
			}
			if err := c.call("POST", "/v1/promote/requests/"+args[0]+"/reject",
				map[string]any{"note": note}, &res); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", args[0], res.Status)
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", "", "server address")
	cmd.Flags().StringVar(&token, "token", "", "service token")
	cmd.Flags().StringVar(&note, "note", "", "reason for rejection")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

// newPromoteRequestCancelCmd cancels a pending request (by its requester).
func newPromoteRequestCancelCmd() *cobra.Command {
	var address, token string
	var yes bool
	cmd := &cobra.Command{
		Use:   "cancel <id>",
		Short: "Cancel a pending promotion request",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes && isTerminalCmd(cmd) {
				ok, err := promptLine(cmd, fmt.Sprintf("Cancel promotion request %q? [y/N]: ", args[0]))
				if err != nil {
					return err
				}
				if ok != "y" && ok != "Y" {
					return nil
				}
			}
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var res struct {
				Status string `json:"status"`
			}
			if err := c.call("POST", "/v1/promote/requests/"+args[0]+"/cancel", nil, &res); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", args[0], res.Status)
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", "", "server address")
	cmd.Flags().StringVar(&token, "token", "", "service token")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}
