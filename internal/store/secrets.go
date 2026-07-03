package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// SecretRepo persists secret values and config versions.
type SecretRepo struct{ s *Store }

// NewSecretRepo returns a secret repository.
func NewSecretRepo(s *Store) *SecretRepo { return &SecretRepo{s: s} }

// manifestEntry is one live pointer while building a new version's manifest.
type manifestEntry struct {
	secretValueID string
}

// SaveConfigVersion batches changes into one new immutable config version.
// changes may be empty (creates an empty version). Returns ErrConflict if the
// config is absent or soft-deleted.
func (r *SecretRepo) SaveConfigVersion(ctx context.Context, configID string, changes []Change, message, createdBy string) (ConfigVersion, error) {
	var cv ConfigVersion
	err := r.s.withTx(ctx, func(tx pgx.Tx) error {
		// Serialize saves per-config and confirm it is live.
		var live bool
		err := tx.QueryRow(ctx,
			`SELECT true FROM configs WHERE id = $1::uuid AND deleted_at IS NULL FOR UPDATE`, configID).
			Scan(&live)
		if err != nil {
			if errors.Is(mapError(err), ErrNotFound) {
				return ErrConflict
			}
			return err
		}

		// Next version number.
		var prevVersion int
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(version), 0) FROM config_versions WHERE config_id = $1::uuid`, configID).
			Scan(&prevVersion); err != nil {
			return err
		}
		newVersion := prevVersion + 1

		// Carry forward the previous version's live entries into a map.
		live2 := map[string]manifestEntry{}
		if prevVersion > 0 {
			rows, err := tx.Query(ctx,
				`SELECT e.key, e.secret_value_id::text
				 FROM config_version_entries e
				 JOIN config_versions v ON v.id = e.config_version_id
				 WHERE v.config_id = $1::uuid AND v.version = $2 AND NOT e.tombstone`,
				configID, prevVersion)
			if err != nil {
				return err
			}
			for rows.Next() {
				var k, svID string
				if err := rows.Scan(&k, &svID); err != nil {
					rows.Close()
					return err
				}
				live2[k] = manifestEntry{secretValueID: svID}
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
		}

		// Tombstones to write for this version (keys deleted now).
		tombstones := map[string]bool{}

		// Apply changes.
		for _, ch := range changes {
			if ch.Value != nil {
				// Set: append a new secret_values row at the next value_version.
				var nextVV int
				if err := tx.QueryRow(ctx,
					`SELECT COALESCE(MAX(value_version), 0) + 1 FROM secret_values
					 WHERE config_id = $1::uuid AND key = $2`, configID, ch.Key).Scan(&nextVV); err != nil {
					return err
				}
				var svID string
				if err := tx.QueryRow(ctx,
					`INSERT INTO secret_values
					   (config_id, key, value_version, wrapped_dek, ciphertext, nonce, dek_key_version)
					 VALUES ($1::uuid, $2, $3, $4, $5, $6, $7)
					 RETURNING id::text`,
					configID, ch.Key, nextVV, ch.Value.WrappedDEK, ch.Value.Ciphertext,
					ch.Value.Nonce, ch.Value.DEKKeyVersion).Scan(&svID); err != nil {
					return err
				}
				live2[ch.Key] = manifestEntry{secretValueID: svID}
				delete(tombstones, ch.Key)
			} else {
				// Delete: only meaningful if the key is currently live.
				if _, ok := live2[ch.Key]; ok {
					delete(live2, ch.Key)
					tombstones[ch.Key] = true
				}
			}
		}

		// Insert the config version.
		if err := tx.QueryRow(ctx,
			`INSERT INTO config_versions (config_id, version, message, created_by)
			 VALUES ($1::uuid, $2, $3, $4)
			 RETURNING id::text, config_id::text, version, message, COALESCE(created_by,''), created_at`,
			configID, newVersion, message, createdBy).
			Scan(&cv.ID, &cv.ConfigID, &cv.Version, &cv.Message, &cv.CreatedBy, &cv.CreatedAt); err != nil {
			return err
		}

		// Insert manifest entries: live pointers + this-version tombstones.
		for k, m := range live2 {
			if _, err := tx.Exec(ctx,
				`INSERT INTO config_version_entries (config_version_id, key, secret_value_id, tombstone)
				 VALUES ($1::uuid, $2, $3::uuid, false)`, cv.ID, k, m.secretValueID); err != nil {
				return err
			}
		}
		for k := range tombstones {
			if _, err := tx.Exec(ctx,
				`INSERT INTO config_version_entries (config_version_id, key, secret_value_id, tombstone)
				 VALUES ($1::uuid, $2, NULL, true)`, cv.ID, k); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return ConfigVersion{}, err
	}
	return cv, nil
}

// GetLatest returns the newest config version and its resolved state.
func (r *SecretRepo) GetLatest(ctx context.Context, configID string) (ConfigVersion, map[string]SecretValue, error) {
	var version int
	err := r.s.pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM config_versions WHERE config_id = $1::uuid`, configID).
		Scan(&version)
	if err != nil {
		return ConfigVersion{}, nil, mapError(err)
	}
	if version == 0 {
		return ConfigVersion{}, nil, ErrNotFound
	}
	return r.GetVersion(ctx, configID, version)
}

// GetVersion returns a specific config version and its resolved state
// (tombstones excluded).
func (r *SecretRepo) GetVersion(ctx context.Context, configID string, version int) (ConfigVersion, map[string]SecretValue, error) {
	var cv ConfigVersion
	err := r.s.pool.QueryRow(ctx,
		`SELECT id::text, config_id::text, version, message, COALESCE(created_by,''), created_at
		 FROM config_versions WHERE config_id = $1::uuid AND version = $2`, configID, version).
		Scan(&cv.ID, &cv.ConfigID, &cv.Version, &cv.Message, &cv.CreatedBy, &cv.CreatedAt)
	if err != nil {
		return ConfigVersion{}, nil, mapError(err)
	}

	rows, err := r.s.pool.Query(ctx,
		`SELECT sv.id::text, sv.config_id::text, sv.key, sv.value_version,
		        sv.wrapped_dek, sv.ciphertext, sv.nonce, sv.dek_key_version, sv.created_at
		 FROM config_version_entries e
		 JOIN secret_values sv ON sv.id = e.secret_value_id
		 WHERE e.config_version_id = $1::uuid AND NOT e.tombstone`, cv.ID)
	if err != nil {
		return ConfigVersion{}, nil, mapError(err)
	}
	defer rows.Close()

	state := map[string]SecretValue{}
	for rows.Next() {
		var sv SecretValue
		if err := rows.Scan(&sv.ID, &sv.ConfigID, &sv.Key, &sv.ValueVersion,
			&sv.WrappedDEK, &sv.Ciphertext, &sv.Nonce, &sv.DEKKeyVersion, &sv.CreatedAt); err != nil {
			return ConfigVersion{}, nil, mapError(err)
		}
		state[sv.Key] = sv
	}
	if err := rows.Err(); err != nil {
		return ConfigVersion{}, nil, mapError(err)
	}
	return cv, state, nil
}
