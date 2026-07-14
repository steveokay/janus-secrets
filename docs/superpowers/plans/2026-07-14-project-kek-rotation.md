# Project-KEK Rotation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a project owner rotate a project's KEK so DEKs are lazily re-wrapped under a fresh KEK, with an instant `rotate` and a resumable `rewrap` sweep that never decrypts a secret value.

**Architecture:** The current wrapped KEK stays on `projects.wrapped_kek`/`kek_version` (latest, hot-path unchanged). A new `project_kek_versions` table holds only *superseded* versions still referenced by un-rewrapped DEKs (empty in steady state). `rotate` (one tx) preserves the old version and installs a new one; `rewrap` (batched, resumable) re-wraps each DEK old→new (unwrap+rewrap the 32-byte DEK only, fresh nonce, same AAD — the value ciphertext is never touched) then retires emptied versions. A new `internal/projectkeys.ProjectKeyService` orchestrates over the keyring + repos; the secrets read path resolves the KEK per `dek_key_version` via a small caching resolver.

**Tech Stack:** Go, pgx v5, golang-migrate, chi, cobra, `crypto/*` + `x/crypto` (AES-256-GCM), testcontainers.

**Spec:** `docs/superpowers/specs/2026-07-14-project-kek-rotation-design.md`. Read it before starting.

**Key facts (verified):**
- `projects` has `wrapped_kek bytea`, `kek_version int DEFAULT 1` (never incremented today). `internal/store/models.go:22` `Project{WrappedKEK []byte; KEKVersion int}`.
- `secret_values` has `wrapped_dek, ciphertext, nonce, dek_key_version int`. `internal/store/models.go:69` `SecretValue{... EncryptedValue{WrappedDEK, Ciphertext, Nonce []byte; DEKKeyVersion int}}`.
- Keyring (`internal/crypto/keyring.go`): `WrapProjectKEK(kek []byte, projectID string) (Ciphertext, error)`, `UnwrapProjectKEK(ct Ciphertext, projectID string) ([]byte, error)`, `NewDEK(projectKEK, aad []byte) (dek []byte, wrapped Ciphertext, err error)`. Package funcs: `crypto.GenerateKey() ([]byte, error)`, `crypto.WrapKey(wrappingKey, key, aad) (Ciphertext, error)`, `crypto.UnwrapKey(wrappingKey, ct, aad) ([]byte, error)`, `crypto.ParseCiphertext([]byte) (Ciphertext, error)`, `Ciphertext.Marshal() []byte`, `crypto.DEKAAD(projectID, path string, version uint64) []byte`, `crypto.ProjectKEKAAD(projectID string) []byte`. `crypto.ErrSealed`, `crypto.KeySize == 32`.
- Store: `Store.withTx(ctx, func(pgx.Tx) error) error` (unexported, `internal/store/store.go:42`), `execAffectingOne`, `mapError`, `ErrNotFound`. `pgx` = `github.com/jackc/pgx/v5`.
- Secrets read path calls `s.unwrapProjectKEK(proj)` then `s.decryptValue(proj, cfgID, sv, kek)` at: `internal/secrets/rawread.go:58,64` and `internal/secrets/secrets.go:43,115/120,137/144,182/187`.
- authz actions in `internal/authz/actions.go`; owner-only actions live in `ownerActions` (currently `= union(adminActions, setOf(ProjectDelete))`).
- API handler idioms (`internal/api/projects_handlers.go`): `s.authorize(w, r, action, authz.Resource{ProjectID: pid}, auditAction, auditResource) bool` (mutations, records denials), `s.can(r, action, resource) error` (reads), `writeJSON(w, status, v)`, `writeError(w, status, code, msg)`, `s.writeServiceError(w, err)`, `s.record(r, action, resource, result, code, detail) error`, `chi.URLParam(r, "pid")`. Codes: `CodeValidation`, `CodeInternal`.
- CLI idioms (`cmd/janus/rotation_commands.go`): a `newXCmd() *cobra.Command` with `--address`/`--token` persistent flags, `newAPIClient(address, token)`, `c.call("METHOD", path, body, &out)`; registered in `cmd/janus/main.go:25` `root.AddCommand(...)`.
- Migrations end at `000014_sync_runs`; next is **000015**. Migrator embeds `migrations/*.sql`.

**Global rules:** TDD (failing test first). Never log/return/audit key or value plaintext. `rewrap` must never SELECT or touch `secret_values.ciphertext`/`nonce`. Zero every unwrapped KEK/DEK after use. Do NOT touch the running dev container/DB (ports 8210/5433) or run `make migrate`. Store/api/service tests use testcontainers (real Postgres, Docker required).

---

### Task 1: Migration `000015_project_kek_versions`

**Files:**
- Create: `migrations/000015_project_kek_versions.up.sql`
- Create: `migrations/000015_project_kek_versions.down.sql`
- Test: `internal/store/project_kek_versions_migration_test.go`

- [ ] **Step 1: Write the up/down SQL.**

`migrations/000015_project_kek_versions.up.sql`:
```sql
CREATE TABLE project_kek_versions (
    project_id  uuid    NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    version     integer NOT NULL,
    wrapped_kek bytea   NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, version)
);
```

`migrations/000015_project_kek_versions.down.sql`:
```sql
DROP TABLE IF EXISTS project_kek_versions;
```

- [ ] **Step 2: Write the failing migration test.** Mirror `internal/store/sync_migration_test.go`.

`internal/store/project_kek_versions_migration_test.go`:
```go
package store

import (
	"context"
	"testing"
)

func TestMigration015CreatesProjectKEKVersions(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	var reg *string
	if err := s.pool.QueryRow(context.Background(),
		`SELECT to_regclass('public.project_kek_versions')::text`).Scan(&reg); err != nil {
		t.Fatalf("query: %v", err)
	}
	if reg == nil || *reg != "project_kek_versions" {
		t.Fatalf("table project_kek_versions not created, got %v", reg)
	}
}
```

- [ ] **Step 3: Run — expect PASS** (the migrator applies all embedded migrations when the test store boots).

Run: `go test ./internal/store/ -run TestMigration015CreatesProjectKEKVersions -v`
Expected: PASS. If it fails with "table not created", confirm the two SQL files are named exactly `000015_project_kek_versions.up.sql` / `.down.sql` and contain the SQL above.

- [ ] **Step 4: Commit.**
```bash
git add migrations/000015_project_kek_versions.up.sql migrations/000015_project_kek_versions.down.sql internal/store/project_kek_versions_migration_test.go
git commit -m "feat(store): project_kek_versions table (migration 000015)"
```

---

### Task 2: `ProjectKEKVersionRepo` (superseded-version storage)

**Files:**
- Create: `internal/store/project_kek_versions.go`
- Test: `internal/store/project_kek_versions_test.go`

**Context:** Holds superseded KEK versions. `ProjectRepo` is `struct{ s *Store }` with `NewProjectRepo(s)`. Follow that shape. `mapError`, `ErrNotFound` are in the package.

- [ ] **Step 1: Write the failing test.** Uses the existing harness helpers `requireStore(t)`, `resetDB(t)`, and `mkProject` — check `internal/store/*_test.go` for the exact project-seeding helper; if none exists, create a project inline via `NewProjectRepo(s).Create(ctx, id, slug, name, wrappedKEK, kekVersion)` where `id` comes from `s.NewID(ctx)`.

`internal/store/project_kek_versions_test.go`:
```go
package store

import (
	"context"
	"testing"
)

func TestProjectKEKVersionsInsertGetListDelete(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	pr := NewProjectRepo(s)
	kr := NewProjectKEKVersionRepo(s)

	id, err := s.NewID(ctx)
	if err != nil { t.Fatal(err) }
	if _, err := pr.Create(ctx, id, "proj", "Proj", []byte("wrapped-latest"), 2); err != nil {
		t.Fatal(err)
	}
	// Superseded version 1 with a wrapped blob.
	if err := kr.Insert(ctx, s.pool, id, 1, []byte("wrapped-v1")); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := kr.GetWrapped(ctx, id, 1)
	if err != nil || string(got) != "wrapped-v1" {
		t.Fatalf("GetWrapped = %q, %v", got, err)
	}
	if _, err := kr.GetWrapped(ctx, id, 99); err != ErrNotFound {
		t.Fatalf("GetWrapped(missing) = %v, want ErrNotFound", err)
	}
	// No DEKs reference version 1 → it is "empty" and pending list shows count 0.
	pend, err := kr.ListPending(ctx, id)
	if err != nil { t.Fatal(err) }
	if len(pend) != 1 || pend[0].Version != 1 || pend[0].DEKCount != 0 {
		t.Fatalf("ListPending = %+v", pend)
	}
	// DeleteEmpty removes version 1 (0 referencing DEKs) and returns it.
	retired, err := kr.DeleteEmpty(ctx, id)
	if err != nil { t.Fatal(err) }
	if len(retired) != 1 || retired[0] != 1 {
		t.Fatalf("DeleteEmpty = %v, want [1]", retired)
	}
	if _, err := kr.GetWrapped(ctx, id, 1); err != ErrNotFound {
		t.Fatalf("after delete GetWrapped = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** ("undefined: NewProjectKEKVersionRepo").
Run: `go test ./internal/store/ -run TestProjectKEKVersionsInsertGetListDelete -v`

- [ ] **Step 3: Implement.**

`internal/store/project_kek_versions.go`:
```go
package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgconn"
)

// ProjectKEKVersionRepo persists superseded project-KEK versions (wrapped
// under the master) that are still referenced by not-yet-rewrapped DEKs.
type ProjectKEKVersionRepo struct{ s *Store }

func NewProjectKEKVersionRepo(s *Store) *ProjectKEKVersionRepo { return &ProjectKEKVersionRepo{s: s} }

// PendingVersion is a superseded KEK version and how many DEKs still point at it.
type PendingVersion struct {
	Version  int
	DEKCount int
}

// execer is satisfied by *pgxpool.Pool and pgx.Tx, so Insert works inside a
// rotate transaction or standalone.
type execer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// Insert records a superseded (project, version) wrapped KEK. Caller passes the
// pool or a tx.
func (r *ProjectKEKVersionRepo) Insert(ctx context.Context, ex execer, projectID string, version int, wrappedKEK []byte) error {
	_, err := ex.Exec(ctx,
		`INSERT INTO project_kek_versions (project_id, version, wrapped_kek)
		 VALUES ($1::uuid, $2, $3)`, projectID, version, wrappedKEK)
	return mapError(err)
}

// GetWrapped returns the wrapped KEK blob for a superseded version, or
// ErrNotFound.
func (r *ProjectKEKVersionRepo) GetWrapped(ctx context.Context, projectID string, version int) ([]byte, error) {
	var b []byte
	err := r.s.pool.QueryRow(ctx,
		`SELECT wrapped_kek FROM project_kek_versions WHERE project_id=$1::uuid AND version=$2`,
		projectID, version).Scan(&b)
	if err != nil {
		return nil, mapError(err)
	}
	return b, nil
}

// ListPending returns every superseded version for a project with the count of
// secret_values DEKs still wrapped at that version, oldest version first.
func (r *ProjectKEKVersionRepo) ListPending(ctx context.Context, projectID string) ([]PendingVersion, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT v.version,
		        (SELECT count(*) FROM secret_values sv
		           JOIN configs c ON c.id = sv.config_id
		          WHERE c.project_id = $1::uuid AND sv.dek_key_version = v.version)
		   FROM project_kek_versions v
		  WHERE v.project_id = $1::uuid
		  ORDER BY v.version ASC`, projectID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	out := []PendingVersion{}
	for rows.Next() {
		var p PendingVersion
		if err := rows.Scan(&p.Version, &p.DEKCount); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, mapError(rows.Err())
}

// DeleteEmpty removes superseded versions no DEK references anymore and returns
// the deleted version numbers.
func (r *ProjectKEKVersionRepo) DeleteEmpty(ctx context.Context, projectID string) ([]int, error) {
	rows, err := r.s.pool.Query(ctx,
		`DELETE FROM project_kek_versions v
		  WHERE v.project_id = $1::uuid
		    AND NOT EXISTS (
		      SELECT 1 FROM secret_values sv
		        JOIN configs c ON c.id = sv.config_id
		       WHERE c.project_id = $1::uuid AND sv.dek_key_version = v.version)
		 RETURNING version`, projectID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	out := []int{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, mapError(rows.Err())
}
```

- [ ] **Step 4: Run — expect PASS.**
Run: `go test ./internal/store/ -run TestProjectKEKVersionsInsertGetListDelete -v`

- [ ] **Step 5: Commit.**
```bash
git add internal/store/project_kek_versions.go internal/store/project_kek_versions_test.go
git commit -m "feat(store): ProjectKEKVersionRepo (insert/get/list-pending/delete-empty)"
```

---

### Task 3: `ProjectRepo.RotateKEK` (atomic version bump)

**Files:**
- Modify: `internal/store/projects.go`
- Test: `internal/store/projects_rotate_test.go`

**Context:** One tx: lock the project row, read `(kek_version V, wrapped_kek W)`, preserve `(V, W)` into `project_kek_versions`, call a caller-supplied `wrapNew` closure (which does the keyring wrap — no DB) to produce the new wrapped KEK, then update the project to `(V+1, newWrapped)`. Keeps the crypto in the caller and the atomic DB work in the store. Uses `r.s.withTx`.

- [ ] **Step 1: Write the failing test.** `wrapNew` is stubbed to a fixed blob; assert the version bumps, the project row updates, and the old version is preserved.

`internal/store/projects_rotate_test.go`:
```go
package store

import (
	"context"
	"testing"
)

func TestProjectRepoRotateKEK(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	pr := NewProjectRepo(s)
	kvr := NewProjectKEKVersionRepo(s)

	id, err := s.NewID(ctx)
	if err != nil { t.Fatal(err) }
	if _, err := pr.Create(ctx, id, "p", "P", []byte("wrapped-v1"), 1); err != nil {
		t.Fatal(err)
	}

	newVer, err := pr.RotateKEK(ctx, id, func(oldVersion int) ([]byte, error) {
		if oldVersion != 1 {
			t.Fatalf("wrapNew got oldVersion %d, want 1", oldVersion)
		}
		return []byte("wrapped-v2"), nil
	})
	if err != nil { t.Fatalf("RotateKEK: %v", err) }
	if newVer != 2 { t.Fatalf("newVer = %d, want 2", newVer) }

	got, err := pr.Get(ctx, id)
	if err != nil { t.Fatal(err) }
	if got.KEKVersion != 2 || string(got.WrappedKEK) != "wrapped-v2" {
		t.Fatalf("project after rotate = v%d %q", got.KEKVersion, got.WrappedKEK)
	}
	old, err := kvr.GetWrapped(ctx, id, 1)
	if err != nil || string(old) != "wrapped-v1" {
		t.Fatalf("preserved v1 = %q, %v", old, err)
	}

	// Not found for unknown / soft-deleted project.
	if _, err := pr.RotateKEK(ctx, "00000000-0000-0000-0000-000000000000", func(int) ([]byte, error) { return nil, nil }); err != ErrNotFound {
		t.Fatalf("RotateKEK(missing) = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** ("RotateKEK undefined").
Run: `go test ./internal/store/ -run TestProjectRepoRotateKEK -v`

- [ ] **Step 3: Implement `RotateKEK` in `internal/store/projects.go`.** Add the import `"github.com/jackc/pgx/v5"` and append:
```go
// RotateKEK atomically installs a new KEK version for a live project. It locks
// the project row, preserves the current (version, wrapped_kek) into
// project_kek_versions, then calls wrapNew(oldVersion) to obtain the newly
// wrapped KEK (the caller does the keyring wrap; no DB access in the closure)
// and updates the project to version+1. Returns the new version, or ErrNotFound
// if the project does not exist or is soft-deleted.
func (r *ProjectRepo) RotateKEK(ctx context.Context, id string, wrapNew func(oldVersion int) (newWrapped []byte, err error)) (int, error) {
	var newVersion int
	err := r.s.withTx(ctx, func(tx pgx.Tx) error {
		var oldVersion int
		var oldWrapped []byte
		row := tx.QueryRow(ctx,
			`SELECT kek_version, wrapped_kek FROM projects
			  WHERE id=$1::uuid AND deleted_at IS NULL FOR UPDATE`, id)
		if err := row.Scan(&oldVersion, &oldWrapped); err != nil {
			return mapError(err) // pgx.ErrNoRows -> ErrNotFound
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO project_kek_versions (project_id, version, wrapped_kek)
			 VALUES ($1::uuid, $2, $3)`, id, oldVersion, oldWrapped); err != nil {
			return mapError(err)
		}
		newWrapped, err := wrapNew(oldVersion)
		if err != nil {
			return err
		}
		newVersion = oldVersion + 1
		tag, err := tx.Exec(ctx,
			`UPDATE projects SET wrapped_kek=$2, kek_version=$3, updated_at=now()
			  WHERE id=$1::uuid`, id, newWrapped, newVersion)
		if err != nil {
			return mapError(err)
		}
		if tag.RowsAffected() != 1 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return newVersion, nil
}
```

- [ ] **Step 4: Run — expect PASS.**
Run: `go test ./internal/store/ -run TestProjectRepoRotateKEK -v`

- [ ] **Step 5: Commit.**
```bash
git add internal/store/projects.go internal/store/projects_rotate_test.go
git commit -m "feat(store): ProjectRepo.RotateKEK atomic version bump"
```

---

### Task 4: `SecretRepo.RewrapBatch` (resumable DEK re-wrap iterator)

**Files:**
- Create: `internal/store/secrets_rewrap.go`
- Test: `internal/store/secrets_rewrap_test.go`

**Context:** Loads one keyset-paginated batch of `secret_values` rows for a project with `dek_key_version < latest`, across ALL configs (join `configs`), **including rows of soft-deleted configs/secrets** (do NOT filter on `deleted_at`). For each row it calls `rewrap(row)` (the service unwraps the old DEK and wraps a new one) and updates `wrapped_dek` + `dek_key_version` in the same tx. **It must SELECT only `id, config_id, key, value_version, wrapped_dek, dek_key_version` — never `ciphertext`/`nonce`** (rotation never decrypts values). Returns processed count and the next cursor (empty when done).

- [ ] **Step 1: Write the failing test.** Seed a project + config + two secret values at `dek_key_version=1`, set project latest to 2, and rewrap with a stub that returns a marker; assert both rows advance to version 2 with the new blob, and a second call processes 0.

`internal/store/secrets_rewrap_test.go`:
```go
package store

import (
	"context"
	"fmt"
	"testing"
)

func TestSecretRepoRewrapBatch(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()

	// Seed project + env + config + 2 secret values at dek_key_version=1.
	// Reuse the existing secrets test harness helpers to build the project/env/config;
	// read internal/store/secrets_test.go for mkConfig/seedSecretValue-style helpers
	// and use them here. The two secret_values must have dek_key_version=1.
	projectID, configID := seedProjectConfig(t, s) // helper: see note below
	for i, k := range []string{"A", "B"} {
		insertSecretValue(t, s, configID, k, 1, []byte(fmt.Sprintf("wrapped-old-%d", i)), 1)
	}
	// Bump the project's latest KEK version to 2 (as RotateKEK would).
	if _, err := s.pool.Exec(ctx, `UPDATE projects SET kek_version=2 WHERE id=$1::uuid`, projectID); err != nil {
		t.Fatal(err)
	}

	sr := NewSecretRepo(s)
	seen := map[string]bool{}
	processed, next, err := sr.RewrapBatch(ctx, projectID, 2, "", 100,
		func(row RewrapRow) ([]byte, error) {
			seen[row.Key] = true
			if row.DEKKeyVersion != 1 {
				t.Fatalf("row %s at version %d, want 1", row.Key, row.DEKKeyVersion)
			}
			return []byte("wrapped-new-" + row.Key), nil
		})
	if err != nil { t.Fatalf("RewrapBatch: %v", err) }
	if processed != 2 || next != "" {
		t.Fatalf("processed=%d next=%q, want 2 and empty", processed, next)
	}
	if !seen["A"] || !seen["B"] {
		t.Fatalf("did not process both keys: %v", seen)
	}
	// Second call: nothing left below version 2.
	processed2, _, err := sr.RewrapBatch(ctx, projectID, 2, "", 100, func(RewrapRow) ([]byte, error) {
		t.Fatal("rewrap called with no pending rows")
		return nil, nil
	})
	if err != nil || processed2 != 0 {
		t.Fatalf("second RewrapBatch processed=%d err=%v, want 0", processed2, err)
	}
}
```

> **Helper note for the implementer:** `seedProjectConfig` and `insertSecretValue` are shorthands — before writing this test, read `internal/store/secrets_test.go` and reuse whatever project/env/config seeding + secret_value insert helpers already exist there (e.g. an `mkConfig`/`mkProject` + a direct `INSERT INTO secret_values`). If none exist, write two tiny local helpers in this test file that (a) create project+env+config via the existing repos and (b) `INSERT INTO secret_values (id, config_id, key, value_version, wrapped_dek, ciphertext, nonce, dek_key_version) VALUES (...)` with dummy ciphertext/nonce and the given wrapped_dek/dek_key_version.

- [ ] **Step 2: Run — expect FAIL** ("RewrapBatch/RewrapRow undefined"). (Confirm the `SecretRepo` constructor name via `grep -n "func NewSecretRepo" internal/store/secrets.go`; if it differs, use the real name.)
Run: `go test ./internal/store/ -run TestSecretRepoRewrapBatch -v`

- [ ] **Step 3: Implement.**

`internal/store/secrets_rewrap.go`:
```go
package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// RewrapRow is one secret_values DEK to re-wrap. It deliberately excludes the
// value ciphertext/nonce — rotation re-wraps the DEK only and never decrypts a
// secret value.
type RewrapRow struct {
	ID            string
	ConfigID      string
	Key           string
	ValueVersion  int
	WrappedDEK    []byte
	DEKKeyVersion int
}

// RewrapBatch processes up to limit secret_values rows for a project whose
// dek_key_version < latest (across all configs, INCLUDING soft-deleted ones),
// keyset-paginated by secret_values.id ascending. For each row it calls
// rewrap(row) to obtain the DEK re-wrapped under the latest KEK, then updates
// wrapped_dek and dek_key_version in the same transaction. Returns the number
// of rows processed and the next cursor (the last id processed; "" when the
// batch was not full, i.e. no more rows). Re-running from "" resumes safely
// because processed rows are no longer < latest.
func (r *SecretRepo) RewrapBatch(ctx context.Context, projectID string, latest int, cursor string, limit int,
	rewrap func(RewrapRow) (newWrappedDEK []byte, err error)) (processed int, next string, err error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	err = r.s.withTx(ctx, func(tx pgx.Tx) error {
		rows, qerr := tx.Query(ctx,
			`SELECT sv.id::text, sv.config_id::text, sv.key, sv.value_version, sv.wrapped_dek, sv.dek_key_version
			   FROM secret_values sv
			   JOIN configs c ON c.id = sv.config_id
			  WHERE c.project_id = $1::uuid
			    AND sv.dek_key_version < $2
			    AND ($3 = '' OR sv.id > $3::uuid)
			  ORDER BY sv.id ASC
			  LIMIT $4`, projectID, latest, cursor, limit)
		if qerr != nil {
			return mapError(qerr)
		}
		var batch []RewrapRow
		for rows.Next() {
			var rr RewrapRow
			if err := rows.Scan(&rr.ID, &rr.ConfigID, &rr.Key, &rr.ValueVersion, &rr.WrappedDEK, &rr.DEKKeyVersion); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, rr)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return mapError(err)
		}
		for _, rr := range batch {
			newWrapped, rerr := rewrap(rr)
			if rerr != nil {
				return rerr
			}
			tag, uerr := tx.Exec(ctx,
				`UPDATE secret_values SET wrapped_dek=$2, dek_key_version=$3 WHERE id=$1::uuid`,
				rr.ID, newWrapped, latest)
			if uerr != nil {
				return mapError(uerr)
			}
			if tag.RowsAffected() != 1 {
				return ErrNotFound
			}
			processed++
			next = rr.ID
		}
		return nil
	})
	if err != nil {
		return 0, "", err
	}
	if processed < limit {
		next = "" // batch not full → no more rows
	}
	return processed, next, nil
}
```

- [ ] **Step 4: Run — expect PASS.**
Run: `go test ./internal/store/ -run TestSecretRepoRewrapBatch -v`

- [ ] **Step 5: Commit.**
```bash
git add internal/store/secrets_rewrap.go internal/store/secrets_rewrap_test.go
git commit -m "feat(store): SecretRepo.RewrapBatch resumable DEK re-wrap iterator"
```

---

### Task 5: `internal/projectkeys` service — Rotate / Rewrap / Status (crypto core)

**Files:**
- Create: `internal/projectkeys/service.go`
- Test: `internal/projectkeys/service_test.go`

**Context:** The orchestration heart. Holds the keyring + repos. This task carries the crypto-correctness burden (aim for 100% branch on the wrap/unwrap paths). No new crypto primitives — reuse `Keyring.WrapProjectKEK`/`UnwrapProjectKEK`, `crypto.GenerateKey`, `crypto.WrapKey`/`UnwrapKey`, `crypto.ParseCiphertext`, `Ciphertext.Marshal`, `crypto.DEKAAD`.

Dependencies (interfaces so the test can use the real store):
- `*crypto.Keyring`
- `*store.ProjectRepo` (Get, RotateKEK)
- `*store.ProjectKEKVersionRepo` (Insert is done inside RotateKEK's tx; here we need GetWrapped, ListPending, DeleteEmpty)
- `*store.SecretRepo` (RewrapBatch)

- [ ] **Step 1: Write the failing test** — the end-to-end crypto property test against real Postgres + a real keyring. This is the crown-jewel test.

`internal/projectkeys/service_test.go` (core cases; expand per the checklist below):
```go
package projectkeys

import (
	"context"
	"testing"
	// import the store test harness pattern: this test needs a real Store
	// (testcontainers) + a real unsealed crypto.Keyring + a secrets.Service to
	// write/read a secret. Read internal/secrets/*_test.go for how those tests
	// build a Service (keyring.Unseal with a random 32-byte master, store repos).
)

// TestRotateThenReadThenRewrapThenRetire is the end-to-end property:
//  1. write a secret (DEK at version 1)
//  2. rotate the project KEK (now latest=2; old version 1 preserved)
//  3. the secret still decrypts (via the retained version-1 KEK)
//  4. rewrap advances the DEK to version 2 and retires version 1
//  5. the secret still decrypts (now via version 2), and version 1 is gone
func TestRotateThenReadThenRewrapThenRetire(t *testing.T) {
	// ... build store + keyring + secrets.Service + projectkeys.Service ...
	// secretSvc.SetSecrets(ctx, configID, [{Key:"DB", Value:[]byte("s3cr3t")}], actor, actor)
	// svc := New(keyring, projRepo, kekVerRepo, secretRepo)
	// before, _ := secretSvc.reveal("DB")            // == "s3cr3t"
	// v, _ := svc.Rotate(ctx, projectID)             // v == 2
	// mid, _ := secretSvc.reveal("DB")               // still "s3cr3t" (retained v1)
	// res, _ := svc.Rewrap(ctx, projectID)           // Rewrapped==1, Retired==[1]
	// after, _ := secretSvc.reveal("DB")             // still "s3cr3t" (via v2)
	// status, _ := svc.Status(ctx, projectID)        // CurrentVersion==2, Pending empty
	t.Skip("fill in using the secrets test harness; assertions listed in comments")
}
```

> The implementer replaces the `t.Skip` with the real wiring, reusing the secrets package's test harness (a `secrets.Service` + `crypto.Keyring` + testcontainers store). Add the checklist cases below as separate tests.

**Required test cases (each an assertion or a test):**
- rotate→read (retained version) → still decrypts.
- rewrap→read (new version) → still decrypts; `Rewrapped==1`, `Retired==[1]`; `project_kek_versions` now empty (Status `Pending` empty).
- rewrap when nothing pending → `Rewrapped==0`, `Retired==[]` (no-op).
- **nonce uniqueness:** the re-wrapped `wrapped_dek` nonce differs from the original (parse both `Ciphertext` and compare `.Nonce`).
- **tamper:** corrupt a stored `wrapped_dek` byte, then `Rewrap` returns an error naming the row id, and does not panic or leak key bytes.
- **crash-resume:** run `Rewrap` with an injected failure after the first batch (e.g. small batch size + a rewrap closure that errors on the 2nd row), assert some rows advanced and re-running `Rewrap` completes the rest and retires the version.
- **no value decryption:** assert `RewrapRow` has no ciphertext field and the service never calls `crypto.Decrypt`/`decryptValue` during rewrap (structural — code review + a test that rewrap succeeds even if the value ciphertext bytes are garbage, proving values are never opened).
- sealed keyring → `Rotate`/`Rewrap` return `crypto.ErrSealed`.

- [ ] **Step 2: Run — expect FAIL** (package/New undefined).
Run: `go test ./internal/projectkeys/ -v`

- [ ] **Step 3: Implement `internal/projectkeys/service.go`.**
```go
// Package projectkeys rotates a project's KEK and lazily re-wraps its DEKs.
// It never decrypts a secret value: rotation unwraps and re-wraps 32-byte DEKs
// only. All key material is zeroed after use.
package projectkeys

import (
	"context"
	"fmt"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

type Service struct {
	kr      *crypto.Keyring
	proj    *store.ProjectRepo
	kekVers *store.ProjectKEKVersionRepo
	secrets *store.SecretRepo
}

func New(kr *crypto.Keyring, proj *store.ProjectRepo, kekVers *store.ProjectKEKVersionRepo, secrets *store.SecretRepo) *Service {
	return &Service{kr: kr, proj: proj, kekVers: kekVers, secrets: secrets}
}

// Rotate installs a fresh KEK version for the project. Instant; existing DEKs
// stay readable via the retained old version until Rewrap moves them.
func (s *Service) Rotate(ctx context.Context, projectID string) (int, error) {
	return s.proj.RotateKEK(ctx, projectID, func(oldVersion int) ([]byte, error) {
		kek, err := crypto.GenerateKey()
		if err != nil {
			return nil, err
		}
		defer zero(kek)
		ct, err := s.kr.WrapProjectKEK(kek, projectID)
		if err != nil {
			return nil, err // includes crypto.ErrSealed
		}
		return ct.Marshal(), nil
	})
}

// RewrapResult reports a completed rewrap sweep.
type RewrapResult struct {
	Rewrapped int
	Retired   []int
}

// Rewrap re-wraps every DEK still under an old KEK version onto the latest KEK,
// then retires emptied versions. Resumable and idempotent.
func (s *Service) Rewrap(ctx context.Context, projectID string) (RewrapResult, error) {
	p, err := s.proj.Get(ctx, projectID)
	if err != nil {
		return RewrapResult{}, err
	}
	latest := p.KEKVersion

	// Unwrap the latest KEK once; cache unwrapped old-version KEKs by version.
	latestCT, err := crypto.ParseCiphertext(p.WrappedKEK)
	if err != nil {
		return RewrapResult{}, err
	}
	latestKEK, err := s.kr.UnwrapProjectKEK(latestCT, projectID)
	if err != nil {
		return RewrapResult{}, err // ErrSealed etc.
	}
	defer zero(latestKEK)
	oldKEKs := map[int][]byte{}
	defer func() {
		for _, k := range oldKEKs {
			zero(k)
		}
	}()
	kekForVersion := func(v int) ([]byte, error) {
		if v == latest {
			return latestKEK, nil
		}
		if k, ok := oldKEKs[v]; ok {
			return k, nil
		}
		wrapped, err := s.kekVers.GetWrapped(ctx, projectID, v)
		if err != nil {
			return nil, err
		}
		ct, err := crypto.ParseCiphertext(wrapped)
		if err != nil {
			return nil, err
		}
		k, err := s.kr.UnwrapProjectKEK(ct, projectID)
		if err != nil {
			return nil, err
		}
		oldKEKs[v] = k
		return k, nil
	}

	total := 0
	cursor := ""
	for {
		processed, next, err := s.secrets.RewrapBatch(ctx, projectID, latest, cursor, 200,
			func(row store.RewrapRow) ([]byte, error) {
				// AAD binds project/path/value-version — identical for unwrap-old and wrap-new.
				aad := crypto.DEKAAD(projectID, row.ConfigID+"/"+row.Key, uint64(row.ValueVersion))
				oldKEK, err := kekForVersion(row.DEKKeyVersion)
				if err != nil {
					return nil, err
				}
				dekCT, err := crypto.ParseCiphertext(row.WrappedDEK)
				if err != nil {
					return nil, fmt.Errorf("rewrap: parse wrapped_dek for %s: %w", row.ID, err)
				}
				dek, err := crypto.UnwrapKey(oldKEK, dekCT, aad)
				if err != nil {
					return nil, fmt.Errorf("rewrap: unwrap dek for %s: %w", row.ID, err)
				}
				defer zero(dek)
				newCT, err := crypto.WrapKey(latestKEK, dek, aad) // fresh random nonce
				if err != nil {
					return nil, err
				}
				return newCT.Marshal(), nil
			})
		if err != nil {
			return RewrapResult{}, err
		}
		total += processed
		if next == "" {
			break
		}
		cursor = next
	}

	retired, err := s.kekVers.DeleteEmpty(ctx, projectID)
	if err != nil {
		return RewrapResult{}, err
	}
	return RewrapResult{Rewrapped: total, Retired: retired}, nil
}

// Status reports the current version and any pending (superseded) versions.
type Status struct {
	CurrentVersion int
	Pending        []store.PendingVersion
}

func (s *Service) StatusFor(ctx context.Context, projectID string) (Status, error) {
	p, err := s.proj.Get(ctx, projectID)
	if err != nil {
		return Status{}, err
	}
	pend, err := s.kekVers.ListPending(ctx, projectID)
	if err != nil {
		return Status{}, err
	}
	return Status{CurrentVersion: p.KEKVersion, Pending: pend}, nil
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
```

> **Error-message safety:** the `%w`/`%s` in rewrap errors interpolate only the row **id** (a uuid), never key material — keep it that way.

- [ ] **Step 4: Run — expect PASS** (fill in the harness so the property tests run).
Run: `go test ./internal/projectkeys/ -v`

- [ ] **Step 5: Commit.**
```bash
git add internal/projectkeys/
git commit -m "feat(projectkeys): Rotate/Rewrap/Status service (lazy DEK re-wrap, value-free)"
```

---

### Task 6: Secrets read path — resolve KEK per `dek_key_version`

**Files:**
- Modify: `internal/secrets/secrets.go` (`decryptValue`, `unwrapProjectKEK` sites), `internal/secrets/rawread.go`
- Modify: the secrets `Service` struct + constructor to hold a `*store.ProjectKEKVersionRepo`
- Test: `internal/secrets/rotation_read_test.go`

**Context:** After a rotate, an un-rewrapped DEK is at `dek_key_version < proj.KEKVersion`, and its KEK now lives in `project_kek_versions`. The read path must unwrap the KEK matching each value's `dek_key_version`. Introduce one caching resolver used by every read site. Callers currently do `kek, _ := s.unwrapProjectKEK(proj)` then `s.decryptValue(proj, cfgID, sv, kek)` at `rawread.go:58/64` and `secrets.go:43,115/120,137/144,182/187`.

- [ ] **Step 1: Write the failing test.** A secret written at version 1, then `project_kek_versions` seeded with the old wrapped KEK and the project bumped to version 2, must still decrypt.
```go
// internal/secrets/rotation_read_test.go
// Using the secrets test harness: write "DB"="s3cr3t" (dek_key_version=1).
// Simulate a rotate WITHOUT rewrap: generate a NEW project KEK, wrap it, move the
// OLD wrapped_kek into project_kek_versions(version=1), UPDATE projects to the new
// wrapped_kek + kek_version=2. Then reveal "DB" and assert it still returns "s3cr3t"
// (the read path unwraps version 1 from project_kek_versions).
```

- [ ] **Step 2: Run — expect FAIL** (read returns a decrypt error because it tries the latest KEK on a version-1 DEK).
Run: `go test ./internal/secrets/ -run RotationRead -v`

- [ ] **Step 3: Implement the resolver.** Add a `kekVers *store.ProjectKEKVersionRepo` field to the secrets `Service` and its constructor (update all constructor call sites — `grep -rn "secrets.New(" .`). Add to `internal/secrets/secrets.go`:
```go
// kekResolver unwraps and caches project KEKs by version for the lifetime of one
// read. Version == proj.KEKVersion uses proj.WrappedKEK; older versions are
// loaded from project_kek_versions. Call zero() when the read completes.
type kekResolver struct {
	s     *Service
	proj  *store.Project
	cache map[int][]byte
}

func (s *Service) newKEKResolver(proj *store.Project) *kekResolver {
	return &kekResolver{s: s, proj: proj, cache: map[int][]byte{}}
}

func (kr *kekResolver) forVersion(ctx context.Context, version int) ([]byte, error) {
	if k, ok := kr.cache[version]; ok {
		return k, nil
	}
	var wrapped []byte
	if version == kr.proj.KEKVersion {
		wrapped = kr.proj.WrappedKEK
	} else {
		b, err := kr.s.kekVers.GetWrapped(ctx, kr.proj.ID, version)
		if err != nil {
			return nil, mapStoreErr(err)
		}
		wrapped = b
	}
	ct, err := crypto.ParseCiphertext(wrapped)
	if err != nil {
		return nil, ErrDecrypt
	}
	kek, err := kr.s.keyring.UnwrapProjectKEK(ct, kr.proj.ID)
	if err != nil {
		return nil, mapCryptoErr(err)
	}
	kr.cache[version] = kek
	return kek, nil
}

func (kr *kekResolver) zero() {
	for _, k := range kr.cache {
		zeroize(k)
	}
}
```
Change `decryptValue` to resolve the KEK by the value's version (thread `ctx` + resolver instead of a fixed `kek`):
```go
func (s *Service) decryptValue(ctx context.Context, proj *store.Project, configID string, sv store.SecretValue, res *kekResolver) ([]byte, error) {
	aad, err := dekAAD(proj.ID, configID+"/"+sv.Key, sv.ValueVersion)
	if err != nil {
		return nil, err
	}
	kek, err := res.forVersion(ctx, sv.DEKKeyVersion)
	if err != nil {
		return nil, err
	}
	dekCT, err := crypto.ParseCiphertext(sv.WrappedDEK)
	if err != nil {
		return nil, ErrDecrypt
	}
	dek, err := crypto.UnwrapKey(kek, dekCT, aad)
	if err != nil {
		return nil, mapCryptoErr(err)
	}
	defer zeroize(dek)
	pt, err := crypto.Decrypt(dek, crypto.Ciphertext{Nonce: sv.Nonce, Data: sv.Ciphertext}, aad)
	if err != nil {
		return nil, mapCryptoErr(err)
	}
	return pt, nil
}
```
Update EVERY read site (`rawread.go:58/64`, `secrets.go:43,115/120,137/144,182/187`): replace `kek, err := s.unwrapProjectKEK(proj)` + per-value `s.decryptValue(proj, cfgID, sv, kek)` with:
```go
res := s.newKEKResolver(proj)
defer res.zero()
// ... per value:
pt, err := s.decryptValue(ctx, proj, cfgID, sv, res)
```
Remove the now-unused `unwrapProjectKEK` if nothing else calls it (grep first; the write path in `SetSecrets` still needs the LATEST KEK — for writes keep using `res.forVersion(ctx, proj.KEKVersion)` or leave `unwrapProjectKEK` for the write path only). Keep `unwrapProjectKEK` if the write path uses it; only the READ sites switch to the resolver.

- [ ] **Step 4: Run — expect PASS**, and run the whole secrets suite to catch call-site regressions.
Run: `go test ./internal/secrets/ -v`
Expected: all PASS (existing read/write tests unaffected; the new rotation-read test passes).

- [ ] **Step 5: Commit.**
```bash
git add internal/secrets/
git commit -m "feat(secrets): resolve project KEK per dek_key_version on read (rotation-aware)"
```

---

### Task 7: authz action + API handlers + routes

**Files:**
- Modify: `internal/authz/actions.go`
- Create: `internal/api/kek_handlers.go`
- Modify: `internal/api/server.go` (routes) and wire a `*projectkeys.Service` into `Server` (constructor)
- Test: `internal/api/kek_e2e_test.go`

**Context:** Owner-only. Add a `KEKManage` action to `ownerActions` ONLY. Handlers mirror `projects_handlers.go`. Routes under `/v1/projects/{pid}/kek/...`.

- [ ] **Step 1: Add the action.** In `internal/authz/actions.go`, add the const in the block:
```go
	KEKManage      Action = "kek:manage"      // project-scoped, owner-only
```
and change `ownerActions` to include it:
```go
	ownerActions = union(adminActions, setOf(ProjectDelete, KEKManage))
```

- [ ] **Step 2: Write the failing handler test** (mirror `internal/api/projects_e2e_test.go`): an owner token can `POST /v1/projects/{pid}/kek/rotate` → 200 with `kek_version:2`; a non-owner (admin) token → 403; `POST .../kek/rewrap` after a rotate → 200 `remaining:0`; `GET .../kek` → 200 with `current_version`; a sealed server → 503 on rotate. (Reuse the e2e harness that builds a booted Server with a real keyring + store and mints role-scoped tokens.)
Run: `go test ./internal/api/ -run KEK -v`  (expect FAIL — handlers undefined)

- [ ] **Step 3: Implement `internal/api/kek_handlers.go`.**
```go
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
)

func (s *Server) handleKEKRotate(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	if !s.authorize(w, r, authz.KEKManage, authz.Resource{ProjectID: pid}, "project.kek.rotate", "projects/"+pid+"/kek") {
		return
	}
	newVersion, err := s.projectKeys.Rotate(r.Context(), pid)
	if err != nil {
		s.writeServiceError(w, err) // maps ErrSealed->503, ErrNotFound->404
		return
	}
	if err := s.record(r, "project.kek.rotate", "projects/"+pid+"/kek", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"kek_version": newVersion})
}

func (s *Server) handleKEKRewrap(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	if !s.authorize(w, r, authz.KEKManage, authz.Resource{ProjectID: pid}, "project.kek.rewrap", "projects/"+pid+"/kek") {
		return
	}
	res, err := s.projectKeys.Rewrap(r.Context(), pid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "project.kek.rewrap", "projects/"+pid+"/kek", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rewrapped": res.Rewrapped, "retired_versions": res.Retired, "remaining": 0,
	})
}

func (s *Server) handleKEKStatus(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	if err := s.can(r, authz.KEKManage, authz.Resource{ProjectID: pid}); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	st, err := s.projectKeys.StatusFor(r.Context(), pid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	pend := make([]map[string]any, 0, len(st.Pending))
	for _, p := range st.Pending {
		pend = append(pend, map[string]any{"version": p.Version, "dek_count": p.DEKCount})
	}
	writeJSON(w, http.StatusOK, map[string]any{"current_version": st.CurrentVersion, "pending": pend})
}
```

- [ ] **Step 4: Wire the service + routes.** Add a `projectKeys *projectkeys.Service` field to `Server` (in `internal/api/server.go`), construct it in `Boot`/`New` where the keyring + store are available (`projectkeys.New(kr, store.NewProjectRepo(st), store.NewProjectKEKVersionRepo(st), store.NewSecretRepo(st))`), and register routes next to the project routes:
```go
r.Post("/v1/projects/{pid}/kek/rotate", s.handleKEKRotate)
r.Post("/v1/projects/{pid}/kek/rewrap", s.handleKEKRewrap)
r.Get("/v1/projects/{pid}/kek", s.handleKEKStatus)
```
Confirm `s.writeServiceError` maps `crypto.ErrSealed`→503 and `store.ErrNotFound`→404 (read `internal/api/service_errors.go`); if `Rotate` returns a raw `crypto.ErrSealed`, ensure the mapping catches it (it does per `service_errors.go:19`).

- [ ] **Step 5: Run — expect PASS.**
Run: `go test ./internal/api/ -run KEK -v`

- [ ] **Step 6: Commit.**
```bash
git add internal/authz/actions.go internal/api/kek_handlers.go internal/api/server.go internal/api/kek_e2e_test.go
git commit -m "feat(api): owner-only project KEK rotate/rewrap/status endpoints"
```

---

### Task 8: CLI — `janus project rotate-kek | rewrap | kek-status`

**Files:**
- Create: `cmd/janus/project_commands.go`
- Modify: `cmd/janus/main.go` (register)
- Test: `cmd/janus/project_commands_test.go`

**Context:** Mirror `cmd/janus/rotation_commands.go`: a `newProjectCmd()` with `--address`/`--token` persistent flags, `newAPIClient`, `c.call`. Register via `root.AddCommand(newProjectCmd())` in `main.go` (the `root.AddCommand(...)` block at `main.go:25`).

- [ ] **Step 1: Write the failing test** (mirror the rotation CLI test): with a stub server returning `{"kek_version":2}`, `janus project rotate-kek <pid>` prints the new version. (If the CLI tests hit a real booted server via the e2e harness, use that instead — match the sibling test's style.)
Run: `go test ./cmd/janus/ -run Project -v`  (expect FAIL)

- [ ] **Step 2: Implement `cmd/janus/project_commands.go`.**
```go
package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newProjectCmd() *cobra.Command {
	var address, token string
	cmd := &cobra.Command{Use: "project", Short: "Project key management"}
	cmd.PersistentFlags().StringVar(&address, "address", "", "server address (default: stored/env)")
	cmd.PersistentFlags().StringVar(&token, "token", "", "service token (default: stored/env)")

	rotate := &cobra.Command{
		Use:   "rotate-kek <project-id>",
		Short: "Rotate a project's KEK (existing DEKs re-wrap lazily)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out map[string]any
			if err := c.call("POST", "/v1/projects/"+args[0]+"/kek/rotate", nil, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "rotated project %s to KEK version %v\n", args[0], out["kek_version"])
			return nil
		},
	}
	rewrap := &cobra.Command{
		Use:   "rewrap <project-id>",
		Short: "Re-wrap all DEKs onto the latest KEK and retire old versions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out map[string]any
			if err := c.call("POST", "/v1/projects/"+args[0]+"/kek/rewrap", nil, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "rewrapped %v DEKs; retired versions %v\n", out["rewrapped"], out["retired_versions"])
			return nil
		},
	}
	status := &cobra.Command{
		Use:   "kek-status <project-id>",
		Short: "Show current KEK version and pending (un-rewrapped) versions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out map[string]any
			if err := c.call("GET", "/v1/projects/"+args[0]+"/kek", nil, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "current version %v; pending %v\n", out["current_version"], out["pending"])
			return nil
		},
	}
	cmd.AddCommand(rotate, rewrap, status)
	return cmd
}
```

- [ ] **Step 3: Register in `cmd/janus/main.go`** — add `newProjectCmd()` to the `root.AddCommand(...)` list.

- [ ] **Step 4: Run — expect PASS.**
Run: `go test ./cmd/janus/ -run Project -v`

- [ ] **Step 5: Commit.**
```bash
git add cmd/janus/project_commands.go cmd/janus/main.go cmd/janus/project_commands_test.go
git commit -m "feat(cli): janus project rotate-kek/rewrap/kek-status"
```

---

### Task 9: Value-free proof + full gate + PR

**Files:** Create `internal/projectkeys/leak_test.go`; no product changes unless a gate fails.

- [ ] **Step 1: Leak test — rewrap never yields a secret value.** Write a sentinel secret value, rotate, then rewrap, and assert the sentinel plaintext never appears in captured logs during rewrap AND that rewrap succeeds even when the value ciphertext is deliberately corrupted (proving the value is never opened). Mirror the structure of `internal/api/runs_leak_test.go` / `internal/crypto/leak_test.go`.
```go
// internal/projectkeys/leak_test.go
// 1. write "CANARY"="SENTINEL-KEK-ROTATE-9f3a" (dek_key_version=1)
// 2. corrupt secret_values.ciphertext for that row (UPDATE ... SET ciphertext=E'\\xdeadbeef')
// 3. svc.Rotate + svc.Rewrap  -> must SUCCEED (rewrap only touches wrapped_dek, never ciphertext)
// 4. capture logs during the rewrap; assert the sentinel string never appears
// (the corrupted-ciphertext-but-rewrap-succeeds case is the strongest proof that
//  rewrap does not decrypt the value.)
```

- [ ] **Step 2: Run the leak test — expect PASS.**
Run: `go test ./internal/projectkeys/ -run Leak -v`

- [ ] **Step 3: Full backend gate.**
Run: `go build ./... && go vet ./... && go test -race ./internal/... ./cmd/...`
Expected: all green (testcontainers/Docker required). Also run `GOTOOLCHAIN=go1.26.5 govulncheck ./...` (expect 0) and `gosec -exclude-dir=internal/crypto/shamir ./...` (expect no new findings).

- [ ] **Step 4: Confirm migration reversibility** — read `000015_*.down.sql` (drops the table; the FK is child→parent so no dependency issue). The migrator's own tests already exercise up on a fresh DB.

- [ ] **Step 5: Push + open PR** (base `main`), title "Project-KEK rotation (lazy DEK re-wrap)", body summarizing: `project_kek_versions` table, instant `rotate`, resumable value-free `rewrap`, version-aware read path, owner-only API + CLI, and the crypto invariants (AES-256-GCM, fresh nonces, unchanged AADs, no value ever decrypted). **Do NOT merge** — the user merges after review.

---

## Self-review notes

- **Spec coverage:** storage table = T1; versioned KEK storage/retire = T2; instant rotate = T3; resumable batched rewrap = T4; orchestration + all crypto property/crash/tamper/nonce/no-decrypt tests = T5; version-aware read path = T6; owner-only API = T7; CLI = T8; value-free leak proof + gate = T9. ✅ every spec section maps to a task.
- **Type consistency:** `RotateKEK(ctx, id, wrapNew func(oldVersion int)([]byte,error))` (T3) is called by `projectkeys.Service.Rotate` (T5). `RewrapBatch(ctx, projectID, latest, cursor, limit, rewrap func(RewrapRow)([]byte,error)) (processed int, next string, err error)` (T4) is called in T5's loop. `ProjectKEKVersionRepo.{Insert,GetWrapped,ListPending,DeleteEmpty}` (T2) used in T3(tx insert)/T5/T6. `Status`/`RewrapResult`/`PendingVersion` names consistent T2/T5/T7. `KEKManage` action (T7) used in handlers (T7). ✅
- **No-decrypt invariant:** `RewrapBatch` SELECT omits `ciphertext`/`nonce`; `RewrapRow` has no value field; T9 proves it with a corrupted-ciphertext-still-succeeds test. ✅
- **Crash-safety:** each rewrap batch commits per tx advancing `dek_key_version`; re-run resumes; `DeleteEmpty` only removes zero-referenced versions. ✅
- **Constructor ripple:** T6 adds a `*store.ProjectKEKVersionRepo` to the secrets `Service` constructor — T6 must update ALL `secrets.New(...)` call sites (grep) or the build breaks; flagged in the task.
