package store

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

// KeyMatch is a single key-name hit from SearchKeys: the config it lives in and
// the matching key. It carries NO secret value — key names are metadata.
type KeyMatch struct {
	ConfigID string
	Key      string
}

// SearchKeys returns key-name matches for the latest live config version of
// each non-soft-deleted config whose key matches q case-insensitively
// (substring, ILIKE '%q%'). Tombstoned keys (removed in the latest version) are
// excluded. Results are ordered by (key, config_id) and capped at limit.
//
// q is matched literally: the LIKE metacharacters %, _ and the escape char (\)
// are escaped so a query of "100%" or "a_b" is treated as text, not a wildcard.
// An empty or whitespace-only q returns an empty slice without querying.
//
// Value-free: this reads only key names + config ids from metadata tables; it
// never touches ciphertext, DEKs, or nonces.
func (r *SecretRepo) SearchKeys(ctx context.Context, q string, limit int) ([]KeyMatch, error) {
	if strings.TrimSpace(q) == "" {
		return nil, nil
	}
	pattern := "%" + escapeLike(q) + "%"

	// Per config, the max version is its latest. Join that version's non-tombstone
	// entries and ILIKE on the key. Soft-deleted configs are excluded by the
	// deleted_at guard. \ is the explicit escape char (matched by escapeLike).
	rows, err := r.s.pool.Query(ctx,
		`SELECT e.key, c.id::text
		 FROM configs c
		 JOIN LATERAL (
		   SELECT id FROM config_versions cv
		   WHERE cv.config_id = c.id
		   ORDER BY cv.version DESC
		   LIMIT 1
		 ) latest ON true
		 JOIN config_version_entries e
		   ON e.config_version_id = latest.id AND NOT e.tombstone
		 WHERE c.deleted_at IS NULL
		   AND e.key ILIKE $1 ESCAPE '\'
		 ORDER BY e.key, c.id
		 LIMIT $2`, pattern, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var out []KeyMatch
	for rows.Next() {
		var m KeyMatch
		if err := rows.Scan(&m.Key, &m.ConfigID); err != nil {
			return nil, mapError(err)
		}
		out = append(out, m)
	}
	return out, mapError(rows.Err())
}

// escapeLike escapes the LIKE metacharacters (%, _) and the escape character
// itself (\) so q is matched as a literal substring under `ESCAPE '\'`.
func escapeLike(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(q)
}

// SecretRepo persists secret values and config versions.
//
// Write paths (SaveConfigVersion, Rollback) reject an absent or soft-deleted
// config. Read paths (GetLatest, GetVersion, ListVersions, GetKeyHistory, Diff)
// deliberately do NOT check the config's deleted_at — they resolve versions
// regardless. A caller that must hide a soft-deleted config's secrets is
// responsible for checking the config first (e.g. via ConfigRepo.Get, which
// returns ErrNotFound for deleted configs).
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
		// leaving an orphan secret_values row. A nil closure means delete.
		final := make(map[string]func(int) (*EncryptedValue, error), len(changes))
		types := make(map[string]string, len(changes))
		for _, ch := range changes {
			final[ch.Key] = ch.Encrypt
			types[ch.Key] = ch.Type
		}

		// Apply the net changes, recording keys deleted in this version.
		tombstones := map[string]bool{}
		for key, encrypt := range final {
			if encrypt != nil {
				// Set: assign the next value_version, then ask the caller to
				// encrypt for exactly that version.
				var nextVV int
				if err := tx.QueryRow(ctx,
					`SELECT COALESCE(MAX(value_version), 0) + 1 FROM secret_values
					 WHERE config_id = $1::uuid AND key = $2`, configID, key).Scan(&nextVV); err != nil {
					return err
				}
				val, err := encrypt(nextVV)
				if err != nil {
					return err
				}
				secretType := types[key]
				if secretType == "" {
					secretType = "string"
				}
				var svID string
				if err := tx.QueryRow(ctx,
					`INSERT INTO secret_values
					   (config_id, key, value_version, wrapped_dek, ciphertext, nonce, dek_key_version, type)
					 VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8)
					 RETURNING id::text`,
					configID, key, nextVV, val.WrappedDEK, val.Ciphertext,
					val.Nonce, val.DEKKeyVersion, secretType).Scan(&svID); err != nil {
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
		`SELECT id::text, config_id::text, version, message, COALESCE(created_by,''), created_at,
		        promoted_from_env_id::text, promoted_from_version
		 FROM config_versions WHERE config_id = $1::uuid AND version = $2`, configID, version).
		Scan(&cv.ID, &cv.ConfigID, &cv.Version, &cv.Message, &cv.CreatedBy, &cv.CreatedAt,
			&cv.PromotedFromEnvID, &cv.PromotedFromVersion)
	if err != nil {
		return ConfigVersion{}, nil, mapError(err)
	}

	rows, err := r.s.pool.Query(ctx,
		`SELECT sv.id::text, sv.config_id::text, sv.key, sv.value_version,
		        sv.wrapped_dek, sv.ciphertext, sv.nonce, sv.dek_key_version, sv.created_at, sv.type
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
			&sv.WrappedDEK, &sv.Ciphertext, &sv.Nonce, &sv.DEKKeyVersion, &sv.CreatedAt, &sv.Type); err != nil {
			return ConfigVersion{}, nil, mapError(err)
		}
		state[sv.Key] = sv
	}
	if err := rows.Err(); err != nil {
		return ConfigVersion{}, nil, mapError(err)
	}
	return cv, state, nil
}

// GetValueByID returns a single secret_values row by its id. The read path uses
// it to re-read a row after a concurrent KEK rewrap advanced the row and retired
// the KEK version its snapshot referenced, so it can retry against fresh state.
func (r *SecretRepo) GetValueByID(ctx context.Context, id string) (SecretValue, error) {
	var sv SecretValue
	err := r.s.pool.QueryRow(ctx,
		`SELECT id::text, config_id::text, key, value_version,
		        wrapped_dek, ciphertext, nonce, dek_key_version, created_at, type
		 FROM secret_values WHERE id = $1::uuid`, id).
		Scan(&sv.ID, &sv.ConfigID, &sv.Key, &sv.ValueVersion,
			&sv.WrappedDEK, &sv.Ciphertext, &sv.Nonce, &sv.DEKKeyVersion, &sv.CreatedAt, &sv.Type)
	if err != nil {
		return SecretValue{}, mapError(err)
	}
	return sv, nil
}

// ListVersions returns a config's version metadata, oldest first.
func (r *SecretRepo) ListVersions(ctx context.Context, configID string) ([]ConfigVersion, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT id::text, config_id::text, version, message, COALESCE(created_by,''), created_at,
		        promoted_from_env_id::text, promoted_from_version
		 FROM config_versions WHERE config_id = $1::uuid ORDER BY version ASC`, configID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []ConfigVersion
	for rows.Next() {
		var cv ConfigVersion
		if err := rows.Scan(&cv.ID, &cv.ConfigID, &cv.Version, &cv.Message, &cv.CreatedBy, &cv.CreatedAt,
			&cv.PromotedFromEnvID, &cv.PromotedFromVersion); err != nil {
			return nil, mapError(err)
		}
		out = append(out, cv)
	}
	return out, mapError(rows.Err())
}

// PromotionRef is a config version's promotion source.
type PromotionRef struct {
	SourceEnvID   string
	SourceVersion int
}

// MarkPromoted records the promotion source (env id + version) on a config
// version. Value-free: it writes only ids + an int, never a secret value.
func (r *SecretRepo) MarkPromoted(ctx context.Context, configVersionID, sourceEnvID string, sourceVersion int) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE config_versions SET promoted_from_env_id=$2::uuid, promoted_from_version=$3 WHERE id=$1::uuid`,
		configVersionID, sourceEnvID, sourceVersion)
}

// LatestPromotionByConfig returns, for each config id whose LATEST version was
// created by promotion, that version's source env id + version. Configs whose
// latest version was not a promotion are absent from the map.
func (r *SecretRepo) LatestPromotionByConfig(ctx context.Context, configIDs []string) (map[string]PromotionRef, error) {
	out := map[string]PromotionRef{}
	if len(configIDs) == 0 {
		return out, nil
	}
	rows, err := r.s.pool.Query(ctx,
		`SELECT DISTINCT ON (config_id) config_id::text, promoted_from_env_id::text, promoted_from_version
		 FROM config_versions WHERE config_id = ANY($1)
		 ORDER BY config_id, version DESC`, configIDs)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid string
		var env *string
		var ver *int
		if err := rows.Scan(&cid, &env, &ver); err != nil {
			return nil, mapError(err)
		}
		if env != nil && ver != nil {
			out[cid] = PromotionRef{SourceEnvID: *env, SourceVersion: *ver}
		}
	}
	return out, mapError(rows.Err())
}

// GetKeyHistory returns every value a key has held, oldest first.
func (r *SecretRepo) GetKeyHistory(ctx context.Context, configID, key string) ([]SecretValue, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT id::text, config_id::text, key, value_version,
		        wrapped_dek, ciphertext, nonce, dek_key_version, created_at, type
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
			&sv.WrappedDEK, &sv.Ciphertext, &sv.Nonce, &sv.DEKKeyVersion, &sv.CreatedAt, &sv.Type); err != nil {
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
