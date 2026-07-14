package main

import (
	"fmt"
	"net/url"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// promoteDiff mirrors the /v1/promote/preview response.
type promoteDiff struct {
	SourceVersion int  `json:"source_version"`
	TargetExists  bool `json:"target_exists"`
	Entries       []struct {
		Key         string `json:"key"`
		Status      string `json:"status"` // add|change|remove|same
		SourceValue string `json:"source_value"`
		TargetValue string `json:"target_value"`
		Locked      bool   `json:"locked"`
	} `json:"entries"`
}

// promoteSelection is one {key,action} entry of a promote apply request.
type promoteSelection struct {
	Key    string `json:"key"`
	Action string `json:"action"` // set|remove
}

// newPromoteCmd previews or applies a promotion of the bound config's secrets
// into the same config name in another environment. Value-free: neither the
// dry-run table nor the apply summary prints secret values.
func newPromoteCmd() *cobra.Command {
	var f secretFlags
	var toEnv string
	var keys []string
	var all, includeRemoves, createTarget, dryRun bool

	cmd := &cobra.Command{
		Use:   "promote --to <env>",
		Short: "Promote the bound config's secrets to another environment",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if toEnv == "" {
				return fmt.Errorf("--to <env-slug> is required")
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

			// Resolve the target config. If it doesn't exist and --create-target is
			// set, resolve the target env id for the create path instead.
			var toCID, toEnvID string
			toCID, err = c.resolveConfigID(project, toEnv, config)
			if err != nil {
				if !createTarget {
					return err
				}
				pid, perr := c.resolveProjectID(project)
				if perr != nil {
					return perr
				}
				envMap, _, eerr := c.listEnvs(pid)
				if eerr != nil {
					return eerr
				}
				id, ok := envMap[toEnv]
				if !ok {
					return fmt.Errorf("environment %q not found in project %q", toEnv, project)
				}
				toEnvID = id
			}

			// Preview.
			q := url.Values{}
			q.Set("from", fromCID)
			q.Set("to", toCID)
			var diff promoteDiff
			if err := c.call("GET", "/v1/promote/preview?"+q.Encode(), nil, &diff); err != nil {
				return err
			}

			if dryRun {
				return printPromoteDiff(cmd, &diff)
			}

			if all && len(keys) > 0 {
				return fmt.Errorf("--all and --key are mutually exclusive")
			}
			if !all && len(keys) == 0 {
				return fmt.Errorf("select keys with --key <K> (repeatable) or --all")
			}

			// Index the preview by key for selection.
			type entry struct {
				status string
				locked bool
			}
			byKey := map[string]entry{}
			for _, e := range diff.Entries {
				byKey[e.Key] = entry{status: e.Status, locked: e.Locked}
			}

			var selections []promoteSelection
			if all {
				for _, e := range diff.Entries {
					if e.Locked {
						continue
					}
					switch e.Status {
					case "add", "change":
						selections = append(selections, promoteSelection{Key: e.Key, Action: "set"})
					case "remove":
						if includeRemoves {
							selections = append(selections, promoteSelection{Key: e.Key, Action: "remove"})
						}
					}
				}
			} else {
				for _, k := range keys {
					e, ok := byKey[k]
					if !ok {
						fmt.Fprintf(cmd.ErrOrStderr(), "warning: key %q not present in preview; skipping\n", k)
						continue
					}
					if e.locked {
						fmt.Fprintf(cmd.ErrOrStderr(), "warning: key %q is locked; skipping\n", k)
						continue
					}
					action := "set"
					if e.status == "remove" {
						action = "remove"
					}
					selections = append(selections, promoteSelection{Key: k, Action: action})
				}
			}

			if len(selections) == 0 {
				return fmt.Errorf("no promotable keys selected")
			}

			body := map[string]any{
				"from_config":    fromCID,
				"to_config":      toCID,
				"create":         createTarget,
				"source_version": diff.SourceVersion,
				"selections":     selections,
			}
			if createTarget && toCID == "" {
				body["to_env"] = toEnvID
				body["to_name"] = config
			}

			var res struct {
				TargetVersion int      `json:"target_version"`
				Applied       []string `json:"applied"`
				Skipped       []string `json:"skipped"`
			}
			if err := c.call("POST", "/v1/promote", body, &res); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"promoted %d key(s) to %s: applied=%v skipped=%v (target v%d)\n",
				len(res.Applied), toEnv, res.Applied, res.Skipped, res.TargetVersion)
			return nil
		},
	}
	f.bind(cmd)
	cmd.Flags().StringVar(&toEnv, "to", "", "target environment slug (required)")
	cmd.Flags().StringArrayVar(&keys, "key", nil, "promote a specific key (repeatable)")
	cmd.Flags().BoolVar(&all, "all", false, "promote all added/changed keys")
	cmd.Flags().BoolVar(&includeRemoves, "include-removes", false, "with --all, also propagate removed keys")
	cmd.Flags().BoolVar(&createTarget, "create-target", false, "create the target config if it does not exist")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the diff and exit without applying")
	return cmd
}

// printPromoteDiff renders KEY/STATUS/LOCKED only — never secret values.
func printPromoteDiff(cmd *cobra.Command, diff *promoteDiff) error {
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "# source version %d, target exists %t\n", diff.SourceVersion, diff.TargetExists)
	fmt.Fprintln(tw, "KEY\tSTATUS\tLOCKED")
	for _, e := range diff.Entries {
		fmt.Fprintf(tw, "%s\t%s\t%t\n", e.Key, e.Status, e.Locked)
	}
	return tw.Flush()
}

// newPipelineCmd reads/configures a project's ordered release pipeline.
func newPipelineCmd() *cobra.Command {
	var address, token string
	cmd := &cobra.Command{
		Use:   "pipeline",
		Short: "Read or configure a project's release pipeline",
	}
	cmd.PersistentFlags().StringVar(&address, "address", "", "server address (default: stored/env)")
	cmd.PersistentFlags().StringVar(&token, "token", "", "service token (default: stored/env)")

	get := &cobra.Command{
		Use:   "get <project-slug>",
		Short: "Print the pipeline's environment slugs in order",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			pid, err := c.resolveProjectID(args[0])
			if err != nil {
				return err
			}
			_, envs, err := c.listEnvs(pid)
			if err != nil {
				return err
			}
			idToSlug := map[string]string{}
			for _, e := range envs {
				idToSlug[e.ID] = e.Slug
			}
			var pl struct {
				EnvironmentIDs []string `json:"environment_ids"`
			}
			if err := c.call("GET", "/v1/projects/"+pid+"/pipeline", nil, &pl); err != nil {
				return err
			}
			if len(pl.EnvironmentIDs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no pipeline configured)")
				return nil
			}
			slugs := make([]string, 0, len(pl.EnvironmentIDs))
			for _, id := range pl.EnvironmentIDs {
				if s, ok := idToSlug[id]; ok {
					slugs = append(slugs, s)
				} else {
					slugs = append(slugs, id)
				}
			}
			fmt.Fprintln(cmd.OutOrStdout(), joinArrow(slugs))
			return nil
		},
	}

	set := &cobra.Command{
		Use:   "set <project-slug> <env-slug>...",
		Short: "Set the ordered pipeline from environment slugs",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			pid, err := c.resolveProjectID(args[0])
			if err != nil {
				return err
			}
			envMap, _, err := c.listEnvs(pid)
			if err != nil {
				return err
			}
			slugs := args[1:]
			ids := make([]string, 0, len(slugs))
			for _, s := range slugs {
				id, ok := envMap[s]
				if !ok {
					return fmt.Errorf("environment %q not found in project %q", s, args[0])
				}
				ids = append(ids, id)
			}
			if err := c.call("PUT", "/v1/projects/"+pid+"/pipeline",
				map[string]any{"environment_ids": ids}, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "pipeline set: %s\n", joinArrow(slugs))
			return nil
		},
	}

	cmd.AddCommand(get, set)
	return cmd
}

func joinArrow(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " → "
		}
		out += p
	}
	return out
}

// newSecretsLockCmd marks a key promotion-protected on the bound config.
func newSecretsLockCmd() *cobra.Command {
	var f secretFlags
	cmd := &cobra.Command{
		Use:   "lock KEY",
		Short: "Mark a key promotion-protected on the bound config",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cid, err := f.resolveCID()
			if err != nil {
				return err
			}
			if err := c.call("POST", "/v1/configs/"+cid+"/locked-keys",
				map[string]any{"key": args[0]}, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "locked %s\n", args[0])
			return nil
		},
	}
	f.bind(cmd)
	return cmd
}

// newSecretsUnlockCmd clears a key's promotion protection on the bound config.
func newSecretsUnlockCmd() *cobra.Command {
	var f secretFlags
	cmd := &cobra.Command{
		Use:   "unlock KEY",
		Short: "Clear a key's promotion protection on the bound config",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cid, err := f.resolveCID()
			if err != nil {
				return err
			}
			if err := c.call("DELETE",
				"/v1/configs/"+cid+"/locked-keys/"+url.PathEscape(args[0]), nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "unlocked %s\n", args[0])
			return nil
		},
	}
	f.bind(cmd)
	return cmd
}
