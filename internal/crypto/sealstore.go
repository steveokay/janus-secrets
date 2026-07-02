package crypto

import (
	"context"
	"encoding/json"
	"errors"
	"os"
)

// marshalSealConfig is a test injection point (json.Marshal cannot fail on
// SealConfig in practice, but the branch must be coverable).
var marshalSealConfig = json.Marshal

// FileSealConfigStore persists seal config as a private JSON file. Used for
// tests and single-binary bootstrap; a Postgres-backed implementation
// arrives with the store milestone.
//
// Put is atomic (tmp file + rename) but not crash-durable — it does not
// fsync — and assumes a single writer (Init / serialized rotation). Losing
// this file after Init loses access to every secret, so a durable store is
// expected before real single-binary deployments.
type FileSealConfigStore struct {
	Path string
}

func (f *FileSealConfigStore) Get(_ context.Context) (*SealConfig, error) {
	// #nosec G304 -- Path is operator-supplied server configuration, not user input.
	b, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoSealConfig
	}
	if err != nil {
		return nil, err
	}
	var cfg SealConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (f *FileSealConfigStore) Put(_ context.Context, cfg *SealConfig) error {
	b, err := marshalSealConfig(cfg)
	if err != nil {
		return err
	}
	tmp := f.Path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, f.Path); err != nil {
		_ = os.Remove(tmp) // don't leave a stale tmp with valid seal metadata
		return err
	}
	return nil
}
