package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"

	"github.com/spf13/cobra"
)

func newRunCmd() *cobra.Command {
	var f secretFlags
	var preserveEnv bool
	var raw bool
	cmd := &cobra.Command{
		Use:                "run [flags] -- command [args...]",
		Short:              "Run a command with the config's secrets injected as env vars",
		DisableFlagParsing: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			dash := cmd.ArgsLenAtDash()
			if dash < 0 || dash >= len(args) {
				return fmt.Errorf("no command given; usage: janus run [flags] -- <command> [args...]")
			}
			cmdArgs := args[dash:]

			c, cid, err := f.resolveCID()
			if err != nil {
				return err
			}
			var resp struct {
				Secrets map[string]string `json:"secrets"`
			}
			path := "/v1/configs/" + cid + "/secrets?reveal=true"
			if raw {
				path += "&raw=true"
			}
			if err := c.call("GET", path, nil, &resp); err != nil {
				return err
			}
			env, skipped := buildChildEnv(os.Environ(), resp.Secrets, preserveEnv)
			if len(skipped) > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "janus: skipped %d secret(s) not usable as env vars: %s\n",
					len(skipped), strings.Join(skipped, ", "))
			}
			return execChild(cmdArgs[0], cmdArgs[1:], env)
		},
	}
	f.bind(cmd)
	cmd.Flags().BoolVar(&preserveEnv, "preserve-env", false, "existing env vars win over secrets")
	cmd.Flags().BoolVar(&raw, "raw", false, "inject stored values verbatim (do not resolve references) — mainly for debugging")
	return cmd
}

// execChild runs name+args with env, wiring std streams, forwarding signals, and
// propagating the child's exit code as this process's exit code.
func execChild(name string, args, env []string) error {
	child := exec.Command(name, args...) // #nosec G204 -- the user is explicitly running their own command
	child.Env = env
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh)
	defer signal.Stop(sigCh)

	if err := child.Start(); err != nil {
		return err
	}
	go func() {
		for s := range sigCh {
			_ = child.Process.Signal(s)
		}
	}()
	err := child.Wait()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			os.Exit(ee.ExitCode()) // propagate the child's code verbatim
		}
		return err
	}
	return nil
}
