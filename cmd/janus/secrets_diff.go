package main

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
)

func newSecretsDiffCmd() *cobra.Command {
	var f secretFlags
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "diff <vA> <vB>",
		Short: "Diff two config versions (key names only, no values)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("vA must be an integer version")
			}
			b, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("vB must be an integer version")
			}
			c, cid, err := f.resolveCID()
			if err != nil {
				return err
			}
			var d struct {
				A, B                    int
				Added, Changed, Removed []string
			}
			path := fmt.Sprintf("/v1/configs/%s/versions/diff?a=%d&b=%d", cid, a, b)
			if err := c.call("GET", path, nil, &d); err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(d)
			}
			w := cmd.OutOrStdout()
			for _, k := range d.Added {
				fmt.Fprintf(w, "+ %s\n", k)
			}
			for _, k := range d.Removed {
				fmt.Fprintf(w, "- %s\n", k)
			}
			for _, k := range d.Changed {
				fmt.Fprintf(w, "~ %s\n", k)
			}
			return nil
		},
	}
	f.bind(cmd)
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}
