package store

import (
	"context"
	"errors"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

// PostgresSealConfigStore persists crypto.SealConfig in the single-row
// seal_config table. It satisfies crypto.SealConfigStore.
type PostgresSealConfigStore struct {
	s *Store
}

// NewSealConfigStore returns a Postgres-backed seal config store.
func NewSealConfigStore(s *Store) *PostgresSealConfigStore {
	return &PostgresSealConfigStore{s: s}
}

// Get returns the seal config, or crypto.ErrNoSealConfig if none is stored.
func (p *PostgresSealConfigStore) Get(ctx context.Context) (*crypto.SealConfig, error) {
	var cfg crypto.SealConfig
	err := p.s.pool.QueryRow(ctx,
		`SELECT type, COALESCE(threshold, 0), COALESCE(shares, 0),
		        key_check_value, wrapped_master_key
		 FROM seal_config WHERE id = 1`).
		Scan(&cfg.Type, &cfg.Threshold, &cfg.Shares, &cfg.KeyCheckValue, &cfg.WrappedMasterKey)
	if err != nil {
		merr := mapError(err)
		if errors.Is(merr, ErrNotFound) {
			return nil, crypto.ErrNoSealConfig
		}
		return nil, merr
	}
	return &cfg, nil
}

// Put upserts the single seal config row.
func (p *PostgresSealConfigStore) Put(ctx context.Context, cfg *crypto.SealConfig) error {
	_, err := p.s.pool.Exec(ctx,
		`INSERT INTO seal_config (id, type, threshold, shares, key_check_value, wrapped_master_key)
		 VALUES (1, $1, $2, $3, $4, $5)
		 ON CONFLICT (id) DO UPDATE SET
		     type = EXCLUDED.type,
		     threshold = EXCLUDED.threshold,
		     shares = EXCLUDED.shares,
		     key_check_value = EXCLUDED.key_check_value,
		     wrapped_master_key = EXCLUDED.wrapped_master_key`,
		cfg.Type, cfg.Threshold, cfg.Shares, cfg.KeyCheckValue, cfg.WrappedMasterKey)
	return mapError(err)
}
