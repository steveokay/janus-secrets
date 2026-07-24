package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// secretChange is the CLI-side change payload (mirrors the API's secretChangeBody).
type secretChange struct {
	Key    string `json:"key"`
	Value  string `json:"value,omitempty"`
	Delete bool   `json:"delete,omitempty"`
	Type   string `json:"type,omitempty"`
}

// secretWriteResponse covers both outcomes of a batch write: a committed
// version (Version set) or, for a PROTECTED config (require_approval), a pending
// edit request (EditRequestID set, 202 Accepted) awaiting four-eyes approval.
type secretWriteResponse struct {
	Version       int    `json:"version"`
	EditRequestID string `json:"edit_request_id"`
	Status        string `json:"status"`
}

// printSecretWriteResult reports the outcome. On a protected config the changes
// are NOT committed — they became a pending edit request that a different
// reviewer must approve.
func printSecretWriteResult(cmd *cobra.Command, n int, verb string, resp secretWriteResponse) {
	if resp.EditRequestID != "" {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"Config is protected — %d change(s) submitted for approval (request %s). A different reviewer must approve before they apply.\n",
			n, resp.EditRequestID)
		return
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s %d secret(s) as v%d\n", verb, n, resp.Version)
}

// parseSetArgs turns `set` args into changes. Forms:
//
//	KEY=VALUE [KEY2=VALUE2 ...]   inline pairs (argv-visible)
//	KEY VALUE                     positional pair
//	KEY                           value read from stdin (or TTY prompt via caller)
func parseSetArgs(args []string, stdin io.Reader) ([]secretChange, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("at least one KEY is required")
	}
	// KEY=VALUE pairs (all args contain '=').
	allPairs := true
	for _, a := range args {
		if !strings.Contains(a, "=") {
			allPairs = false
			break
		}
	}
	if allPairs {
		out := make([]secretChange, 0, len(args))
		for _, a := range args {
			k, v, _ := strings.Cut(a, "=")
			if k == "" {
				return nil, fmt.Errorf("invalid pair %q", a)
			}
			out = append(out, secretChange{Key: k, Value: v})
		}
		return out, nil
	}
	// KEY VALUE positional.
	if len(args) == 2 && !strings.Contains(args[0], "=") {
		return []secretChange{{Key: args[0], Value: args[1]}}, nil
	}
	// Single KEY → read value from stdin.
	if len(args) == 1 {
		if stdin == nil {
			return nil, fmt.Errorf("no value provided for %q", args[0])
		}
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && line == "" {
			return nil, fmt.Errorf("reading value for %q: %w", args[0], err)
		}
		return []secretChange{{Key: args[0], Value: strings.TrimRight(line, "\r\n")}}, nil
	}
	return nil, fmt.Errorf("ambiguous arguments; use KEY=VALUE pairs, `KEY VALUE`, or `KEY` with a piped value")
}

func newSecretsSetCmd() *cobra.Command {
	var f secretFlags
	var message string
	var secretType string
	cmd := &cobra.Command{
		Use:   "set KEY[=VALUE] [KEY2=VALUE2 ...]",
		Short: "Set one or more secrets as a single new config version",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Single bare KEY on a TTY → hidden prompt; otherwise read piped stdin.
			var stdin io.Reader = cmd.InOrStdin()
			if len(args) == 1 && !strings.Contains(args[0], "=") && isTerminalCmd(cmd) {
				v, err := promptHidden(cmd, fmt.Sprintf("Value for %s: ", args[0]))
				if err != nil {
					return err
				}
				stdin = strings.NewReader(v + "\n")
			}
			changes, err := parseSetArgs(args, stdin)
			if err != nil {
				return err
			}
			if secretType != "" {
				for i := range changes {
					changes[i].Type = secretType
				}
			}
			c, cid, err := f.resolveCID()
			if err != nil {
				return err
			}
			req := map[string]any{"message": message, "changes": changes}
			var resp secretWriteResponse
			if err := c.call("PUT", "/v1/configs/"+cid+"/secrets", req, &resp); err != nil {
				return err
			}
			printSecretWriteResult(cmd, len(changes), "Saved", resp)
			return nil
		},
	}
	f.bind(cmd)
	cmd.Flags().StringVar(&message, "message", "", "config-version message")
	cmd.Flags().StringVar(&secretType, "type", "", "secret type: string|password|json|ssh_key|certificate|note (applies to all KEY=VALUE args in this call; empty defaults to string)")
	return cmd
}

func newSecretsDeleteCmd() *cobra.Command {
	var f secretFlags
	var yes bool
	var message string
	cmd := &cobra.Command{
		Use:   "delete KEY [KEY2 ...]",
		Short: "Delete one or more secrets (new config version with tombstones)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes && isTerminalCmd(cmd) {
				ok, err := promptLine(cmd, fmt.Sprintf("Delete %d secret(s)? [y/N]: ", len(args)))
				if err != nil {
					return err
				}
				if !strings.EqualFold(strings.TrimSpace(ok), "y") {
					return fmt.Errorf("aborted")
				}
			}
			changes := make([]secretChange, 0, len(args))
			for _, k := range args {
				changes = append(changes, secretChange{Key: k, Delete: true})
			}
			c, cid, err := f.resolveCID()
			if err != nil {
				return err
			}
			req := map[string]any{"message": message, "changes": changes}
			var resp secretWriteResponse
			if err := c.call("PUT", "/v1/configs/"+cid+"/secrets", req, &resp); err != nil {
				return err
			}
			printSecretWriteResult(cmd, len(changes), "Deleted", resp)
			return nil
		},
	}
	f.bind(cmd)
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	cmd.Flags().StringVar(&message, "message", "", "config-version message")
	return cmd
}

// isTerminal reports whether f is an interactive terminal.
func isTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

// isTerminalCmd reports whether the command's stdin is an interactive terminal.
func isTerminalCmd(cmd *cobra.Command) bool {
	if f, ok := cmd.InOrStdin().(*os.File); ok {
		return isTerminal(f)
	}
	return false
}
