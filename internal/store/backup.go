package store

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// backupTable names one table in the logical dump. The slice order is
// FK-safe for insertion (parents before children); restore enforces it.
type backupTable struct {
	name    string
	orderBy string
}

// backupTables is the full-instance dump set. Excluded on purpose:
// sessions and oidc_auth_requests (ephemeral login state — everyone
// re-authenticates after a restore) and schema_migrations (owned by
// golang-migrate; the header pins the version instead).
var backupTables = []backupTable{
	{"seal_config", "id"},
	{"auth_config", "id"},
	{"users", "created_at, id"},
	{"oidc_providers", "created_at, id"},
	{"oidc_identities", "created_at, id"},
	{"oidc_federation_config", "created_at, id"},
	{"oidc_federation_bindings", "created_at, id"},
	{"projects", "created_at, id"},
	{"environments", "created_at, id"},
	{"role_bindings", "created_at, id"},
	{"configs", "created_at, id"},
	{"config_versions", "config_id, version"},
	{"secret_values", "created_at, id"},
	{"config_version_entries", "config_version_id, key"},
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
	for _, t := range backupTables {
		// #nosec G201 -- identifiers come from the fixed compile-time backupTables list, not user input.
		q := fmt.Sprintf(`SELECT row_to_json(t)::text FROM %s t ORDER BY %s`, t.name, t.orderBy)
		if err := dumpTable(ctx, s, t.name, q, w); err != nil {
			return err
		}
	}
	return nil
}

func dumpTable(ctx context.Context, s *Store, name, query string, w io.Writer) error {
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var rowJSON string
		if err := rows.Scan(&rowJSON); err != nil {
			return mapError(err)
		}
		if _, err := fmt.Fprintf(w, "{\"table\":%q,\"row\":%s}\n", name, rowJSON); err != nil {
			return err
		}
	}
	return mapError(rows.Err())
}
