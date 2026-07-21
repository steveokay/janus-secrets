// Command janus is the Janus server and its operator CLI.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/steveokay/janus-secrets/internal/version"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "janus",
		Short:         "Janus — self-hosted secrets manager",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	// Cobra's cmd.Print* fall back to stderr unless an output is set. Command
	// results (init shares, seal-status) must go to stdout so pipelines like
	// scripts/dev-unseal.sh can capture them.
	root.SetOut(os.Stdout)
	root.SetErr(os.Stderr)
	root.AddCommand(
		newServerCmd(),
		newMigrateCmd(),
		newInitCmd(),
		newUnsealCmd(),
		newSealStatusCmd(),
		newSealCmd(),
		newBackupCmd(),
		newRestoreCmd(),
		newLoginCmd(),
		newLogoutCmd(),
		newSetupCmd(),
		newSecretsCmd(),
		newRunCmd(),
		newProjectCmd(),
		newEnvCmd(),
		newConfigCmd(),
		newTokenCmd(),
		newMasterKeyCmd(),
		newPromoteCmd(),
		newPipelineCmd(),
		newRotationCmd(),
		newSyncCmd(),
		newDynamicCmd(),
		newNotificationsCmd(),
		newVersionCmd(),
		newWhoamiCmd(),
		newSessionCmd(),
		newCompletionCmd(),
	)
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the janus version",
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), "janus", version.String())
		},
	}
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "janus:", err)
		os.Exit(1)
	}
}
