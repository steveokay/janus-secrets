package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// authState is the persisted CLI credential file (~/.config/janus/auth.json, 0600).
type authState struct {
	Address string `json:"address,omitempty"`
	Session string `json:"session,omitempty"`
	Email   string `json:"email,omitempty"`
}

// credential is the resolved per-request credential: exactly one of Bearer/Cookie set.
type credential struct {
	Bearer string
	Cookie string
}

const defaultAddr = "http://127.0.0.1:8200"

// configDir is os.UserConfigDir()/janus (~/.config/janus on linux honoring
// XDG_CONFIG_HOME, %AppData%\janus on Windows, ~/Library/Application Support/janus
// on macOS).
func configDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "janus"), nil
}

func authPath() (string, error) {
	d, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "auth.json"), nil
}

// loadAuth reads auth.json; a missing file is not an error (returns a zero state).
func loadAuth() (*authState, error) {
	p, err := authPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p) // #nosec G304 -- path is the fixed config dir, not user input
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &authState{}, nil
		}
		return nil, err
	}
	var st authState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// saveAuth writes auth.json atomically with dir 0700 / file 0600.
func saveAuth(st *authState) error {
	d, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(d, "auth.json")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// clearSession drops the stored session (keeps address/email), for logout.
func clearSession() error {
	st, err := loadAuth()
	if err != nil {
		return err
	}
	st.Session = ""
	return saveAuth(st)
}

// resolveAddress applies: flag > JANUS_ADDR > auth.json > default.
func resolveAddress(flagAddr string) string {
	if flagAddr != "" {
		return flagAddr
	}
	if v := os.Getenv("JANUS_ADDR"); v != "" {
		return v
	}
	if st, err := loadAuth(); err == nil && st.Address != "" {
		return st.Address
	}
	return defaultAddr
}

// resolveCredential applies: --token > JANUS_TOKEN (both Bearer) > stored session (Cookie).
func resolveCredential(flagToken string) (credential, error) {
	if flagToken != "" {
		return credential{Bearer: flagToken}, nil
	}
	if v := os.Getenv("JANUS_TOKEN"); v != "" {
		return credential{Bearer: v}, nil
	}
	st, err := loadAuth()
	if err != nil {
		return credential{}, err
	}
	return credential{Cookie: st.Session}, nil
}
