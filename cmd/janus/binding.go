package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// bindingFile is the committed .janus.yaml mapping a directory to a config (slugs;
// config is matched by name). No secret values — safe to commit.
type bindingFile struct {
	Project     string `yaml:"project"`
	Environment string `yaml:"environment"`
	Config      string `yaml:"config"`
}

func bindingPath(dir string) string { return filepath.Join(dir, ".janus.yaml") }

// readBinding returns the parsed .janus.yaml, or nil if the file is absent.
func readBinding(dir string) (*bindingFile, error) {
	b, err := os.ReadFile(bindingPath(dir)) // #nosec G304 -- fixed filename in the given dir
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var bf bindingFile
	if err := yaml.Unmarshal(b, &bf); err != nil {
		return nil, err
	}
	return &bf, nil
}

func writeBinding(dir string, b *bindingFile) error {
	out, err := yaml.Marshal(b)
	if err != nil {
		return err
	}
	return os.WriteFile(bindingPath(dir), out, 0o644) // #nosec G306 -- non-secret binding, meant to be committed
}

// resolveBinding applies, per field: flag > JANUS_* env > .janus.yaml. All three
// fields must resolve to non-empty or it errors (pointing at `janus setup`).
func resolveBinding(dir, flagProject, flagEnv, flagConfig string) (project, env, config string, err error) {
	bf, err := readBinding(dir)
	if err != nil {
		return "", "", "", err
	}
	pick := func(flag, envName, fromFile string) string {
		if flag != "" {
			return flag
		}
		if v := os.Getenv(envName); v != "" {
			return v
		}
		return fromFile
	}
	fp, fe, fc := "", "", ""
	if bf != nil {
		fp, fe, fc = bf.Project, bf.Environment, bf.Config
	}
	project = pick(flagProject, "JANUS_PROJECT", fp)
	env = pick(flagEnv, "JANUS_ENV", fe)
	config = pick(flagConfig, "JANUS_CONFIG", fc)
	if project == "" || env == "" || config == "" {
		return "", "", "", fmt.Errorf("no project/environment/config configured — run `janus setup` or pass --project/--env/--config")
	}
	return project, env, config, nil
}
