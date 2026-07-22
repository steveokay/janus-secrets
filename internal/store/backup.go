package store

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"
)

// backupTable names one table in the logical dump. The slice order is
// FK-safe for insertion (parents before children); restore enforces it.
type backupTable struct {
	name    string
	orderBy string
}

// backupTables is the full-instance dump set. Excluded on purpose:
// sessions and oidc_auth_requests (ephemeral login state — everyone
// re-authenticates after a restore); schema_migrations (owned by
// golang-migrate; the header pins the version instead); and the notification
// tables (notification_channels/_deliveries/_cursor) — operational alerting
// config re-established after a restore. The cursor is seeded to the audit head
// at migrate time, so restoring channels without it would replay the entire
// restored audit history as stale alerts; reconfigure channels post-restore.
var backupTables = []backupTable{
	{"seal_config", "id"},
	{"auth_config", "id"},
	{"users", "created_at, id"},
	{"user_totp", "user_id"},
	{"user_recovery_codes", "created_at, id"},
	{"oidc_providers", "created_at, id"},
	{"oidc_identities", "created_at, id"},
	{"oidc_federation_config", "created_at, id"},
	{"oidc_federation_bindings", "created_at, id"},
	{"projects", "created_at, id"},
	{"environments", "created_at, id"},
	{"role_bindings", "created_at, id"},
	// Roots (inherits_from IS NULL) sort first so a config always restores
	// before any config that inherits from it, even on created_at ties.
	{"configs", "(inherits_from IS NOT NULL), created_at, id"},
	{"config_versions", "config_id, version"},
	{"secret_values", "created_at, id"},
	{"config_version_entries", "config_version_id, key"},
	{"rotation_policies", "created_at, id"},
	{"sync_targets", "created_at, id"},
	{"service_tokens", "created_at, id"},
	{"transit_keys", "created_at, id"},
	{"transit_key_versions", "transit_key_id, version"},
	{"audit_events", "seq"},
}

// SchemaVersion returns the applied golang-migrate version. A dirty
// migration state is an error (never back up or restore over one).
func (s *Store) SchemaVersion(ctx context.Context) (int64, error) {
	var v int64
	var dirty bool
	if err := s.pool.QueryRow(ctx,
		`SELECT version, dirty FROM schema_migrations`).Scan(&v, &dirty); err != nil {
		return 0, mapError(err)
	}
	if dirty {
		return 0, errors.New("store: schema_migrations is dirty")
	}
	return v, nil
}

// MissingBackupTables returns the names of backup-set tables absent from the
// database. A cleanly-migrated instance has all of them at any tracked version;
// a non-empty result means the schema is inconsistent with schema_migrations
// (e.g. a table dropped without resetting the tracker), which would otherwise
// fail a dump mid-stream. Callers use this as a pre-flight so the API can
// refuse with a clean error instead of aborting a partial stream.
func (s *Store) MissingBackupTables(ctx context.Context) ([]string, error) {
	names := make([]string, len(backupTables))
	for i, t := range backupTables {
		names[i] = t.name
	}
	rows, err := s.pool.Query(ctx,
		`SELECT n FROM unnest($1::text[]) AS n
		 WHERE NOT EXISTS (
		   SELECT 1 FROM information_schema.tables
		   WHERE table_schema = 'public' AND table_name = n)`, names)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var missing []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, mapError(err)
		}
		missing = append(missing, n)
	}
	return missing, mapError(rows.Err())
}

// IsEmptyForRestore reports whether the instance is empty enough to restore
// into: no seal config, no users, no projects (the state of a freshly
// migrated database, before /v1/sys/init).
func (s *Store) IsEmptyForRestore(ctx context.Context) (bool, error) {
	var seals, users, projects int
	err := s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM seal_config),
		(SELECT count(*) FROM users),
		(SELECT count(*) FROM projects)`).Scan(&seals, &users, &projects)
	if err != nil {
		return false, mapError(err)
	}
	return seals == 0 && users == 0 && projects == 0, nil
}

// DumpBackup streams every backup table to w as JSONL records:
// {"table":"<name>","row":{...}}. Rows are emitted exactly as stored —
// wrapped keys, ciphertexts, and hashes stay wrapped (key-preserving dump;
// the output contains no plaintext secrets by construction). pgx streams
// result rows, so large tables (audit_events) never buffer in memory.
func (s *Store) DumpBackup(ctx context.Context, w io.Writer) error {
	// single REPEATABLE READ snapshot so the JSONL is FK-consistent as a set —
	// a torn multi-tx dump can capture children whose parents are missing and
	// fail restore. (withTx is not reused: it hardcodes default isolation and
	// commits; a read-only dump wants this isolation and a plain rollback.)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return mapError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, t := range backupTables {
		// #nosec G201 -- identifiers come from the fixed compile-time backupTables list, not user input.
		q := fmt.Sprintf(`SELECT row_to_json(t)::text FROM %s t ORDER BY %s`, t.name, t.orderBy)
		if err := dumpTable(ctx, tx, t.name, q, w); err != nil {
			return err
		}
	}
	return nil
}

// queryer is the read surface dumpTable needs; satisfied by pgx.Tx (and the
// pool), so the dump can run every table on one snapshot transaction.
type queryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func dumpTable(ctx context.Context, q queryer, name, query string, w io.Writer) error {
	rows, err := q.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("store: dump %s: %w", name, mapError(err))
	}
	defer rows.Close()
	for rows.Next() {
		var rowJSON string
		if err := rows.Scan(&rowJSON); err != nil {
			return fmt.Errorf("store: dump %s: %w", name, mapError(err))
		}
		if _, err := fmt.Fprintf(w, "{\"table\":%q,\"row\":%s}\n", name, rowJSON); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store: dump %s: %w", name, mapError(err))
	}
	return nil
}

// RestoreBackup inserts records supplied by next() inside one transaction.
// next returns (table, rowJSON, nil) per record and io.EOF when done. Records
// must arrive in backupTables order (the dump's order) — that guarantees FK
// parents land before children. Any error rolls the whole restore back,
// leaving the instance empty and restorable.
func (s *Store) RestoreBackup(ctx context.Context, next func() (string, []byte, error)) error {
	order := make(map[string]int, len(backupTables))
	for i, t := range backupTables {
		order[t.name] = i
	}
	lastIdx := -1
	return s.withTx(ctx, func(tx pgx.Tx) error {
		// re-checked inside the tx: the handler's pre-check is advisory; a
		// concurrent init between check and restore must not interleave.
		var seals, users, projects int
		if err := tx.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM seal_config),
			(SELECT count(*) FROM users),
			(SELECT count(*) FROM projects)`).Scan(&seals, &users, &projects); err != nil {
			return mapError(err)
		}
		if seals != 0 || users != 0 || projects != 0 {
			return errors.New("store: instance is not empty")
		}
		for {
			table, row, err := next()
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
			idx, ok := order[table]
			if !ok {
				return fmt.Errorf("store: unknown backup table %q", table)
			}
			if idx < lastIdx {
				return fmt.Errorf("store: backup records out of order at %q", table)
			}
			lastIdx = idx
			// json_populate_record maps JSON fields onto the table's row type;
			// SELECT * preserves column order for the bare INSERT.
			// #nosec G201 -- identifier from the fixed compile-time backupTables list, not user input.
			q := fmt.Sprintf(
				`INSERT INTO %s SELECT * FROM json_populate_record(NULL::%s, $1::json)`,
				table, table)
			if _, err := tx.Exec(ctx, q, string(row)); err != nil {
				// wrapped so unmapped Postgres errors surface the table, not
				// row field values (emails, names) echoed by the driver.
				return fmt.Errorf("store: restore into %s: %w", table, mapError(err))
			}
		}
	})
}
