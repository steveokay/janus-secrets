package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"text/template"
	"time"

	"github.com/spf13/cobra"
)

func newRenderCmd() *cobra.Command {
	var f secretFlags
	var templatePath, outPath string
	var raw bool
	var watch bool
	var watchInterval string
	cmd := &cobra.Command{
		Use:   "render --template <file> --out <file>",
		Short: "Render a text/template with the config's secrets into a file",
		Long: "Render a Go text/template using the bound config's secrets and write the\n" +
			"result to --out (0600, atomic).\n\n" +
			"Secrets are available BOTH as a top-level map ({{ .KEY }} / {{ index . \"KEY\" }})\n" +
			"and via a `secret \"KEY\"` function. Referencing a missing key is an error.\n\n" +
			"WARNING: the rendered --out file contains plaintext secret VALUES by design\n" +
			"(this is the command's purpose, analogous to `download --plain`). Protect and\n" +
			"clean up the output accordingly.\n\n" +
			"With --watch, janus re-renders whenever a new config version is saved.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if templatePath == "" || outPath == "" {
				return fmt.Errorf("both --template and --out are required")
			}
			tmplSrc, err := os.ReadFile(templatePath) // #nosec G304 -- operator-supplied template path, read intentionally
			if err != nil {
				return fmt.Errorf("read template: %w", err)
			}

			c, cid, err := f.resolveCID()
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.ErrOrStderr(),
				"janus: note: %s will contain plaintext secret values\n", outPath)

			renderOnce := func() error {
				secrets, err := fetchSecrets(c, cid, raw)
				if err != nil {
					return err
				}
				rendered, err := renderTemplate(templatePath, string(tmplSrc), secrets)
				if err != nil {
					return err
				}
				if err := writeSecretFile(outPath, rendered); err != nil {
					return err
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "janus: rendered %d secret(s) → %s\n", len(secrets), outPath)
				return nil
			}

			if err := renderOnce(); err != nil {
				return err
			}
			if !watch {
				return nil
			}

			interval, err := parseWatchInterval(watchInterval)
			if err != nil {
				return err
			}
			return renderWatch(c, cid, interval, newRealTicker, renderOnce, cmd.ErrOrStderr())
		},
	}
	f.bind(cmd)
	cmd.Flags().StringVar(&templatePath, "template", "", "path to a Go text/template file (required)")
	cmd.Flags().StringVar(&outPath, "out", "", "path to write the rendered output, 0600 (required)")
	cmd.Flags().BoolVar(&raw, "raw", false, "use stored values verbatim (do not resolve references)")
	cmd.Flags().BoolVar(&watch, "watch", false, "re-render when the config version changes")
	cmd.Flags().StringVar(&watchInterval, "interval", "10s", "how often to poll for a new config version (with --watch)")
	return cmd
}

// renderTemplate parses and executes src as a text/template with secrets exposed
// both as the top-level dot map and via a `secret "KEY"` function. Referencing a
// missing key errors (Option missingkey=error). name is used only for error
// messages. The returned bytes may contain plaintext secret values by design.
func renderTemplate(name, src string, secrets map[string]string) ([]byte, error) {
	funcs := template.FuncMap{
		"secret": func(key string) (string, error) {
			v, ok := secrets[key]
			if !ok {
				return "", fmt.Errorf("secret %q not found in config", key)
			}
			return v, nil
		},
	}
	t, err := template.New(name).Option("missingkey=error").Funcs(funcs).Parse(src)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, secrets); err != nil {
		return nil, fmt.Errorf("render template: %w", err)
	}
	return buf.Bytes(), nil
}

// renderWatch polls the config version on a ticker and calls render on each
// increase. Poll/render errors are logged and retried on the next tick; the loop
// runs until the process is interrupted. It reuses the same poll helper as
// `run --watch`.
func renderWatch(vf versionFetcher, cid string, interval time.Duration, tickerFn func(time.Duration) ticker, render func() error, errOut io.Writer) error {
	baseline, err := vf.currentVersion(cid)
	if err != nil {
		fmt.Fprintf(errOut, "janus: watch: could not read initial config version: %v\n", err)
	}
	tk := tickerFn(interval)
	defer tk.Stop()
	for range tk.Chan() {
		observed, changed, perr := pollOnce(vf, cid, baseline)
		if perr != nil {
			fmt.Fprintf(errOut, "janus: watch: poll failed: %v\n", perr)
			continue
		}
		if !changed {
			baseline = observed
			continue
		}
		fmt.Fprintf(errOut, "janus: watch: config version %d → %d, re-rendering\n", baseline, observed)
		if rerr := render(); rerr != nil {
			fmt.Fprintf(errOut, "janus: watch: re-render failed: %v\n", rerr)
			continue
		}
		baseline = observed
	}
	return nil
}
