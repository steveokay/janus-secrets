// Command janus is the Janus server and its operator CLI.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "dev"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "janus",
		Short:         "Janus — self-hosted secrets manager",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newServerCmd(),
		newMigrateCmd(),
		newInitCmd(),
		newUnsealCmd(),
		newSealStatusCmd(),
		newSealCmd(),
		newVersionCmd(),
	)
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the janus version",
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), "janus", version)
		},
	}
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "janus:", err)
		os.Exit(1)
	}
}
