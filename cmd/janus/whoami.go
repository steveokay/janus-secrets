package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func newWhoamiCmd() *cobra.Command {
	var address, token string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show the authenticated principal",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var me struct{ Kind, ID, Name string }
			if err := c.call("GET", "/v1/auth/me", nil, &me); err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(me)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", me.Kind, me.Name, me.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", "", "server address")
	cmd.Flags().StringVar(&token, "token", "", "service token")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}
