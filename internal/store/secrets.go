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

// querier is the read subset shared by *pgxpool.Pool and pgx.Tx, so
// livePointers can run against the pool or inside a transaction.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
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

		// Carry forward the previous version's live entries (key → secret_value_id).
		livePtrs := map[string]string{}
		if prevVersion > 0 {
			livePtrs, err = r.livePointers(ctx, tx, configID, prevVersion)
			if err != nil {
				return err
			}
		}

		// Collapse the batch so each key has one net effect (last change wins).
		// This also prevents a set-then-delete of the same key in one batch from
		// leaving an orphan secret_values row.
		final := make(map[string]*EncryptedValue, len(changes))
		for _, ch := range changes {
			final[ch.Key] = ch.Value
		}

		// Apply the net changes, recording keys deleted in this version.
		tombstones := map[string]bool{}
		for key, val := range final {
			if val != nil {
				// Set: append a new secret_values row at the next value_version.
				var nextVV int
				if err := tx.QueryRow(ctx,
					`SELECT COALESCE(MAX(value_version), 0) + 1 FROM secret_values
					 WHERE config_id = $1::uuid AND key = $2`, configID, key).Scan(&nextVV); err != nil {
					return err
				}
				var svID string
				if err := tx.QueryRow(ctx,
					`INSERT INTO secret_values
					   (config_id, key, value_version, wrapped_dek, ciphertext, nonce, dek_key_version)
					 VALUES ($1::uuid, $2, $3, $4, $5, $6, $7)
					 RETURNING id::text`,
					configID, key, nextVV, val.WrappedDEK, val.Ciphertext,
					val.Nonce, val.DEKKeyVersion).Scan(&svID); err != nil {
					return err
				}
				livePtrs[key] = svID
			} else if _, ok := livePtrs[key]; ok {
				// Delete: only meaningful if the key is currently live.
				delete(livePtrs, key)
				tombstones[key] = true
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
		for k, svID := range livePtrs {
			if _, err := tx.Exec(ctx,
				`INSERT INTO config_version_entries (config_version_id, key, secret_value_id, tombstone)
				 VALUES ($1::uuid, $2, $3::uuid, false)`, cv.ID, k, svID); err != nil {
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

// ListVersions returns a config's version metadata, oldest first.
func (r *SecretRepo) ListVersions(ctx context.Context, configID string) ([]ConfigVersion, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT id::text, config_id::text, version, message, COALESCE(created_by,''), created_at
		 FROM config_versions WHERE config_id = $1::uuid ORDER BY version ASC`, configID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []ConfigVersion
	for rows.Next() {
		var cv ConfigVersion
		if err := rows.Scan(&cv.ID, &cv.ConfigID, &cv.Version, &cv.Message, &cv.CreatedBy, &cv.CreatedAt); err != nil {
			return nil, mapError(err)
		}
		out = append(out, cv)
	}
	return out, mapError(rows.Err())
}

// GetKeyHistory returns every value a key has held, oldest first.
func (r *SecretRepo) GetKeyHistory(ctx context.Context, configID, key string) ([]SecretValue, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT id::text, config_id::text, key, value_version,
		        wrapped_dek, ciphertext, nonce, dek_key_version, created_at
		 FROM secret_values WHERE config_id = $1::uuid AND key = $2
		 ORDER BY value_version ASC`, configID, key)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []SecretValue
	for rows.Next() {
		var sv SecretValue
		if err := rows.Scan(&sv.ID, &sv.ConfigID, &sv.Key, &sv.ValueVersion,
			&sv.WrappedDEK, &sv.Ciphertext, &sv.Nonce, &sv.DEKKeyVersion, &sv.CreatedAt); err != nil {
			return nil, mapError(err)
		}
		out = append(out, sv)
	}
	return out, mapError(rows.Err())
}

// livePointers returns key → secret_value_id for a version's live
// (non-tombstone) entries. q may be the pool or a transaction. A version that
// does not exist yields an empty map, not an error.
func (r *SecretRepo) livePointers(ctx context.Context, q querier, configID string, version int) (map[string]string, error) {
	rows, err := q.Query(ctx,
		`SELECT e.key, e.secret_value_id::text
		 FROM config_version_entries e
		 JOIN config_versions v ON v.id = e.config_version_id
		 WHERE v.config_id = $1::uuid AND v.version = $2 AND NOT e.tombstone`, configID, version)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, id string
		if err := rows.Scan(&k, &id); err != nil {
			return nil, mapError(err)
		}
		out[k] = id
	}
	return out, mapError(rows.Err())
}

// Diff compares two versions by (key, secret_value_id). Because livePointers
// treats a missing version as empty, diffing a nonexistent version reports its
// keys as added/removed rather than erroring; callers that need existence
// guarantees should check the versions first.
func (r *SecretRepo) Diff(ctx context.Context, configID string, vA, vB int) (Diff, error) {
	a, err := r.livePointers(ctx, r.s.pool, configID, vA)
	if err != nil {
		return Diff{}, err
	}
	b, err := r.livePointers(ctx, r.s.pool, configID, vB)
	if err != nil {
		return Diff{}, err
	}
	var d Diff
	for k, idB := range b {
		idA, ok := a[k]
		switch {
		case !ok:
			d.Added = append(d.Added, k)
		case idA != idB:
			d.Changed = append(d.Changed, k)
		}
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			d.Removed = append(d.Removed, k)
		}
	}
	return d, nil
}

// Rollback creates a new config version whose state equals targetVersion's,
// reusing the target's secret_value rows (no re-encryption). Returns
// ErrConflict for an absent/soft-deleted config, ErrNotFound for a missing
// target version.
func (r *SecretRepo) Rollback(ctx context.Context, configID string, targetVersion int, message, createdBy string) (ConfigVersion, error) {
	var cv ConfigVersion
	err := r.s.withTx(ctx, func(tx pgx.Tx) error {
		var live bool
		err := tx.QueryRow(ctx,
			`SELECT true FROM configs WHERE id = $1::uuid AND deleted_at IS NULL FOR UPDATE`, configID).Scan(&live)
		if err != nil {
			if errors.Is(mapError(err), ErrNotFound) {
				return ErrConflict
			}
			return err
		}

		var prevVersion int
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(version), 0) FROM config_versions WHERE config_id = $1::uuid`, configID).
			Scan(&prevVersion); err != nil {
			return err
		}
		if prevVersion == 0 {
			return ErrNotFound
		}

		target, err := r.livePointers(ctx, tx, configID, targetVersion)
		if err != nil {
			return err
		}
		// A target version with no live entries AND no such version at all are
		// distinguished by checking existence.
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT true FROM config_versions WHERE config_id = $1::uuid AND version = $2`,
			configID, targetVersion).Scan(&exists); err != nil {
			if errors.Is(mapError(err), ErrNotFound) {
				return ErrNotFound
			}
			return err
		}

		current, err := r.livePointers(ctx, tx, configID, prevVersion)
		if err != nil {
			return err
		}

		newVersion := prevVersion + 1
		if err := tx.QueryRow(ctx,
			`INSERT INTO config_versions (config_id, version, message, created_by)
			 VALUES ($1::uuid, $2, $3, $4)
			 RETURNING id::text, config_id::text, version, message, COALESCE(created_by,''), created_at`,
			configID, newVersion, message, createdBy).
			Scan(&cv.ID, &cv.ConfigID, &cv.Version, &cv.Message, &cv.CreatedBy, &cv.CreatedAt); err != nil {
			return err
		}

		// Live pointers = target's live set (reusing secret_value ids).
		for k, svID := range target {
			if _, err := tx.Exec(ctx,
				`INSERT INTO config_version_entries (config_version_id, key, secret_value_id, tombstone)
				 VALUES ($1::uuid, $2, $3::uuid, false)`, cv.ID, k, svID); err != nil {
				return err
			}
		}
		// Tombstone keys that are live now but not in the target.
		for k := range current {
			if _, ok := target[k]; !ok {
				if _, err := tx.Exec(ctx,
					`INSERT INTO config_version_entries (config_version_id, key, secret_value_id, tombstone)
					 VALUES ($1::uuid, $2, NULL, true)`, cv.ID, k); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return ConfigVersion{}, err
	}
	return cv, nil
}
