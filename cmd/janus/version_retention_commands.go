package main

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// Secret value-version retention CLI (roadmap 8.2).
//
//	janus secrets retention get|set|clear   the per-config retention override
//	janus secrets prune                     destroy old config versions
//
// Every command here is VALUE-FREE: it prints version numbers, counts and
// policy integers only — never a secret value.

// retentionResp is the value-free wire shape of GET/PUT .../versions/retention.
type retentionResp struct {
	InstanceMinVersions  int  `json:"instance_min_versions"`
	InstanceMinDays      int  `json:"instance_min_days"`
	ConfigMinVersions    *int `json:"config_min_versions"`
	ConfigMinDays        *int `json:"config_min_days"`
	EffectiveMinVersions int  `json:"effective_min_versions"`
	EffectiveMinDays     int  `json:"effective_min_days"`
}

// pruneResp is the value-free wire shape of POST .../versions/prune.
type pruneResp struct {
	DryRun           bool  `json:"dry_run"`
	LatestVersion    int   `json:"latest_version"`
	KeepVersions     int   `json:"keep_versions"`
	KeepDays         int   `json:"keep_days"`
	PrunedVersions   []int `json:"pruned_versions"`
	PinnedVersions   []int `json:"pinned_versions"`
	VersionsDeleted  int   `json:"versions_deleted"`
	ValuesDeleted    int   `json:"values_deleted"`
	VersionsRetained int   `json:"versions_retained"`
}

// newSecretsRetentionCmd groups the value-version retention policy commands.
func newSecretsRetentionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "retention",
		Short: "Inspect and set this config's secret value-version retention floor",
		Long: "Retention floors bound how much of a config's version history " +
			"`janus secrets prune` may destroy.\n\n" +
			"The effective floor is the STRICTEST of the instance-wide floor " +
			"(JANUS_SECRET_RETAIN_MIN_VERSIONS / JANUS_SECRET_RETAIN_MIN_DAYS) and " +
			"this config's override, so an override can only ever retain MORE, never less.",
	}
	cmd.AddCommand(newRetentionGetCmd(), newRetentionSetCmd(), newRetentionClearCmd())
	return cmd
}

func newRetentionGetCmd() *cobra.Command {
	var f secretFlags
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Show the resolved retention floor (instance, config override, effective)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, cid, err := f.resolveCID()
			if err != nil {
				return err
			}
			var resp retentionResp
			if err := c.call("GET", "/v1/configs/"+cid+"/versions/retention", nil, &resp); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "SCOPE\tMIN VERSIONS\tMIN DAYS")
			fmt.Fprintf(tw, "instance\t%s\t%s\n",
				retentionInt(resp.InstanceMinVersions), retentionInt(resp.InstanceMinDays))
			fmt.Fprintf(tw, "config\t%s\t%s\n",
				retentionOpt(resp.ConfigMinVersions), retentionOpt(resp.ConfigMinDays))
			fmt.Fprintf(tw, "effective\t%s\t%s\n",
				retentionInt(resp.EffectiveMinVersions), retentionInt(resp.EffectiveMinDays))
			return tw.Flush()
		},
	}
	f.bind(cmd)
	return cmd
}

// retentionInt renders an instance/effective floor value ("off" for 0).
func retentionInt(v int) string {
	if v <= 0 {
		return "off"
	}
	return fmt.Sprintf("%d", v)
}

// retentionOpt renders an optional per-config override ("-" when unset).
func retentionOpt(v *int) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *v)
}

func newRetentionSetCmd() *cobra.Command {
	var f secretFlags
	var minVersions, minDays int
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set this config's retention override (raises the floor; never lowers it)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if minVersions <= 0 && minDays <= 0 {
				return fmt.Errorf("set --min-versions and/or --min-days (each >= 1)")
			}
			c, cid, err := f.resolveCID()
			if err != nil {
				return err
			}
			body := map[string]any{"min_versions": nil, "min_days": nil}
			if minVersions > 0 {
				body["min_versions"] = minVersions
			}
			if minDays > 0 {
				body["min_days"] = minDays
			}
			var resp retentionResp
			if err := c.call("PUT", "/v1/configs/"+cid+"/versions/retention", body, &resp); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"retention override set; effective floor: %s versions, %s days\n",
				retentionInt(resp.EffectiveMinVersions), retentionInt(resp.EffectiveMinDays))
			return nil
		},
	}
	f.bind(cmd)
	cmd.Flags().IntVar(&minVersions, "min-versions", 0, "never prune below the newest N config versions")
	cmd.Flags().IntVar(&minDays, "min-days", 0, "never prune a config version younger than N days")
	return cmd
}

func newRetentionClearCmd() *cobra.Command {
	var f secretFlags
	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Clear this config's retention override (falls back to the instance floor)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, cid, err := f.resolveCID()
			if err != nil {
				return err
			}
			var resp retentionResp
			// Both fields null clears the override.
			body := map[string]any{"min_versions": nil, "min_days": nil}
			if err := c.call("PUT", "/v1/configs/"+cid+"/versions/retention", body, &resp); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"retention override cleared; effective floor: %s versions, %s days\n",
				retentionInt(resp.EffectiveMinVersions), retentionInt(resp.EffectiveMinDays))
			return nil
		},
	}
	f.bind(cmd)
	return cmd
}

// newSecretsPruneCmd destroys old config VERSIONS of the bound config.
func newSecretsPruneCmd() *cobra.Command {
	var f secretFlags
	var keepVersions, keepDays int
	var apply bool
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Destroy old config versions and their unreferenced value history (owner-only)",
		Long: "Prunes at the CONFIG-VERSION granularity — the unit of diff and rollback — " +
			"then garbage-collects secret value rows no surviving version references.\n\n" +
			"Every RETAINED config version stays fully restorable: individual value versions " +
			"are never deleted out from under an older config version.\n\n" +
			"Previews by default. Pass --apply to actually destroy; this cannot be undone.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if keepVersions <= 0 && keepDays <= 0 {
				return fmt.Errorf("set --keep-versions and/or --keep-days")
			}
			c, cid, err := f.resolveCID()
			if err != nil {
				return err
			}
			var resp pruneResp
			if err := c.call("POST", "/v1/configs/"+cid+"/versions/prune", map[string]any{
				"keep_versions": keepVersions,
				"keep_days":     keepDays,
				"dry_run":       !apply,
			}, &resp); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			verb := "would prune"
			if apply {
				verb = "pruned"
			}
			fmt.Fprintf(out, "effective floor: keep %d newest versions, keep %d days\n",
				resp.KeepVersions, resp.KeepDays)
			fmt.Fprintf(out, "%s %d config version(s) and %d unreferenced value version(s)\n",
				verb, resp.VersionsDeleted, resp.ValuesDeleted)
			if len(resp.PrunedVersions) > 0 {
				fmt.Fprintf(out, "versions: %s\n", joinInts(resp.PrunedVersions))
			}
			if len(resp.PinnedVersions) > 0 {
				fmt.Fprintf(out, "retained despite the floor (pending promotion request): %s\n",
					joinInts(resp.PinnedVersions))
			}
			fmt.Fprintf(out, "%d version(s) retained; latest is v%d\n",
				resp.VersionsRetained, resp.LatestVersion)
			if !apply {
				fmt.Fprintln(out, "(dry run — re-run with --apply to destroy)")
			}
			return nil
		},
	}
	f.bind(cmd)
	cmd.Flags().IntVar(&keepVersions, "keep-versions", 0, "retain the newest N config versions")
	cmd.Flags().IntVar(&keepDays, "keep-days", 0, "retain config versions created in the last N days")
	cmd.Flags().BoolVar(&apply, "apply", false, "actually destroy (default is a dry-run preview)")
	return cmd
}

// joinInts renders version numbers as "v1, v2, v3".
func joinInts(vs []int) string {
	parts := make([]string, 0, len(vs))
	for _, v := range vs {
		parts = append(parts, fmt.Sprintf("v%d", v))
	}
	return strings.Join(parts, ", ")
}
