package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/steveokay/janus-secrets/internal/crypto"
)

// MasterKeyRepo re-wraps every master-key-wrapped blob under a fresh master key
// in one transaction, then writes the new seal metadata. It never sees plaintext
// key material: the caller supplies a rewrap closure that decrypts under the old
// master and re-encrypts under the new one for a given AAD.
type MasterKeyRepo struct{ s *Store }

// NewMasterKeyRepo returns a master-key rotation repository.
func NewMasterKeyRepo(s *Store) *MasterKeyRepo { return &MasterKeyRepo{s: s} }

// MasterKeyMeta is the non-secret master-key rotation state read from seal_config.
type MasterKeyMeta struct {
	Version   int
	RotatedAt *time.Time
	SealType  string
}

// rewrapFn decrypts an old master-wrapped ciphertext and re-encrypts the same
// plaintext under the new master, both bound to aad. Supplied by the caller so
// the store stays crypto-blind.
type rewrapFn func(old, aad []byte) (newCT []byte, err error)

// RewrapAllUnderNewMaster re-wraps every master-wrapped blob (project KEKs,
// superseded KEK versions, the auth token-HMAC key, OIDC client secrets,
// transit key material, and notification channel configs) under a new master
// via rewrap, then writes the reseal
// result and bumps master_key_version. Everything runs in a single transaction:
// any error — including one from reseal — rolls the whole rotation back.
func (r *MasterKeyRepo) RewrapAllUnderNewMaster(
	ctx context.Context,
	rewrap rewrapFn,
	reseal func() (*crypto.SealConfig, error),
) error {
	return r.s.withTx(ctx, func(tx pgx.Tx) error {
		// 1. projects.wrapped_kek (ALL projects, including soft-deleted: undelete
		//    is reversible, so a stranded KEK would mean permanent data loss).
		if err := rewrapByID(ctx, tx, rewrap,
			`SELECT id::text, wrapped_kek FROM projects FOR UPDATE`,
			func(id string) []byte { return crypto.ProjectKEKAAD(id) },
			`UPDATE projects SET wrapped_kek=$2 WHERE id=$1::uuid`); err != nil {
			return err
		}
		// 2. project_kek_versions.wrapped_kek (composite PK on project_id+version).
		if err := rewrapKEKVersions(ctx, tx, rewrap); err != nil {
			return err
		}
		// 3. auth_config (single row, may be absent).
		if err := rewrapSingle(ctx, tx, rewrap, crypto.AuthKeyAAD(),
			`SELECT wrapped_token_hmac_key FROM auth_config WHERE id=1 FOR UPDATE`,
			`UPDATE auth_config SET wrapped_token_hmac_key=$1 WHERE id=1`); err != nil {
			return err
		}
		// 4. oidc_providers (all rows; secret is arbitrary length).
		if err := rewrapByID(ctx, tx, rewrap,
			`SELECT id::text, wrapped_client_secret FROM oidc_providers FOR UPDATE`,
			func(string) []byte { return crypto.OIDCClientSecretAAD() },
			`UPDATE oidc_providers SET wrapped_client_secret=$2 WHERE id=$1::uuid`); err != nil {
			return err
		}
		// 5. transit_key_versions (AAD binds the key name + version).
		if err := rewrapTransit(ctx, tx, rewrap); err != nil {
			return err
		}
		// 6. notification_channels (all rows; config blob is arbitrary length,
		//    AAD binds the channel id).
		if err := rewrapByID(ctx, tx, rewrap,
			`SELECT id::text, config_ct FROM notification_channels FOR UPDATE`,
			func(id string) []byte { return crypto.NotificationChannelAAD(id) },
			`UPDATE notification_channels SET config_ct=$2 WHERE id=$1::uuid`); err != nil {
			return err
		}
		// 7. reseal + persist new seal metadata, bumping the version.
		cfg, err := reseal()
		if err != nil {
			return err
		}
		return writeResealCfg(ctx, tx, cfg)
	})
}

// idRow pairs a row identifier with its current wrapped blob.
type idRow struct {
	id   string
	blob []byte
}

// rewrapByID rewraps every row returned by selectSQL (id::text, blob), using
// aadFor(id) as the AAD, then UPDATEs each via updateSQL($1=id, $2=newBlob).
// The SELECT cursor is fully drained and closed before any UPDATE runs, so the
// UPDATEs never contend with an open cursor on the same connection.
func rewrapByID(ctx context.Context, tx pgx.Tx, rewrap rewrapFn,
	selectSQL string, aadFor func(id string) []byte, updateSQL string) error {
	rows, err := tx.Query(ctx, selectSQL)
	if err != nil {
		return mapError(err)
	}
	var buf []idRow
	for rows.Next() {
		var rec idRow
		if err := rows.Scan(&rec.id, &rec.blob); err != nil {
			rows.Close()
			return err
		}
		buf = append(buf, rec)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return mapError(err)
	}
	for _, rec := range buf {
		nc, err := rewrap(rec.blob, aadFor(rec.id))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, updateSQL, rec.id, nc); err != nil {
			return mapError(err)
		}
	}
	return nil
}

// kekVersionRow is a superseded project-KEK version (composite PK).
type kekVersionRow struct {
	projectID string
	version   int
	blob      []byte
}

// rewrapKEKVersions rewraps project_kek_versions. The PK is (project_id,
// version); the AAD binds only to the project, and both PK columns key the UPDATE.
func rewrapKEKVersions(ctx context.Context, tx pgx.Tx, rewrap rewrapFn) error {
	rows, err := tx.Query(ctx,
		`SELECT project_id::text, version, wrapped_kek FROM project_kek_versions FOR UPDATE`)
	if err != nil {
		return mapError(err)
	}
	var buf []kekVersionRow
	for rows.Next() {
		var rec kekVersionRow
		if err := rows.Scan(&rec.projectID, &rec.version, &rec.blob); err != nil {
			rows.Close()
			return err
		}
		buf = append(buf, rec)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return mapError(err)
	}
	for _, rec := range buf {
		nc, err := rewrap(rec.blob, crypto.ProjectKEKAAD(rec.projectID))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE project_kek_versions SET wrapped_kek=$3 WHERE project_id=$1::uuid AND version=$2`,
			rec.projectID, rec.version, nc); err != nil {
			return mapError(err)
		}
	}
	return nil
}

// rewrapSingle rewraps a single-row blob (id=1). A missing row is not an error:
// e.g. auth_config may be absent before the first token key is initialized.
func rewrapSingle(ctx context.Context, tx pgx.Tx, rewrap rewrapFn, aad []byte,
	selectSQL, updateSQL string) error {
	var blob []byte
	if err := tx.QueryRow(ctx, selectSQL).Scan(&blob); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return mapError(err)
	}
	nc, err := rewrap(blob, aad)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, updateSQL, nc); err != nil {
		return mapError(err)
	}
	return nil
}

// transitRow is a transit key version plus its key name, needed for the AAD.
type transitRow struct {
	id      string
	name    string
	version int
	blob    []byte
}

// rewrapTransit rewraps every transit_key_versions row. The AAD binds the key
// name (from transit_keys) and version, so the join carries the name; the UPDATE
// keys on the version row's own id. FOR UPDATE OF v locks only the version rows.
func rewrapTransit(ctx context.Context, tx pgx.Tx, rewrap rewrapFn) error {
	rows, err := tx.Query(ctx,
		`SELECT v.id::text, k.name, v.version, v.wrapped_material
		   FROM transit_key_versions v
		   JOIN transit_keys k ON k.id = v.transit_key_id
		  FOR UPDATE OF v`)
	if err != nil {
		return mapError(err)
	}
	var buf []transitRow
	for rows.Next() {
		var rec transitRow
		if err := rows.Scan(&rec.id, &rec.name, &rec.version, &rec.blob); err != nil {
			rows.Close()
			return err
		}
		buf = append(buf, rec)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return mapError(err)
	}
	for _, rec := range buf {
		nc, err := rewrap(rec.blob, crypto.TransitKeyAAD(rec.name, rec.version))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE transit_key_versions SET wrapped_material=$2 WHERE id=$1::uuid`,
			rec.id, nc); err != nil {
			return mapError(err)
		}
	}
	return nil
}

// writeResealCfg persists the new seal metadata and bumps the master-key
// version. threshold/shares are nullable integers; mirroring sealconfig.go's
// Put, we write cfg's plain ints (Shamir sets them; a KMS reseal would carry a
// non-nil WrappedMasterKey and its own threshold/shares handling upstream).
func writeResealCfg(ctx context.Context, tx pgx.Tx, cfg *crypto.SealConfig) error {
	tag, err := tx.Exec(ctx,
		`UPDATE seal_config
		    SET type=$1, threshold=$2, shares=$3, key_check_value=$4, wrapped_master_key=$5,
		        master_key_version = master_key_version + 1,
		        master_key_rotated_at = now()
		  WHERE id=1`,
		cfg.Type, cfg.Threshold, cfg.Shares, cfg.KeyCheckValue, cfg.WrappedMasterKey)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetMasterKeyMeta reads the master-key rotation state from seal_config (id=1)
// via the pool (not a transaction) — a plain observability read.
func (r *MasterKeyRepo) GetMasterKeyMeta(ctx context.Context) (MasterKeyMeta, error) {
	var m MasterKeyMeta
	err := r.s.pool.QueryRow(ctx,
		`SELECT type, master_key_version, master_key_rotated_at FROM seal_config WHERE id=1`).
		Scan(&m.SealType, &m.Version, &m.RotatedAt)
	if err != nil {
		return MasterKeyMeta{}, mapError(err)
	}
	return m, nil
}
