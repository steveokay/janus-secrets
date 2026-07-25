package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newRunCmd() *cobra.Command {
	var f secretFlags
	var preserveEnv bool
	var raw bool
	var watch bool
	var watchInterval string
	cmd := &cobra.Command{
		Use:   "run [flags] -- command [args...]",
		Short: "Run a command with the config's secrets injected as env vars",
		Long: "Run a command with the bound config's secrets injected as environment\n" +
			"variables. With --watch, janus polls the config's current version and\n" +
			"gracefully restarts the child (SIGTERM, grace, then Kill; Kill on Windows)\n" +
			"with a fresh environment whenever a new config version is saved.",
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

			// envForRun fetches the current secrets and builds the child env,
			// reporting any non-injectable (filename-style) keys to stderr.
			envForRun := func() ([]string, error) {
				secrets, err := fetchSecrets(c, cid, raw)
				if err != nil {
					return nil, err
				}
				env, skipped := buildChildEnv(os.Environ(), secrets, preserveEnv)
				if len(skipped) > 0 {
					fmt.Fprintf(cmd.ErrOrStderr(), "janus: skipped %d secret(s) not usable as env vars: %s\n",
						len(skipped), strings.Join(skipped, ", "))
				}
				return env, nil
			}

			env, err := envForRun()
			if err != nil {
				return err
			}

			if !watch {
				return execChild(cmdArgs[0], cmdArgs[1:], env)
			}

			interval, err := parseWatchInterval(watchInterval)
			if err != nil {
				return err
			}
			sup := &runSupervisor{
				name:     cmdArgs[0],
				args:     cmdArgs[1:],
				envFor:   envForRun,
				newCmd:   realCommandFactory,
				grace:    5 * time.Second,
				errOut:   cmd.ErrOrStderr(),
				stdin:    os.Stdin,
				stdout:   os.Stdout,
				stderr:   os.Stderr,
				vf:       c,
				cid:      cid,
				tickerFn: newRealTicker,
			}
			return sup.run(interval, env)
		},
	}
	f.bind(cmd)
	cmd.Flags().BoolVar(&preserveEnv, "preserve-env", false, "existing env vars win over secrets")
	cmd.Flags().BoolVar(&raw, "raw", false, "inject stored values verbatim (do not resolve references) — mainly for debugging")
	cmd.Flags().BoolVar(&watch, "watch", false, "poll the config version and restart the child when it changes")
	cmd.Flags().StringVar(&watchInterval, "watch-interval", "10s", "how often to poll for a new config version (with --watch)")
	return cmd
}

// fetchSecrets pulls the bound config's revealed secrets. With raw, stored
// values are returned verbatim (references unresolved).
func fetchSecrets(c *apiClient, cid string, raw bool) (map[string]string, error) {
	var resp struct {
		Secrets map[string]string `json:"secrets"`
	}
	path := "/v1/configs/" + cid + "/secrets?reveal=true"
	if raw {
		path += "&raw=true"
	}
	if err := c.call("GET", path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Secrets, nil
}

// execChild runs name+args with env, wiring std streams, forwarding signals, and
// propagating the child's exit code as this process's exit code. Used by the
// non-watch (single-shot) path.
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

// childProc is the subset of a running child process the supervisor drives. A
// command factory returns one; tests supply a trivial in-process fake so the
// restart logic is exercised without spawning real programs.
type childProc interface {
	Start() error
	Wait() error
	// Terminate requests a graceful stop then, after grace, forces a kill. It
	// returns once the process has exited (or the kill has been issued).
	Terminate(grace time.Duration)
}

// commandFactory builds a childProc for name+args with env + wired streams.
type commandFactory func(name string, args, env []string, stdin io.Reader, stdout, stderr io.Writer) childProc

// realCommandFactory wraps os/exec.Cmd as a childProc.
func realCommandFactory(name string, args, env []string, stdin io.Reader, stdout, stderr io.Writer) childProc {
	c := exec.Command(name, args...) // #nosec G204 -- the user is explicitly running their own command
	c.Env = env
	c.Stdin, c.Stdout, c.Stderr = stdin, stdout, stderr
	return &execChildProc{cmd: c, done: make(chan struct{})}
}

type execChildProc struct {
	cmd  *exec.Cmd
	done chan struct{}
}

func (p *execChildProc) Start() error { return p.cmd.Start() }

func (p *execChildProc) Wait() error {
	err := p.cmd.Wait()
	close(p.done)
	return err
}

// Terminate sends a graceful stop (SIGTERM on POSIX; Kill on Windows), waits up
// to grace for exit, then hard-kills. It never blocks past grace.
func (p *execChildProc) Terminate(grace time.Duration) {
	if p.cmd.Process == nil {
		return
	}
	_ = terminateGracefully(p.cmd.Process)
	select {
	case <-p.done:
		return
	case <-time.After(grace):
		_ = p.cmd.Process.Kill()
		<-p.done
	}
}

// runSupervisor owns the watch-mode lifecycle: start the child, poll the config
// version on a ticker, and on an increase gracefully restart with fresh env.
type runSupervisor struct {
	name string
	args []string

	envFor func() ([]string, error)
	newCmd commandFactory
	grace  time.Duration

	errOut         io.Writer
	stdin          io.Reader
	stdout, stderr io.Writer

	vf       versionFetcher
	cid      string
	tickerFn func(time.Duration) ticker
}

// run starts the child with initEnv, records the current version as baseline,
// and loops: on each tick, if the version increased, restart the child with a
// freshly-fetched env. It returns when the child exits on its own (not due to a
// restart). A poll error is logged and retried on the next tick (non-fatal).
func (s *runSupervisor) run(interval time.Duration, initEnv []string) error {
	baseline, err := s.vf.currentVersion(s.cid)
	if err != nil {
		// Baseline unknown: still run the child; the next successful poll
		// becomes authoritative once the server is reachable.
		fmt.Fprintf(s.errOut, "janus: watch: could not read initial config version: %v\n", err)
	}

	child := s.newCmd(s.name, s.args, initEnv, s.stdin, s.stdout, s.stderr)
	if err := child.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- child.Wait() }()

	tk := s.tickerFn(interval)
	defer tk.Stop()

	for {
		select {
		case werr := <-exited:
			// Child exited on its own; propagate its exit code like single-shot.
			if werr != nil {
				var ee *exec.ExitError
				if errors.As(werr, &ee) {
					os.Exit(ee.ExitCode())
				}
				return werr
			}
			return nil
		case <-tk.Chan():
			observed, changed, perr := pollOnce(s.vf, s.cid, baseline)
			if perr != nil {
				fmt.Fprintf(s.errOut, "janus: watch: poll failed: %v\n", perr)
				continue
			}
			if !changed {
				baseline = observed
				continue
			}
			fmt.Fprintf(s.errOut, "janus: watch: config version %d → %d, restarting\n", baseline, observed)
			env, ferr := s.envFor()
			if ferr != nil {
				fmt.Fprintf(s.errOut, "janus: watch: refetch failed, keeping current child: %v\n", ferr)
				continue
			}
			child.Terminate(s.grace)
			<-exited // drain the previous child's Wait result

			baseline = observed
			child = s.newCmd(s.name, s.args, env, s.stdin, s.stdout, s.stderr)
			if err := child.Start(); err != nil {
				return err
			}
			nextExited := make(chan error, 1)
			go func() { nextExited <- child.Wait() }()
			exited = nextExited
		}
	}
}
