package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newSetupCmd() *cobra.Command {
	var address, token, project, env, config string
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Bind this directory to a project/environment/config (writes .janus.yaml)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			if project == "" {
				if project, err = promptLine(cmd, "Project slug: "); err != nil {
					return err
				}
			}
			if env == "" {
				if env, err = promptLine(cmd, "Environment slug: "); err != nil {
					return err
				}
			}
			if config == "" {
				if config, err = promptLine(cmd, "Config name: "); err != nil {
					return err
				}
			}
			// Validate by resolving to a config id before writing anything.
			if _, err := c.resolveConfigID(project, env, config); err != nil {
				return err
			}
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			if err := writeBinding(dir, &bindingFile{Project: project, Environment: env, Config: config}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Bound %s to %s/%s/%s\n", dir, project, env, config)
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", "", "server address")
	cmd.Flags().StringVar(&token, "token", "", "service token (overrides stored session)")
	cmd.Flags().StringVar(&project, "project", "", "project slug")
	cmd.Flags().StringVar(&env, "env", "", "environment slug")
	cmd.Flags().StringVar(&config, "config", "", "config name")
	return cmd
}
