package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// TransitKey is a named transit key with its version history.
type TransitKey struct {
	ID                   string
	Name                 string
	KeyType              string
	LatestVersion        int
	MinDecryptionVersion int
	DeletionAllowed      bool
	CreatedAt            time.Time
	UpdatedAt            time.Time
	Versions             []TransitKeyVersion // ordered by version asc; empty on metadata-only reads
}

// TransitKeyVersion is one version's wrapped key material (+ ed25519 public key).
type TransitKeyVersion struct {
	ID              string
	Version         int
	WrappedMaterial []byte
	PublicKey       []byte // ed25519 public key in clear; nil for aes keys
	CreatedAt       time.Time
}

// TransitRepo persists transit keys and versions (crypto-blind).
type TransitRepo struct{ s *Store }

// NewTransitRepo returns a transit key repository.
func NewTransitRepo(s *Store) *TransitRepo { return &TransitRepo{s: s} }

const transitKeyCols = `id::text, name, key_type, latest_version, min_decryption_version, deletion_allowed, created_at, updated_at`

const transitVersionCols = `id::text, version, wrapped_material, public_key, created_at`

// Create inserts a new key with its first version in one transaction.
func (r *TransitRepo) Create(ctx context.Context, id, name, keyType string, v *TransitKeyVersion) (*TransitKey, error) {
	err := r.s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO transit_keys (id, name, key_type) VALUES ($1::uuid, $2, $3)`,
			id, name, keyType); err != nil {
			return mapError(err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO transit_key_versions (id, transit_key_id, version, wrapped_material, public_key)
			 VALUES ($1::uuid, $2::uuid, $3, $4, $5)`,
			v.ID, id, v.Version, v.WrappedMaterial, v.PublicKey); err != nil {
			return mapError(err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return r.GetByName(ctx, name)
}

// AppendVersion inserts a new version and bumps latest_version to it.
func (r *TransitRepo) AppendVersion(ctx context.Context, keyID string, v *TransitKeyVersion) error {
	return r.s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO transit_key_versions (id, transit_key_id, version, wrapped_material, public_key)
			 VALUES ($1::uuid, $2::uuid, $3, $4, $5)`,
			v.ID, keyID, v.Version, v.WrappedMaterial, v.PublicKey); err != nil {
			return mapError(err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE transit_keys SET latest_version = $2, updated_at = now() WHERE id = $1::uuid`,
			keyID, v.Version); err != nil {
			return mapError(err)
		}
		return nil
	})
}

// UpdateConfig sets min_decryption_version and/or deletion_allowed (nil = leave).
func (r *TransitRepo) UpdateConfig(ctx context.Context, keyID string, minDec *int, delAllowed *bool) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE transit_keys SET
		   min_decryption_version = COALESCE($2, min_decryption_version),
		   deletion_allowed       = COALESCE($3, deletion_allowed),
		   updated_at             = now()
		 WHERE id = $1::uuid`, keyID, minDec, delAllowed)
}

// TrimBelow deletes versions with version < minAvailable.
func (r *TransitRepo) TrimBelow(ctx context.Context, keyID string, minAvailable int) error {
	_, err := r.s.pool.Exec(ctx,
		`DELETE FROM transit_key_versions WHERE transit_key_id = $1::uuid AND version < $2`,
		keyID, minAvailable)
	return mapError(err)
}

// Delete removes a key (cascade removes versions). Returns ErrNotFound if the
// key does not exist.
func (r *TransitRepo) Delete(ctx context.Context, keyID string) error {
	return r.s.execAffectingOne(ctx, `DELETE FROM transit_keys WHERE id = $1::uuid`, keyID)
}

// GetByName returns a key with all its versions (ordered by version asc).
func (r *TransitRepo) GetByName(ctx context.Context, name string) (*TransitKey, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+transitKeyCols+` FROM transit_keys WHERE name = $1`, name)
	return r.scanKeyWithVersions(ctx, row)
}

// GetByID returns a key with all its versions (ordered by version asc), keyed
// on the key's id. Mirrors GetByName for callers that hold an id.
func (r *TransitRepo) GetByID(ctx context.Context, id string) (*TransitKey, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+transitKeyCols+` FROM transit_keys WHERE id = $1::uuid`, id)
	return r.scanKeyWithVersions(ctx, row)
}

// scanKeyWithVersions scans a single key row then loads its versions.
func (r *TransitRepo) scanKeyWithVersions(ctx context.Context, row interface{ Scan(...any) error }) (*TransitKey, error) {
	var k TransitKey
	if err := row.Scan(&k.ID, &k.Name, &k.KeyType, &k.LatestVersion, &k.MinDecryptionVersion,
		&k.DeletionAllowed, &k.CreatedAt, &k.UpdatedAt); err != nil {
		return nil, mapError(err)
	}
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+transitVersionCols+`
		 FROM transit_key_versions WHERE transit_key_id = $1::uuid ORDER BY version ASC`, k.ID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var v TransitKeyVersion
		if err := rows.Scan(&v.ID, &v.Version, &v.WrappedMaterial, &v.PublicKey, &v.CreatedAt); err != nil {
			return nil, mapError(err)
		}
		k.Versions = append(k.Versions, v)
	}
	return &k, mapError(rows.Err())
}

// List returns key metadata (no versions). It is the unbounded delegate of
// ListPage.
func (r *TransitRepo) List(ctx context.Context) ([]*TransitKey, error) {
	return r.ListPage(ctx, 0, nil)
}

// ListPage returns key metadata (no versions) in (created_at DESC, id DESC)
// order, with keyset continuation from after (nil = first page) and a LIMIT when
// limit>0 (limit<=0 = unbounded, the legacy List path).
func (r *TransitRepo) ListPage(ctx context.Context, limit int, after *Cursor) ([]*TransitKey, error) {
	q := `SELECT ` + transitKeyCols + ` FROM transit_keys`
	var args []any
	if ks, ksArgs := keyset(after, len(args)+1); ks != "" {
		q += " WHERE " + ks
		args = append(args, ksArgs...)
	}
	q += " ORDER BY created_at DESC, id DESC"
	if ls, lArgs := limitSQL(limit, len(args)+1); ls != "" {
		q += ls
		args = append(args, lArgs...)
	}
	rows, err := r.s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*TransitKey
	for rows.Next() {
		var k TransitKey
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyType, &k.LatestVersion, &k.MinDecryptionVersion,
			&k.DeletionAllowed, &k.CreatedAt, &k.UpdatedAt); err != nil {
			return nil, mapError(err)
		}
		out = append(out, &k)
	}
	return out, mapError(rows.Err())
}
