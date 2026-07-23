package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// parseMaxAge parses an advisory max-age duration. It accepts any Go duration
// (e.g. "2160h", "90m") plus a convenience day suffix "<n>d" and week suffix
// "<n>w" (Go's time.ParseDuration has no unit larger than hours). Returns whole
// seconds; the value must be strictly positive.
func parseMaxAge(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("duration is required")
	}
	var d time.Duration
	switch {
	case strings.HasSuffix(s, "d"):
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid days duration %q", s)
		}
		d = time.Duration(n * float64(24*time.Hour))
	case strings.HasSuffix(s, "w"):
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, "w"), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid weeks duration %q", s)
		}
		d = time.Duration(n * float64(7*24*time.Hour))
	default:
		var err error
		d, err = time.ParseDuration(s)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q (use Go durations like 2160h, or a day/week suffix like 90d, 2w)", s)
		}
	}
	secs := int64(d / time.Second)
	if secs <= 0 {
		return 0, fmt.Errorf("duration must be positive")
	}
	return secs, nil
}

// humanizeSeconds renders whole seconds as an approximate day/hour string.
func humanizeSeconds(secs int64) string {
	d := time.Duration(secs) * time.Second
	if secs%(24*3600) == 0 {
		return fmt.Sprintf("%dd", secs/(24*3600))
	}
	return d.String()
}

// newSecretsMaxAgeCmd groups the advisory max-age policy commands. Without --key
// the commands act on the config-level default; with --key on a per-key override.
func newSecretsMaxAgeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "max-age",
		Short: "Manage the advisory secret max-age / expiry policy (never blocks anything)",
	}
	cmd.AddCommand(newMaxAgeSetCmd(), newMaxAgeGetCmd(), newMaxAgeClearCmd())
	return cmd
}

func newMaxAgeSetCmd() *cobra.Command {
	var f secretFlags
	var key string
	cmd := &cobra.Command{
		Use:   "set DURATION",
		Short: "Set the config default (or --key KEY override) max-age (e.g. 90d, 2160h)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			secs, err := parseMaxAge(args[0])
			if err != nil {
				return err
			}
			c, cid, err := f.resolveCID()
			if err != nil {
				return err
			}
			path := "/v1/configs/" + cid + "/max-age"
			if key != "" {
				path = "/v1/configs/" + cid + "/secrets/" + url.PathEscape(key) + "/max-age"
			}
			if err := c.call("PUT", path, map[string]any{"max_age_seconds": secs}, nil); err != nil {
				return err
			}
			target := "config default"
			if key != "" {
				target = key
			}
			fmt.Fprintf(cmd.OutOrStdout(), "max-age for %s set to %s\n", target, humanizeSeconds(secs))
			return nil
		},
	}
	f.bind(cmd)
	cmd.Flags().StringVar(&key, "key", "", "set a per-key override instead of the config default")
	return cmd
}

func newMaxAgeGetCmd() *cobra.Command {
	var f secretFlags
	cmd := &cobra.Command{
		Use:   "get",
		Short: "List the config's max-age policies (default under key \"\")",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, cid, err := f.resolveCID()
			if err != nil {
				return err
			}
			var resp struct {
				Policies []struct {
					Key           string `json:"key"`
					MaxAgeSeconds int64  `json:"max_age_seconds"`
				} `json:"policies"`
			}
			if err := c.call("GET", "/v1/configs/"+cid+"/max-age", nil, &resp); err != nil {
				return err
			}
			if len(resp.Policies) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no max-age policy configured")
				return nil
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "KEY\tMAX-AGE")
			for _, p := range resp.Policies {
				name := p.Key
				if name == "" {
					name = "(config default)"
				}
				fmt.Fprintf(tw, "%s\t%s\n", name, humanizeSeconds(p.MaxAgeSeconds))
			}
			return tw.Flush()
		},
	}
	f.bind(cmd)
	return cmd
}

func newMaxAgeClearCmd() *cobra.Command {
	var f secretFlags
	var key string
	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Clear the config default (or --key KEY override) max-age",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, cid, err := f.resolveCID()
			if err != nil {
				return err
			}
			path := "/v1/configs/" + cid + "/max-age"
			if key != "" {
				path = "/v1/configs/" + cid + "/secrets/" + url.PathEscape(key) + "/max-age"
			}
			// A null body clears the policy.
			if err := c.call("PUT", path, map[string]any{"max_age_seconds": nil}, nil); err != nil {
				return err
			}
			target := "config default"
			if key != "" {
				target = key
			}
			fmt.Fprintf(cmd.OutOrStdout(), "max-age for %s cleared\n", target)
			return nil
		},
	}
	f.bind(cmd)
	cmd.Flags().StringVar(&key, "key", "", "clear a per-key override instead of the config default")
	return cmd
}
