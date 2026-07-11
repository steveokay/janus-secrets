# Dynamic Postgres Credentials + Lease Manager Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add on-demand, short-lived Postgres credentials to Janus with a lease manager that enforces TTL, renewal, revocation on expiry, and revoke-on-startup cleanup of crash-orphaned leases.

**Architecture:** A new `internal/dynamic/` package mirrors the shipped `internal/rotation/` and `internal/secretsync/` packages: config-scoped rows with an envelope-encrypted config blob, an in-process due-scan scheduler on a `JANUS_DYNAMIC_TICK`, and a post-unseal sweep. An admin registers a *dynamic role* holding Vault-style `creation`/`revocation`/`renew` SQL templates + admin DSN + TTLs. Issuing creds runs the creation SQL to mint a unique Postgres role and records a *lease*; the lease manager drops the DB role on expiry. Postgres-only (per CLAUDE.md non-goals).

**Tech Stack:** Go stdlib + `github.com/jackc/pgx/v5`, `chi` router, `cobra` CLI, `golang-migrate` SQL migrations, testcontainers-go for integration tests. Envelope crypto reuses `internal/crypto` (stdlib AES-256-GCM).

**Spec:** `docs/superpowers/specs/2026-07-11-dynamic-postgres-credentials-design.md`

---

## Canonical types (defined once; used verbatim across tasks)

```go
// internal/store/dynamic.go
type DynamicRole struct {
	ID                  string
	ProjectID           string
	ConfigID            string
	Name                string
	DefaultTTLSeconds   int64
	MaxTTLSeconds       int64
	ConfigCT            []byte
	ConfigNonce         []byte
	ConfigWrappedDEK    []byte
	ConfigDEKKEKVersion int
	CreatedBy           string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type DynamicLease struct {
	ID           string
	RoleID       string
	ProjectID    string
	DBUsername   string
	Status       string // creating|active|revoked|expired|revoke_failed
	IssuedAt     time.Time
	ExpiresAt    time.Time
	MaxExpiresAt time.Time
	RenewedAt    *time.Time
	RevokedAt    *time.Time
	LastError    *string
	CreatedBy    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
```

```go
// internal/dynamic package
type RoleConfig struct {
	AdminDSN             string `json:"admin_dsn"`
	CreationStatements   string `json:"creation_statements"`
	RevocationStatements string `json:"revocation_statements,omitempty"`
	RenewStatements      string `json:"renew_statements,omitempty"`
}

type RoleInput struct {
	ConfigID          string
	Name              string
	DefaultTTLSeconds int64
	MaxTTLSeconds     int64
	Config            RoleConfig
}

type RoleView struct {
	ID                string
	ProjectID         string
	ConfigID          string
	Name              string
	DefaultTTLSeconds int64
	MaxTTLSeconds     int64
	CreatedAt         time.Time
}

type LeaseView struct {
	ID           string
	RoleID       string
	ProjectID    string
	DBUsername   string
	Status       string
	IssuedAt     time.Time
	ExpiresAt    time.Time
	MaxExpiresAt time.Time
	RenewedAt    *time.Time
	CreatedAt    time.Time
}

type Creds struct {
	LeaseID   string
	Username  string
	Password  string
	ExpiresAt time.Time
}
```

**Status machine:** `creating` → `active` → (`revoked` manual | `expired` scheduler); any revoke failure → `revoke_failed` (retried by the scheduler). The generated **password is never persisted** — returned once from `IssueCreds`, then discarded.

---

## Task 1: Migration — `dynamic_roles` + `dynamic_leases`

**Files:**
- Create: `migrations/000012_dynamic.up.sql`
- Create: `migrations/000012_dynamic.down.sql`
- Test: `internal/store/dynamic_migration_test.go`

- [ ] **Step 1: Write the up migration**

`migrations/000012_dynamic.up.sql`:
```sql
CREATE TABLE dynamic_roles (
  id                     uuid PRIMARY KEY,
  project_id             uuid   NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  config_id              uuid   NOT NULL REFERENCES configs(id)  ON DELETE CASCADE,
  name                   text   NOT NULL,
  default_ttl_seconds    bigint NOT NULL CHECK (default_ttl_seconds > 0),
  max_ttl_seconds        bigint NOT NULL CHECK (max_ttl_seconds >= default_ttl_seconds),
  config_ct              bytea  NOT NULL,
  config_nonce           bytea  NOT NULL,
  config_wrapped_dek     bytea  NOT NULL,
  config_dek_kek_version int    NOT NULL,
  created_by             text   NOT NULL,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  UNIQUE (config_id, name)
);

CREATE TABLE dynamic_leases (
  id             uuid PRIMARY KEY,
  role_id        uuid NOT NULL REFERENCES dynamic_roles(id) ON DELETE CASCADE,
  project_id     uuid NOT NULL REFERENCES projects(id)      ON DELETE CASCADE,
  db_username    text NOT NULL,
  status         text NOT NULL DEFAULT 'creating'
                   CHECK (status IN ('creating','active','revoked','expired','revoke_failed')),
  issued_at      timestamptz NOT NULL DEFAULT now(),
  expires_at     timestamptz NOT NULL,
  max_expires_at timestamptz NOT NULL,
  renewed_at     timestamptz,
  revoked_at     timestamptz,
  last_error     text,
  created_by     text NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now()
);

-- Lease-manager due-scan: expiring active leases + revoke retries.
CREATE INDEX dynamic_leases_active_due ON dynamic_leases (expires_at) WHERE status = 'active';
-- Reclaim of crash-orphaned in-flight issues + revoke retries.
CREATE INDEX dynamic_leases_creating ON dynamic_leases (created_at) WHERE status = 'creating';
CREATE INDEX dynamic_leases_revoke_failed ON dynamic_leases (id) WHERE status = 'revoke_failed';
```

- [ ] **Step 2: Write the down migration**

`migrations/000012_dynamic.down.sql`:
```sql
DROP TABLE IF EXISTS dynamic_leases;
DROP TABLE IF EXISTS dynamic_roles;
```

- [ ] **Step 3: Write the migration test**

Mirror `internal/store/rotation_migration_test.go`. Open the existing structure first (`Read internal/store/rotation_migration_test.go`) and copy its shape exactly, changing table/column names.

`internal/store/dynamic_migration_test.go`:
```go
package store

import (
	"context"
	"testing"
)

func TestDynamicMigrationTables(t *testing.T) {
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	ctx := context.Background()
	for _, tbl := range []string{"dynamic_roles", "dynamic_leases"} {
		var exists bool
		if err := testStore.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name=$1)`, tbl).
			Scan(&exists); err != nil {
			t.Fatalf("query %s: %v", tbl, err)
		}
		if !exists {
			t.Fatalf("table %s not created by migration", tbl)
		}
	}
}
```

- [ ] **Step 4: Run the migration test**

Run: `go test ./internal/store/ -run TestDynamicMigrationTables -v`
Expected: PASS (or SKIP if Docker unavailable). The store `TestMain` applies all migrations via `st.Migrate`.

- [ ] **Step 5: Commit**

```bash
git add migrations/000012_dynamic.up.sql migrations/000012_dynamic.down.sql internal/store/dynamic_migration_test.go
git commit -m "feat(dynamic): migration 000012 for dynamic roles + leases"
```

---

## Task 2: Crypto AAD — `DynamicConfigAAD`

**Files:**
- Modify: `internal/crypto/keys.go` (add after `SyncCredsAAD`, ~line 91)
- Test: `internal/crypto/keys_test.go` (extend domain-separation test block)

- [ ] **Step 1: Write the failing test**

Add to `internal/crypto/keys_test.go` (inside the existing AAD test area near line 155 where `SyncCredsAAD` is tested):
```go
func TestDynamicConfigAADDomainSeparation(t *testing.T) {
	if bytes.Equal(DynamicConfigAAD("x"), SyncCredsAAD("x")) {
		t.Fatal("dynamic and sync AADs must differ")
	}
	if bytes.Equal(DynamicConfigAAD("x"), RotationConfigAAD("x")) {
		t.Fatal("dynamic and rotation AADs must differ")
	}
	if bytes.Equal(DynamicConfigAAD("r1"), DynamicConfigAAD("r2")) {
		t.Fatal("different role ids must yield different AADs")
	}
	if bytes.Equal(DynamicConfigAAD("ab"), DynamicConfigAAD("a\x00b")) {
		t.Fatal("length-prefixed encoding must resist boundary collisions")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/crypto/ -run TestDynamicConfigAADDomainSeparation -v`
Expected: FAIL — `undefined: DynamicConfigAAD`.

- [ ] **Step 3: Add the AAD function**

In `internal/crypto/keys.go`, after `SyncCredsAAD`:
```go
// DynamicConfigAAD binds a dynamic role's encrypted RoleConfig blob (admin DSN,
// creation/revocation/renew SQL) to its role id, in a domain distinct from the
// rotation and sync AADs.
func DynamicConfigAAD(roleID string) []byte {
	return appendField([]byte("janus:dynamic:config"), roleID)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/crypto/ -run TestDynamicConfigAADDomainSeparation -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/crypto/keys.go internal/crypto/keys_test.go
git commit -m "feat(dynamic): DynamicConfigAAD domain-separated envelope AAD"
```

---

## Task 3: Store repositories — roles + leases

**Files:**
- Create: `internal/store/dynamic.go`
- Test: `internal/store/dynamic_test.go`

Reference `internal/store/rotation.go` for the exact `mapError`, `execAffectingOne`, `NewID`, and column-list conventions used below.

- [ ] **Step 1: Write the repositories**

`internal/store/dynamic.go`:
```go
package store

import (
	"context"
	"time"
)

// --- Roles ---

type DynamicRole struct {
	ID                  string
	ProjectID           string
	ConfigID            string
	Name                string
	DefaultTTLSeconds   int64
	MaxTTLSeconds       int64
	ConfigCT            []byte
	ConfigNonce         []byte
	ConfigWrappedDEK    []byte
	ConfigDEKKEKVersion int
	CreatedBy           string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type DynamicRoleRepo struct{ s *Store }

func NewDynamicRoleRepo(s *Store) *DynamicRoleRepo { return &DynamicRoleRepo{s: s} }

const dynamicRoleCols = `id::text, project_id::text, config_id::text, name,
	default_ttl_seconds, max_ttl_seconds, config_ct, config_nonce, config_wrapped_dek,
	config_dek_kek_version, created_by, created_at, updated_at`

func scanRole(row interface{ Scan(...any) error }) (*DynamicRole, error) {
	var r DynamicRole
	if err := row.Scan(&r.ID, &r.ProjectID, &r.ConfigID, &r.Name,
		&r.DefaultTTLSeconds, &r.MaxTTLSeconds, &r.ConfigCT, &r.ConfigNonce, &r.ConfigWrappedDEK,
		&r.ConfigDEKKEKVersion, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, mapError(err)
	}
	return &r, nil
}

// Create inserts a role. Duplicate (config_id, name) → ErrAlreadyExists.
func (repo *DynamicRoleRepo) Create(ctx context.Context, r *DynamicRole) (*DynamicRole, error) {
	_, err := repo.s.pool.Exec(ctx,
		`INSERT INTO dynamic_roles
		 (id, project_id, config_id, name, default_ttl_seconds, max_ttl_seconds,
		  config_ct, config_nonce, config_wrapped_dek, config_dek_kek_version, created_by)
		 VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10,$11)`,
		r.ID, r.ProjectID, r.ConfigID, r.Name, r.DefaultTTLSeconds, r.MaxTTLSeconds,
		r.ConfigCT, r.ConfigNonce, r.ConfigWrappedDEK, r.ConfigDEKKEKVersion, r.CreatedBy)
	if err != nil {
		return nil, mapError(err)
	}
	return repo.Get(ctx, r.ID)
}

func (repo *DynamicRoleRepo) Get(ctx context.Context, id string) (*DynamicRole, error) {
	return scanRole(repo.s.pool.QueryRow(ctx,
		`SELECT `+dynamicRoleCols+` FROM dynamic_roles WHERE id = $1::uuid`, id))
}

func (repo *DynamicRoleRepo) ListByConfig(ctx context.Context, configID string) ([]*DynamicRole, error) {
	rows, err := repo.s.pool.Query(ctx,
		`SELECT `+dynamicRoleCols+` FROM dynamic_roles WHERE config_id = $1::uuid ORDER BY created_at DESC, id`,
		configID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*DynamicRole
	for rows.Next() {
		r, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, mapError(rows.Err())
}

// Update sets TTLs and (optionally) a new encrypted config blob. nil leaves a
// field unchanged.
func (repo *DynamicRoleRepo) Update(ctx context.Context, id string, defaultTTL, maxTTL *int64,
	configCT, configNonce, configWrappedDEK []byte, configKEKVer *int) error {
	return repo.s.execAffectingOne(ctx,
		`UPDATE dynamic_roles SET
		   default_ttl_seconds    = COALESCE($2, default_ttl_seconds),
		   max_ttl_seconds        = COALESCE($3, max_ttl_seconds),
		   config_ct              = COALESCE($4, config_ct),
		   config_nonce           = COALESCE($5, config_nonce),
		   config_wrapped_dek     = COALESCE($6, config_wrapped_dek),
		   config_dek_kek_version = COALESCE($7, config_dek_kek_version),
		   updated_at             = now()
		 WHERE id = $1::uuid`,
		id, defaultTTL, maxTTL, configCT, configNonce, configWrappedDEK, configKEKVer)
}

func (repo *DynamicRoleRepo) Delete(ctx context.Context, id string) error {
	return repo.s.execAffectingOne(ctx, `DELETE FROM dynamic_roles WHERE id = $1::uuid`, id)
}

// --- Leases ---

type DynamicLease struct {
	ID           string
	RoleID       string
	ProjectID    string
	DBUsername   string
	Status       string
	IssuedAt     time.Time
	ExpiresAt    time.Time
	MaxExpiresAt time.Time
	RenewedAt    *time.Time
	RevokedAt    *time.Time
	LastError    *string
	CreatedBy    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type DynamicLeaseRepo struct{ s *Store }

func NewDynamicLeaseRepo(s *Store) *DynamicLeaseRepo { return &DynamicLeaseRepo{s: s} }

const dynamicLeaseCols = `id::text, role_id::text, project_id::text, db_username, status,
	issued_at, expires_at, max_expires_at, renewed_at, revoked_at, last_error,
	created_by, created_at, updated_at`

func scanLease(row interface{ Scan(...any) error }) (*DynamicLease, error) {
	var l DynamicLease
	if err := row.Scan(&l.ID, &l.RoleID, &l.ProjectID, &l.DBUsername, &l.Status,
		&l.IssuedAt, &l.ExpiresAt, &l.MaxExpiresAt, &l.RenewedAt, &l.RevokedAt, &l.LastError,
		&l.CreatedBy, &l.CreatedAt, &l.UpdatedAt); err != nil {
		return nil, mapError(err)
	}
	return &l, nil
}

// Create inserts a lease in status 'creating'.
func (repo *DynamicLeaseRepo) Create(ctx context.Context, l *DynamicLease) error {
	_, err := repo.s.pool.Exec(ctx,
		`INSERT INTO dynamic_leases
		 (id, role_id, project_id, db_username, expires_at, max_expires_at, created_by)
		 VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7)`,
		l.ID, l.RoleID, l.ProjectID, l.DBUsername, l.ExpiresAt, l.MaxExpiresAt, l.CreatedBy)
	return mapError(err)
}

func (repo *DynamicLeaseRepo) Get(ctx context.Context, id string) (*DynamicLease, error) {
	return scanLease(repo.s.pool.QueryRow(ctx,
		`SELECT `+dynamicLeaseCols+` FROM dynamic_leases WHERE id = $1::uuid`, id))
}

func (repo *DynamicLeaseRepo) ListByRole(ctx context.Context, roleID string) ([]*DynamicLease, error) {
	rows, err := repo.s.pool.Query(ctx,
		`SELECT `+dynamicLeaseCols+` FROM dynamic_leases WHERE role_id = $1::uuid ORDER BY created_at DESC, id`,
		roleID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*DynamicLease
	for rows.Next() {
		l, err := scanLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, mapError(rows.Err())
}

// Activate flips a 'creating' lease to 'active'.
func (repo *DynamicLeaseRepo) Activate(ctx context.Context, id string) error {
	return repo.s.execAffectingOne(ctx,
		`UPDATE dynamic_leases SET status='active', updated_at=now()
		 WHERE id=$1::uuid AND status='creating'`, id)
}

// ClaimDue returns leases the lease-manager must act on: active leases past
// expiry, revoke retries, and crash-orphaned 'creating' rows older than the
// grace window (a running IssueCreds activates within milliseconds, so grace
// prevents revoking an in-flight lease). Single-node = one scheduler goroutine,
// so a plain SELECT is race-free; no FOR UPDATE because revocation performs
// network I/O.
func (repo *DynamicLeaseRepo) ClaimDue(ctx context.Context, now, creatingBefore time.Time, limit int) ([]*DynamicLease, error) {
	rows, err := repo.s.pool.Query(ctx,
		`SELECT `+dynamicLeaseCols+` FROM dynamic_leases
		 WHERE (status='active' AND expires_at <= $1)
		    OR status='revoke_failed'
		    OR (status='creating' AND created_at <= $2)
		 ORDER BY expires_at ASC LIMIT $3`, now, creatingBefore, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*DynamicLease
	for rows.Next() {
		l, err := scanLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, mapError(rows.Err())
}

// MarkRevoked records a successful revocation with the given terminal status
// ('revoked' or 'expired').
func (repo *DynamicLeaseRepo) MarkRevoked(ctx context.Context, id, status string, now time.Time) error {
	return repo.s.execAffectingOne(ctx,
		`UPDATE dynamic_leases SET status=$2, revoked_at=$3, last_error=NULL, updated_at=now()
		 WHERE id=$1::uuid`, id, status, now)
}

// MarkRevokeFailed records a failed revocation for scheduler retry.
func (repo *DynamicLeaseRepo) MarkRevokeFailed(ctx context.Context, id, sanitizedErr string) error {
	return repo.s.execAffectingOne(ctx,
		`UPDATE dynamic_leases SET status='revoke_failed', last_error=$2, updated_at=now()
		 WHERE id=$1::uuid`, id, sanitizedErr)
}

// Renew advances an active lease's expiry.
func (repo *DynamicLeaseRepo) Renew(ctx context.Context, id string, newExpiry, now time.Time) error {
	return repo.s.execAffectingOne(ctx,
		`UPDATE dynamic_leases SET expires_at=$2, renewed_at=$3, updated_at=now()
		 WHERE id=$1::uuid AND status='active'`, id, newExpiry, now)
}
```

- [ ] **Step 2: Write the store test**

`internal/store/dynamic_test.go`:
```go
package store

import (
	"context"
	"testing"
	"time"
)

func mkRole(t *testing.T, ctx context.Context, repo *DynamicRoleRepo, projectID, configID, name string) *DynamicRole {
	t.Helper()
	id, err := testStore.NewID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, err := repo.Create(ctx, &DynamicRole{
		ID: id, ProjectID: projectID, ConfigID: configID, Name: name,
		DefaultTTLSeconds: 3600, MaxTTLSeconds: 86400,
		ConfigCT: []byte("ct"), ConfigNonce: []byte("n"), ConfigWrappedDEK: []byte("w"),
		ConfigDEKKEKVersion: 1, CreatedBy: "tester",
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	return r
}

func TestDynamicLeaseLifecycle(t *testing.T) {
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	ctx := context.Background()
	projectID, configID := seedProjectConfig(t, ctx) // see note below
	roleRepo := NewDynamicRoleRepo(testStore)
	leaseRepo := NewDynamicLeaseRepo(testStore)

	role := mkRole(t, ctx, roleRepo, projectID, configID, "readonly")

	// Duplicate (config_id, name) rejected.
	if _, err := roleRepo.Create(ctx, &DynamicRole{
		ID: mustID(t, ctx), ProjectID: projectID, ConfigID: configID, Name: "readonly",
		DefaultTTLSeconds: 1, MaxTTLSeconds: 2, ConfigCT: []byte("x"), ConfigNonce: []byte("x"),
		ConfigWrappedDEK: []byte("x"), ConfigDEKKEKVersion: 1, CreatedBy: "t",
	}); err != ErrAlreadyExists {
		t.Fatalf("want ErrAlreadyExists, got %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	lease := &DynamicLease{
		ID: mustID(t, ctx), RoleID: role.ID, ProjectID: projectID, DBUsername: "janus_readonly_abc",
		ExpiresAt: now.Add(-time.Minute), MaxExpiresAt: now.Add(time.Hour), CreatedBy: "tester",
	}
	if err := leaseRepo.Create(ctx, lease); err != nil {
		t.Fatalf("create lease: %v", err)
	}

	// A 'creating' lease is NOT due until it ages past the grace window, and
	// is never selected by the active-expiry branch.
	due, err := leaseRepo.ClaimDue(ctx, now, now.Add(-time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("young creating lease should not be due, got %d", len(due))
	}

	if err := leaseRepo.Activate(ctx, lease.ID); err != nil {
		t.Fatal(err)
	}
	// Now active and already expired → due.
	due, err = leaseRepo.ClaimDue(ctx, now, now.Add(-time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != lease.ID {
		t.Fatalf("expired active lease should be due, got %d", len(due))
	}

	if err := leaseRepo.MarkRevoked(ctx, lease.ID, "expired", now); err != nil {
		t.Fatal(err)
	}
	got, err := leaseRepo.Get(ctx, lease.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "expired" || got.RevokedAt == nil {
		t.Fatalf("want expired+revoked_at, got status=%s revoked_at=%v", got.Status, got.RevokedAt)
	}
}
```

> **Note on `seedProjectConfig`/`mustID`:** The store test package already has helpers that create a project + environment + config for FK-satisfying tests (used by `rotation_test.go` / `sync_test.go`). Open `internal/store/rotation_test.go` and reuse the exact helper it uses to obtain a valid `(projectID, configID)` and a fresh id. If no shared helper exists, add small local `seedProjectConfig(t, ctx) (string, string)` and `mustID(t, ctx) string` helpers in `dynamic_test.go` following the insert pattern in `rotation_test.go`.

- [ ] **Step 3: Run the test**

Run: `go test ./internal/store/ -run 'TestDynamicLeaseLifecycle|TestDynamicMigrationTables' -v`
Expected: PASS (or SKIP without Docker).

- [ ] **Step 4: Commit**

```bash
git add internal/store/dynamic.go internal/store/dynamic_test.go
git commit -m "feat(dynamic): store repos for dynamic roles + leases"
```

---

## Task 4: Engine scaffolding — errors, service, envelope, generation

**Files:**
- Create: `internal/dynamic/errors.go`
- Create: `internal/dynamic/dynamic.go`
- Create: `internal/dynamic/generate.go`
- Test: `internal/dynamic/generate_test.go`

Envelope helpers (`zero`, `sealBlob`, `openBlob`, `unwrapProjectKEK`, `keyring` interface) are copied from `internal/rotation/rotation.go:44-168` verbatim except the package name and AAD calls. The dynamic engine does **not** depend on `secrets.Service` (it never writes secret versions).

- [ ] **Step 1: Write `errors.go`**

`internal/dynamic/errors.go`:
```go
package dynamic

import "errors"

var (
	ErrNotFound     = errors.New("dynamic: role or lease not found")
	ErrExists       = errors.New("dynamic: role already exists for this config/name")
	ErrSealed       = errors.New("dynamic: server is sealed")
	ErrInvalidConfig = errors.New("dynamic: invalid role config")
	ErrNotRenewable = errors.New("dynamic: lease is not active")
	ErrApplyFailed  = errors.New("dynamic: postgres statement failed")
)
```

- [ ] **Step 2: Write `dynamic.go`**

`internal/dynamic/dynamic.go`:
```go
// Package dynamic is Janus's dynamic Postgres credentials engine: on-demand,
// short-lived database roles issued from admin-authored SQL templates, with a
// lease manager that revokes them on expiry (and reclaims crash-orphaned leases
// after unseal).
package dynamic

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"runtime"
	"time"

	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

const (
	defaultBatch     = 50               // leases claimed per tick
	defaultPasswdLen = 32               // generated password length
	creatingGrace    = 5 * time.Minute  // min age before a 'creating' lease is swept
)

// RoleConfig is the decrypted role-config blob (never logged/persisted in clear).
type RoleConfig struct {
	AdminDSN             string `json:"admin_dsn"`
	CreationStatements   string `json:"creation_statements"`
	RevocationStatements string `json:"revocation_statements,omitempty"`
	RenewStatements      string `json:"renew_statements,omitempty"`
}

type keyring interface {
	UnwrapProjectKEK(ct crypto.Ciphertext, projectID string) ([]byte, error)
	NewDEK(projectKEK, aad []byte) ([]byte, crypto.Ciphertext, error)
	Sealed() bool
}

type Service struct {
	kr       keyring
	roles    *store.DynamicRoleRepo
	leases   *store.DynamicLeaseRepo
	projects *store.ProjectRepo
	audit    *audit.Recorder
	logger   *slog.Logger
	st       *store.Store
	now      func() time.Time
}

func New(kr *crypto.Keyring, st *store.Store, aud *audit.Recorder, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		kr: kr, roles: store.NewDynamicRoleRepo(st), leases: store.NewDynamicLeaseRepo(st),
		projects: store.NewProjectRepo(st), audit: aud, logger: logger, st: st, now: time.Now,
	}
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
	runtime.KeepAlive(b)
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func mapStoreErr(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, store.ErrAlreadyExists):
		return ErrExists
	default:
		return err
	}
}

// sanitize maps an error to a fixed, value-free category safe for last_error/audit.
func sanitize(err error) string {
	switch {
	case errors.Is(err, ErrSealed):
		return "sealed"
	case errors.Is(err, ErrApplyFailed):
		return "apply failed"
	case errors.Is(err, ErrInvalidConfig):
		return "invalid config"
	default:
		return "dynamic error"
	}
}

// --- envelope (copied from the rotation engine; AAD is DynamicConfigAAD) ---

func (s *Service) sealConfig(proj *store.Project, roleID string, cfg RoleConfig) (ct, nonce, wrapped []byte, kekVer int, err error) {
	return s.sealBlob(proj, crypto.DynamicConfigAAD(roleID), mustJSON(cfg))
}

func (s *Service) openConfig(proj *store.Project, r *store.DynamicRole) (RoleConfig, error) {
	pt, err := s.openBlob(proj, crypto.DynamicConfigAAD(r.ID), r.ConfigWrappedDEK, r.ConfigNonce, r.ConfigCT)
	if err != nil {
		return RoleConfig{}, err
	}
	defer zero(pt)
	var cfg RoleConfig
	if err := json.Unmarshal(pt, &cfg); err != nil {
		return RoleConfig{}, ErrInvalidConfig
	}
	return cfg, nil
}

func (s *Service) sealBlob(proj *store.Project, aad, plaintext []byte) (ct, nonce, wrapped []byte, kekVer int, err error) {
	kek, err := s.unwrapProjectKEK(proj)
	if err != nil {
		return nil, nil, nil, 0, err
	}
	defer zero(kek)
	dek, wrappedDEK, err := s.kr.NewDEK(kek, aad)
	if err != nil {
		return nil, nil, nil, 0, err
	}
	defer zero(dek)
	c, err := crypto.Encrypt(dek, plaintext, aad)
	if err != nil {
		return nil, nil, nil, 0, err
	}
	zero(plaintext)
	return c.Data, c.Nonce, wrappedDEK.Marshal(), proj.KEKVersion, nil
}

func (s *Service) openBlob(proj *store.Project, aad, wrappedDEK, nonce, ct []byte) ([]byte, error) {
	kek, err := s.unwrapProjectKEK(proj)
	if err != nil {
		return nil, err
	}
	defer zero(kek)
	dekCT, err := crypto.ParseCiphertext(wrappedDEK)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	dek, err := crypto.UnwrapKey(kek, dekCT, aad)
	if err != nil {
		return nil, err
	}
	defer zero(dek)
	return crypto.Decrypt(dek, crypto.Ciphertext{Nonce: nonce, Data: ct}, aad)
}

func (s *Service) unwrapProjectKEK(proj *store.Project) ([]byte, error) {
	if s.kr.Sealed() {
		return nil, ErrSealed
	}
	ct, err := crypto.ParseCiphertext(proj.WrappedKEK)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	return s.kr.UnwrapProjectKEK(ct, proj.ID)
}

// --- views ---

type RoleView struct {
	ID                string
	ProjectID         string
	ConfigID          string
	Name              string
	DefaultTTLSeconds int64
	MaxTTLSeconds     int64
	CreatedAt         time.Time
}

func roleView(r *store.DynamicRole) RoleView {
	return RoleView{
		ID: r.ID, ProjectID: r.ProjectID, ConfigID: r.ConfigID, Name: r.Name,
		DefaultTTLSeconds: r.DefaultTTLSeconds, MaxTTLSeconds: r.MaxTTLSeconds, CreatedAt: r.CreatedAt,
	}
}

type LeaseView struct {
	ID           string
	RoleID       string
	ProjectID    string
	DBUsername   string
	Status       string
	IssuedAt     time.Time
	ExpiresAt    time.Time
	MaxExpiresAt time.Time
	RenewedAt    *time.Time
	CreatedAt    time.Time
}

func leaseView(l *store.DynamicLease) LeaseView {
	return LeaseView{
		ID: l.ID, RoleID: l.RoleID, ProjectID: l.ProjectID, DBUsername: l.DBUsername,
		Status: l.Status, IssuedAt: l.IssuedAt, ExpiresAt: l.ExpiresAt, MaxExpiresAt: l.MaxExpiresAt,
		RenewedAt: l.RenewedAt, CreatedAt: l.CreatedAt,
	}
}

type Creds struct {
	LeaseID   string
	Username  string
	Password  string
	ExpiresAt time.Time
}
```

- [ ] **Step 3: Write `generate.go` and its failing test first**

`internal/dynamic/generate_test.go`:
```go
package dynamic

import "testing"

func TestGenerateUsernameIsIdentifierSafe(t *testing.T) {
	cases := []string{"readonly", "read-write!!", "", "ADMIN", "a_very_long_role_name_exceeding_limits_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	for _, name := range cases {
		u, err := generateUsername(name)
		if err != nil {
			t.Fatalf("generateUsername(%q): %v", name, err)
		}
		if !identRe.MatchString(u) {
			t.Fatalf("username %q not identifier-safe", u)
		}
		if len(u) > 63 {
			t.Fatalf("username %q exceeds 63 bytes", u)
		}
	}
	// Uniqueness across calls (random suffix).
	a, _ := generateUsername("x")
	b, _ := generateUsername("x")
	if a == b {
		t.Fatal("expected distinct usernames")
	}
}

func TestGeneratePasswordAlphabet(t *testing.T) {
	p, err := generatePassword(40)
	if err != nil || len(p) != 40 {
		t.Fatalf("generatePassword: %q err=%v", p, err)
	}
	for _, c := range p {
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			t.Fatalf("password has non-alphanumeric char %q", c)
		}
	}
}
```

`internal/dynamic/generate.go`:
```go
package dynamic

import (
	"crypto/rand"
	"errors"
	"regexp"
	"strings"
)

const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
const lowerAlnum = "abcdefghijklmnopqrstuvwxyz0123456789"

// identRe restricts generated usernames to a plain SQL identifier (<=63 bytes).
var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)

var prefixStrip = regexp.MustCompile(`[^a-z0-9_]`)

func randChars(n int, alpha string) (string, error) {
	if n <= 0 {
		return "", errors.New("dynamic: length must be positive")
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = alpha[int(buf[i])%len(alpha)]
	}
	return string(buf), nil
}

func generatePassword(n int) (string, error) { return randChars(n, alphabet) }

// generateUsername builds "janus_<prefix>_<random>", identifier-safe and <=63 bytes.
func generateUsername(roleName string) (string, error) {
	prefix := prefixStrip.ReplaceAllString(strings.ToLower(roleName), "")
	if prefix == "" {
		prefix = "role"
	}
	const suffixLen = 12
	maxPrefix := 63 - len("janus_") - 1 - suffixLen
	if len(prefix) > maxPrefix {
		prefix = prefix[:maxPrefix]
	}
	suffix, err := randChars(suffixLen, lowerAlnum)
	if err != nil {
		return "", err
	}
	u := "janus_" + prefix + "_" + suffix
	if !identRe.MatchString(u) {
		return "", errors.New("dynamic: generated username failed identifier check")
	}
	return u, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/dynamic/ -run 'TestGenerate' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/dynamic/errors.go internal/dynamic/dynamic.go internal/dynamic/generate.go internal/dynamic/generate_test.go
git commit -m "feat(dynamic): engine scaffolding — service, envelope, generation"
```

---

## Task 5: Postgres executor — interpolation + statement execution

**Files:**
- Create: `internal/dynamic/postgres.go`
- Test: `internal/dynamic/postgres_test.go`

- [ ] **Step 1: Write the failing unit test**

`internal/dynamic/postgres_test.go` (unit portion — no DB):
```go
package dynamic

import (
	"strings"
	"testing"
	"time"
)

func TestInterpolateSubstitutesPlaceholders(t *testing.T) {
	exp := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	out, err := interpolate(
		`CREATE ROLE "{{name}}" LOGIN PASSWORD '{{password}}' VALID UNTIL '{{expiration}}';`,
		"janus_ro_abc123def456", "Pw0rd", exp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"janus_ro_abc123def456"`) ||
		!strings.Contains(out, `'Pw0rd'`) ||
		!strings.Contains(out, `'2030-01-02T03:04:05Z'`) {
		t.Fatalf("unexpected interpolation: %s", out)
	}
}

func TestInterpolateRejectsUnsafeUsername(t *testing.T) {
	if _, err := interpolate(`CREATE ROLE "{{name}}"`, `bad"; DROP`, "x", time.Now()); err == nil {
		t.Fatal("want rejection of non-identifier username")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/dynamic/ -run TestInterpolate -v`
Expected: FAIL — `undefined: interpolate`.

- [ ] **Step 3: Write `postgres.go`**

`internal/dynamic/postgres.go`:
```go
package dynamic

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// interpolate substitutes {{name}}/{{password}}/{{expiration}}. The username and
// password are generated from a quote-free alphabet (generate.go) and expiration
// is RFC3339, so raw substitution inside the admin-authored quotes is
// injection-safe; the username is re-validated against identRe defensively.
func interpolate(tmpl, username, password string, expiration time.Time) (string, error) {
	if !identRe.MatchString(username) {
		return "", ErrInvalidConfig
	}
	r := strings.NewReplacer(
		"{{name}}", username,
		"{{password}}", password,
		"{{expiration}}", expiration.UTC().Format(time.RFC3339),
	)
	return r.Replace(tmpl), nil
}

// runStatements connects as admin and executes the (possibly multi-statement)
// SQL text. With no query arguments, pgx uses the simple protocol, which permits
// multiple semicolon-separated statements in one call. The admin DSN is never
// surfaced in returned errors.
func runStatements(ctx context.Context, adminDSN, sql string) error {
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return fmt.Errorf("%w: admin connect failed", ErrApplyFailed)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, sql); err != nil {
		return fmt.Errorf("%w: statement exec failed", ErrApplyFailed)
	}
	return nil
}
```

- [ ] **Step 4: Run the unit test**

Run: `go test ./internal/dynamic/ -run TestInterpolate -v`
Expected: PASS.

- [ ] **Step 5: Add the testcontainer TestMain + a real create/revoke integration test**

Copy `internal/rotation/rotation_test.go:1-70` `TestMain`/`startPostgres` into a new `internal/dynamic/main_test.go` (change package to `dynamic`, keep `testStore`/`testDSN` globals). Then add the DB-backed test to `postgres_test.go`:
```go
func TestRunStatementsCreatesAndDropsRole(t *testing.T) {
	if testDSN == "" {
		t.Skip("postgres/docker not available")
	}
	ctx := context.Background()
	u, err := generateUsername("itest")
	if err != nil {
		t.Fatal(err)
	}
	create, _ := interpolate(`CREATE ROLE "{{name}}" LOGIN PASSWORD '{{password}}';`, u, "testPW123", time.Now())
	if err := runStatements(ctx, testDSN, create); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Idempotent drop via the default revocation template.
	drop, _ := interpolate(`DROP ROLE IF EXISTS "{{name}}";`, u, "", time.Now())
	if err := runStatements(ctx, testDSN, drop); err != nil {
		t.Fatalf("drop: %v", err)
	}
	// Second drop still succeeds (IF EXISTS).
	if err := runStatements(ctx, testDSN, drop); err != nil {
		t.Fatalf("re-drop not idempotent: %v", err)
	}
	// Connect failure never leaks the DSN.
	if err := runStatements(ctx, "postgres://bad:bad@127.0.0.1:1/none", drop); err == nil {
		t.Fatal("want connect error")
	} else if strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("error leaked DSN host: %v", err)
	}
}
```

- [ ] **Step 6: Run all dynamic tests**

Run: `go test ./internal/dynamic/ -v`
Expected: PASS (DB tests SKIP without Docker).

- [ ] **Step 7: Commit**

```bash
git add internal/dynamic/postgres.go internal/dynamic/postgres_test.go internal/dynamic/main_test.go
git commit -m "feat(dynamic): postgres executor with injection-safe interpolation"
```

---

## Task 6: Role CRUD

**Files:**
- Create: `internal/dynamic/crud.go`
- Test: `internal/dynamic/crud_test.go`

- [ ] **Step 1: Write `crud.go`**

`internal/dynamic/crud.go`:
```go
package dynamic

import (
	"context"
	"strings"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

// validateConfig enforces required placeholders and non-empty admin DSN.
func validateConfig(cfg RoleConfig) error {
	if cfg.AdminDSN == "" {
		return ErrInvalidConfig
	}
	if !strings.Contains(cfg.CreationStatements, "{{name}}") ||
		!strings.Contains(cfg.CreationStatements, "{{password}}") {
		return ErrInvalidConfig
	}
	// Revocation may be empty (engine falls back to DROP ROLE IF EXISTS); if set
	// it must reference the role name.
	if strings.TrimSpace(cfg.RevocationStatements) != "" &&
		!strings.Contains(cfg.RevocationStatements, "{{name}}") {
		return ErrInvalidConfig
	}
	if strings.TrimSpace(cfg.RenewStatements) != "" &&
		!strings.Contains(cfg.RenewStatements, "{{name}}") {
		return ErrInvalidConfig
	}
	return nil
}

func (s *Service) projectForConfig(ctx context.Context, configID string) (*store.Project, error) {
	cfg, err := store.NewConfigRepo(s.st).Get(ctx, configID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	env, err := store.NewEnvironmentRepo(s.st).Get(ctx, cfg.EnvironmentID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	proj, err := s.projects.Get(ctx, env.ProjectID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return proj, nil
}

func (s *Service) CreateRole(ctx context.Context, in RoleInput, createdBy string) (RoleView, error) {
	if in.Name == "" || in.DefaultTTLSeconds <= 0 || in.MaxTTLSeconds < in.DefaultTTLSeconds {
		return RoleView{}, ErrInvalidConfig
	}
	if err := validateConfig(in.Config); err != nil {
		return RoleView{}, err
	}
	proj, err := s.projectForConfig(ctx, in.ConfigID)
	if err != nil {
		return RoleView{}, err
	}
	id, err := s.st.NewID(ctx)
	if err != nil {
		return RoleView{}, err
	}
	ct, nonce, wrapped, kekVer, err := s.sealConfig(proj, id, in.Config)
	if err != nil {
		return RoleView{}, err
	}
	r := &store.DynamicRole{
		ID: id, ProjectID: proj.ID, ConfigID: in.ConfigID, Name: in.Name,
		DefaultTTLSeconds: in.DefaultTTLSeconds, MaxTTLSeconds: in.MaxTTLSeconds,
		ConfigCT: ct, ConfigNonce: nonce, ConfigWrappedDEK: wrapped, ConfigDEKKEKVersion: kekVer,
		CreatedBy: createdBy,
	}
	saved, err := s.roles.Create(ctx, r)
	if err != nil {
		return RoleView{}, mapStoreErr(err)
	}
	return roleView(saved), nil
}

func (s *Service) GetRole(ctx context.Context, id string) (RoleView, error) {
	r, err := s.roles.Get(ctx, id)
	if err != nil {
		return RoleView{}, mapStoreErr(err)
	}
	return roleView(r), nil
}

func (s *Service) ListRolesByConfig(ctx context.Context, configID string) ([]RoleView, error) {
	rs, err := s.roles.ListByConfig(ctx, configID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	out := make([]RoleView, 0, len(rs))
	for _, r := range rs {
		out = append(out, roleView(r))
	}
	return out, nil
}

// UpdateRole changes TTLs and/or the config blob. nil leaves a field unchanged.
func (s *Service) UpdateRole(ctx context.Context, id string, defaultTTL, maxTTL *int64, cfg *RoleConfig) (RoleView, error) {
	r, err := s.roles.Get(ctx, id)
	if err != nil {
		return RoleView{}, mapStoreErr(err)
	}
	// Validate the resulting TTL pair.
	dt, mt := r.DefaultTTLSeconds, r.MaxTTLSeconds
	if defaultTTL != nil {
		dt = *defaultTTL
	}
	if maxTTL != nil {
		mt = *maxTTL
	}
	if dt <= 0 || mt < dt {
		return RoleView{}, ErrInvalidConfig
	}
	var ct, nonce, wrapped []byte
	var kekVer *int
	if cfg != nil {
		if err := validateConfig(*cfg); err != nil {
			return RoleView{}, err
		}
		proj, err := s.projects.Get(ctx, r.ProjectID)
		if err != nil {
			return RoleView{}, mapStoreErr(err)
		}
		c, n, w, v, err := s.sealConfig(proj, id, *cfg)
		if err != nil {
			return RoleView{}, err
		}
		ct, nonce, wrapped, kekVer = c, n, w, &v
	}
	if err := s.roles.Update(ctx, id, defaultTTL, maxTTL, ct, nonce, wrapped, kekVer); err != nil {
		return RoleView{}, mapStoreErr(err)
	}
	return s.GetRole(ctx, id)
}

// DeleteRole revokes every still-active lease for the role, then deletes it. If
// any revocation fails the role is left in place so leases are never orphaned.
func (s *Service) DeleteRole(ctx context.Context, id string) error {
	if s.kr.Sealed() {
		return ErrSealed
	}
	leases, err := s.leases.ListByRole(ctx, id)
	if err != nil {
		return mapStoreErr(err)
	}
	for _, l := range leases {
		if l.Status == "active" || l.Status == "creating" || l.Status == "revoke_failed" {
			if err := s.revoke(ctx, l, "revoked"); err != nil {
				return err
			}
		}
	}
	_ = time.Now // (keeps import if trimmed) -- remove if unused
	return mapStoreErr(s.roles.Delete(ctx, id))
}
```

> Remove the `time` import + `_ = time.Now` line if `go vet` flags it unused; it is only present as a guard and not required.

- [ ] **Step 2: Write `crud_test.go` (unit, fake keyring)**

Reuse the fake-keyring test harness from `internal/rotation/rotation_test.go` (search it for the in-memory `keyring` fake and the helper that seeds a project with a real wrapped KEK). Open `internal/rotation/crud_test.go` and mirror its setup. The test asserts: create validates required placeholders; create rejects `max < default`; duplicate name → `ErrExists`; get/list round-trip; update re-seals config.

```go
package dynamic

import (
	"context"
	"testing"
)

func TestCreateRoleValidation(t *testing.T) {
	s := newTestService(t) // helper below
	ctx := context.Background()
	configID := seedConfig(t, ctx, s) // creates project+env+config with real KEK

	// Missing {{password}} placeholder → invalid.
	_, err := s.CreateRole(ctx, RoleInput{
		ConfigID: configID, Name: "ro", DefaultTTLSeconds: 60, MaxTTLSeconds: 120,
		Config: RoleConfig{AdminDSN: "postgres://x", CreationStatements: `CREATE ROLE "{{name}}";`},
	}, "tester")
	if err != ErrInvalidConfig {
		t.Fatalf("want ErrInvalidConfig, got %v", err)
	}

	// max < default → invalid.
	_, err = s.CreateRole(ctx, RoleInput{
		ConfigID: configID, Name: "ro", DefaultTTLSeconds: 120, MaxTTLSeconds: 60,
		Config: RoleConfig{AdminDSN: "postgres://x", CreationStatements: `CREATE ROLE "{{name}}" PASSWORD '{{password}}';`},
	}, "tester")
	if err != ErrInvalidConfig {
		t.Fatalf("want ErrInvalidConfig for bad ttls, got %v", err)
	}

	// Valid create + duplicate name.
	v, err := s.CreateRole(ctx, RoleInput{
		ConfigID: configID, Name: "ro", DefaultTTLSeconds: 60, MaxTTLSeconds: 120,
		Config: RoleConfig{AdminDSN: "postgres://x", CreationStatements: `CREATE ROLE "{{name}}" PASSWORD '{{password}}';`},
	}, "tester")
	if err != nil {
		t.Fatalf("valid create: %v", err)
	}
	if v.Name != "ro" {
		t.Fatalf("unexpected view: %+v", v)
	}
	if _, err := s.CreateRole(ctx, RoleInput{
		ConfigID: configID, Name: "ro", DefaultTTLSeconds: 60, MaxTTLSeconds: 120,
		Config: RoleConfig{AdminDSN: "postgres://x", CreationStatements: `CREATE ROLE "{{name}}" PASSWORD '{{password}}';`},
	}, "tester"); err != ErrExists {
		t.Fatalf("want ErrExists, got %v", err)
	}
}
```

> **`newTestService`/`seedConfig`:** Add a small helper file `internal/dynamic/helpers_test.go` that constructs a `*Service` bound to `testStore` with a real `*crypto.Keyring` (unsealed with a test master key) — copy the exact keyring bootstrap used in `internal/rotation/rotation_test.go` (it already unseals a keyring and seeds a project's wrapped KEK). `seedConfig` inserts project+env+config and returns the config id. These DB-backed tests SKIP when `testStore == nil`.

- [ ] **Step 3: Run tests**

Run: `go test ./internal/dynamic/ -run 'TestCreateRoleValidation' -v`
Expected: PASS (or SKIP without Docker).

- [ ] **Step 4: Commit**

```bash
git add internal/dynamic/crud.go internal/dynamic/crud_test.go internal/dynamic/helpers_test.go
git commit -m "feat(dynamic): role CRUD with placeholder + TTL validation"
```

---

## Task 7: Issue credentials (crash-safe) + revoke/renew

**Files:**
- Create: `internal/dynamic/issue.go`
- Create: `internal/dynamic/lease.go`
- Test: `internal/dynamic/issue_test.go`

- [ ] **Step 1: Write `lease.go` (revocation + renew helpers first — issue depends on revoke)**

`internal/dynamic/lease.go`:
```go
package dynamic

import (
	"context"
	"strings"
	"time"

	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/store"
)

// revoke drops the lease's DB role and marks it with the given terminal status
// ('revoked' or 'expired'). On failure it records 'revoke_failed' for retry and
// returns the error. Loads the owning role/project to decrypt the admin DSN.
func (s *Service) revoke(ctx context.Context, l *store.DynamicLease, terminal string) error {
	role, err := s.roles.Get(ctx, l.RoleID)
	if err != nil {
		return mapStoreErr(err)
	}
	proj, err := s.projects.Get(ctx, l.ProjectID)
	if err != nil {
		return mapStoreErr(err)
	}
	cfg, err := s.openConfig(proj, role) // ErrSealed while sealed
	if err != nil {
		return err
	}
	stmts := cfg.RevocationStatements
	if strings.TrimSpace(stmts) == "" {
		stmts = `DROP ROLE IF EXISTS "{{name}}";`
	}
	sql, err := interpolate(stmts, l.DBUsername, "", l.ExpiresAt)
	if err != nil {
		_ = s.leases.MarkRevokeFailed(ctx, l.ID, "invalid config")
		return err
	}
	if err := runStatements(ctx, cfg.AdminDSN, sql); err != nil {
		_ = s.leases.MarkRevokeFailed(ctx, l.ID, sanitize(err))
		return err
	}
	if err := s.leases.MarkRevoked(ctx, l.ID, terminal, s.now()); err != nil {
		return mapStoreErr(err)
	}
	return nil
}

func (s *Service) RevokeLease(ctx context.Context, id string) error {
	l, err := s.leases.Get(ctx, id)
	if err != nil {
		return mapStoreErr(err)
	}
	if l.Status == "revoked" || l.Status == "expired" {
		return nil // idempotent
	}
	if s.kr.Sealed() {
		return ErrSealed
	}
	if err := s.revoke(ctx, l, "revoked"); err != nil {
		return err
	}
	s.recordLease(ctx, l, "dynamic.lease.revoke")
	return nil
}

func (s *Service) RenewLease(ctx context.Context, id string) (LeaseView, error) {
	l, err := s.leases.Get(ctx, id)
	if err != nil {
		return LeaseView{}, mapStoreErr(err)
	}
	if l.Status != "active" {
		return LeaseView{}, ErrNotRenewable
	}
	if s.kr.Sealed() {
		return LeaseView{}, ErrSealed
	}
	role, err := s.roles.Get(ctx, l.RoleID)
	if err != nil {
		return LeaseView{}, mapStoreErr(err)
	}
	proj, err := s.projects.Get(ctx, l.ProjectID)
	if err != nil {
		return LeaseView{}, mapStoreErr(err)
	}
	cfg, err := s.openConfig(proj, role)
	if err != nil {
		return LeaseView{}, err
	}
	newExpiry := s.now().Add(time.Duration(role.DefaultTTLSeconds) * time.Second)
	if newExpiry.After(l.MaxExpiresAt) {
		newExpiry = l.MaxExpiresAt
	}
	stmts := cfg.RenewStatements
	if strings.TrimSpace(stmts) == "" {
		stmts = `ALTER ROLE "{{name}}" VALID UNTIL '{{expiration}}';`
	}
	sql, err := interpolate(stmts, l.DBUsername, "", newExpiry)
	if err != nil {
		return LeaseView{}, err
	}
	if err := runStatements(ctx, cfg.AdminDSN, sql); err != nil {
		return LeaseView{}, err
	}
	if err := s.leases.Renew(ctx, id, newExpiry, s.now()); err != nil {
		return LeaseView{}, mapStoreErr(err)
	}
	s.recordLease(ctx, l, "dynamic.lease.renew")
	updated, err := s.leases.Get(ctx, id)
	if err != nil {
		return LeaseView{}, mapStoreErr(err)
	}
	return leaseView(updated), nil
}

// recordLease writes a value-free audit event for a lease action (system actor
// for scheduler events, resource path = configs/<config>/dynamic/<role>). Never
// includes the password.
func (s *Service) recordLease(ctx context.Context, l *store.DynamicLease, action string) {
	if s.audit == nil {
		return
	}
	err := s.audit.Record(ctx, audit.Event{
		Actor:    audit.Actor{Kind: "system", Name: "dynamic:" + l.RoleID},
		Action:   action,
		Resource: "dynamic/roles/" + l.RoleID + "/leases/" + l.ID,
		Result:   "success",
	})
	if err != nil {
		s.logger.Warn("dynamic audit write failed", "lease", l.ID, "err", err)
	}
}
```

- [ ] **Step 2: Write `issue.go`**

`internal/dynamic/issue.go`:
```go
package dynamic

import (
	"context"
	"time"

	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/store"
)

// IssueCreds mints a short-lived Postgres role from the dynamic role's creation
// template and records a lease. Crash-safe persist->apply->commit: the lease is
// persisted 'creating' BEFORE any DB change, so a crash after CREATE ROLE leaves
// a row the lease manager reclaims (the caller received no password). The
// generated password is returned once and never persisted.
func (s *Service) IssueCreds(ctx context.Context, roleID, createdBy string) (Creds, error) {
	if s.kr.Sealed() {
		return Creds{}, ErrSealed
	}
	role, err := s.roles.Get(ctx, roleID)
	if err != nil {
		return Creds{}, mapStoreErr(err)
	}
	proj, err := s.projects.Get(ctx, role.ProjectID)
	if err != nil {
		return Creds{}, mapStoreErr(err)
	}
	cfg, err := s.openConfig(proj, role)
	if err != nil {
		return Creds{}, err
	}

	username, err := generateUsername(role.Name)
	if err != nil {
		return Creds{}, err
	}
	password, err := generatePassword(defaultPasswdLen)
	if err != nil {
		return Creds{}, err
	}
	now := s.now()
	expires := now.Add(time.Duration(role.DefaultTTLSeconds) * time.Second)
	maxExpires := now.Add(time.Duration(role.MaxTTLSeconds) * time.Second)

	id, err := s.st.NewID(ctx)
	if err != nil {
		return Creds{}, err
	}
	lease := &store.DynamicLease{
		ID: id, RoleID: role.ID, ProjectID: role.ProjectID, DBUsername: username,
		ExpiresAt: expires, MaxExpiresAt: maxExpires, CreatedBy: createdBy,
	}
	// Reserve.
	if err := s.leases.Create(ctx, lease); err != nil {
		return Creds{}, mapStoreErr(err)
	}
	// Apply.
	sql, err := interpolate(cfg.CreationStatements, username, password, expires)
	if err != nil {
		_ = s.revoke(ctx, lease, "revoked") // clean up the reserved role/row
		return Creds{}, err
	}
	if err := runStatements(ctx, cfg.AdminDSN, sql); err != nil {
		_ = s.revoke(ctx, lease, "revoked") // DROP ROLE IF EXISTS is idempotent
		return Creds{}, err
	}
	// Commit.
	if err := s.leases.Activate(ctx, lease.ID); err != nil {
		return Creds{}, mapStoreErr(err)
	}
	s.recordIssue(ctx, role, lease)
	return Creds{LeaseID: lease.ID, Username: username, Password: password, ExpiresAt: expires}, nil
}

// recordIssue audits a credential issue (role name + lease id + db_username;
// NEVER the password).
func (s *Service) recordIssue(ctx context.Context, role *store.DynamicRole, lease *store.DynamicLease) {
	if s.audit == nil {
		return
	}
	err := s.audit.Record(ctx, audit.Event{
		Actor:    audit.Actor{Kind: "system", Name: "dynamic:" + role.ID},
		Action:   "dynamic.creds.issue",
		Resource: "dynamic/roles/" + role.ID + "/leases/" + lease.ID,
		Detail:   "db_user=" + lease.DBUsername,
		Result:   "success",
	})
	if err != nil {
		s.logger.Warn("dynamic audit write failed", "lease", lease.ID, "err", err)
	}
}
```

- [ ] **Step 3: Write the integration test (testcontainers)**

`internal/dynamic/issue_test.go`:
```go
package dynamic

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestIssueRenewRevokeRoundTrip(t *testing.T) {
	if testDSN == "" {
		t.Skip("postgres/docker not available")
	}
	ctx := context.Background()
	s := newTestService(t)
	configID := seedConfig(t, ctx, s)

	role, err := s.CreateRole(ctx, RoleInput{
		ConfigID: configID, Name: "ro", DefaultTTLSeconds: 3600, MaxTTLSeconds: 7200,
		Config: RoleConfig{
			AdminDSN:           testDSN,
			CreationStatements: `CREATE ROLE "{{name}}" LOGIN PASSWORD '{{password}}' VALID UNTIL '{{expiration}}';`,
		},
	}, "tester")
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	creds, err := s.IssueCreds(ctx, role.ID, "tester")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// The generated credential can log in.
	cfg, _ := pgx.ParseConfig(testDSN)
	cfg.User = creds.Username
	cfg.Password = creds.Password
	c, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("issued creds should connect: %v", err)
	}
	c.Close(ctx)

	// Renew succeeds on an active lease.
	if _, err := s.RenewLease(ctx, creds.LeaseID); err != nil {
		t.Fatalf("renew: %v", err)
	}

	// Revoke drops the role; a second revoke is idempotent.
	if err := s.RevokeLease(ctx, creds.LeaseID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := s.RevokeLease(ctx, creds.LeaseID); err != nil {
		t.Fatalf("double revoke: %v", err)
	}
	// The dropped role can no longer connect.
	if c2, err := pgx.ConnectConfig(ctx, cfg); err == nil {
		c2.Close(ctx)
		t.Fatal("revoked role should not connect")
	}
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/dynamic/ -run 'TestIssueRenewRevokeRoundTrip' -v`
Expected: PASS (or SKIP without Docker).

- [ ] **Step 5: Commit**

```bash
git add internal/dynamic/issue.go internal/dynamic/lease.go internal/dynamic/issue_test.go
git commit -m "feat(dynamic): crash-safe credential issue + renew + revoke"
```

---

## Task 8: Lease manager — scheduler + post-unseal sweep

**Files:**
- Create: `internal/dynamic/scheduler.go`
- Test: `internal/dynamic/scheduler_test.go`

- [ ] **Step 1: Write `scheduler.go`**

`internal/dynamic/scheduler.go`:
```go
package dynamic

import (
	"context"
	"time"
)

// RunDue revokes every lease the lease-manager must act on: active leases past
// expiry, prior revoke failures, and crash-orphaned 'creating' rows older than
// creatingGrace. No-op while sealed. Per-lease errors are logged and never abort
// the pass.
func (s *Service) RunDue(ctx context.Context) {
	if s.kr.Sealed() {
		return
	}
	now := s.now()
	leases, err := s.leases.ClaimDue(ctx, now, now.Add(-creatingGrace), defaultBatch)
	if err != nil {
		s.logger.Warn("dynamic claim-due failed", "err", err)
		return
	}
	for _, l := range leases {
		if ctx.Err() != nil {
			return
		}
		if err := s.revoke(ctx, l, "expired"); err != nil {
			s.logger.Warn("dynamic lease revoke failed", "lease", l.ID, "err", sanitize(err))
			continue
		}
		s.recordLease(ctx, l, "dynamic.lease.expire")
	}
}

// SweepOrphanedLeases runs one immediate RunDue pass. It is invoked right after
// the keyring transitions sealed->unsealed (see unsealNow), so leases orphaned
// by a crash — including in-flight 'creating' rows and leases that expired while
// the server was down — are revoked promptly rather than waiting a full tick.
func (s *Service) SweepOrphanedLeases(ctx context.Context) {
	s.RunDue(ctx)
}

// RunScheduler ticks every `tick` and revokes due leases until ctx is done.
// tick <= 0 disables it. Ties to the server shutdown context.
func (s *Service) RunScheduler(ctx context.Context, tick time.Duration) {
	if tick <= 0 {
		return
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	s.logger.Info("dynamic lease scheduler started", "tick", tick.String())
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("dynamic lease scheduler stopping")
			return
		case <-t.C:
			s.RunDue(ctx)
		}
	}
}
```

- [ ] **Step 2: Write the scheduler test**

`internal/dynamic/scheduler_test.go`:
```go
package dynamic

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestRunDueRevokesExpiredLease(t *testing.T) {
	if testDSN == "" {
		t.Skip("postgres/docker not available")
	}
	ctx := context.Background()
	s := newTestService(t)
	configID := seedConfig(t, ctx, s)

	role, err := s.CreateRole(ctx, RoleInput{
		ConfigID: configID, Name: "exp", DefaultTTLSeconds: 3600, MaxTTLSeconds: 7200,
		Config: RoleConfig{AdminDSN: testDSN, CreationStatements: `CREATE ROLE "{{name}}" LOGIN PASSWORD '{{password}}';`},
	}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	creds, err := s.IssueCreds(ctx, role.ID, "tester")
	if err != nil {
		t.Fatal(err)
	}

	// Advance the engine clock past expiry.
	base := time.Now()
	s.now = func() time.Time { return base.Add(2 * time.Hour) }

	s.RunDue(ctx)

	l, err := s.leases.Get(ctx, creds.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	if l.Status != "expired" {
		t.Fatalf("want expired, got %s", l.Status)
	}
	// Role really gone.
	cfg, _ := pgx.ParseConfig(testDSN)
	cfg.User, cfg.Password = creds.Username, creds.Password
	if c, err := pgx.ConnectConfig(ctx, cfg); err == nil {
		c.Close(ctx)
		t.Fatal("expired role should not connect")
	}
}

func TestRunDueNoopWhileSealed(t *testing.T) {
	s := newSealedTestService(t) // keyring reporting Sealed()==true; see helpers_test.go
	// Should return without touching the store (no panic, no error path).
	s.RunDue(context.Background())
}
```

> Add `newSealedTestService` to `helpers_test.go`: same as `newTestService` but with a keyring that is sealed (do not unseal it). If constructing a sealed `*crypto.Keyring` is awkward, this second test may instead assert via the fake-keyring interface used by the rotation package's sealed test — mirror whatever `internal/rotation/scheduler_test.go` does for its sealed no-op case.

- [ ] **Step 3: Run the tests**

Run: `go test ./internal/dynamic/ -run 'TestRunDue' -v`
Expected: PASS (DB test SKIP without Docker; the sealed no-op test runs always).

- [ ] **Step 4: Commit**

```bash
git add internal/dynamic/scheduler.go internal/dynamic/scheduler_test.go
git commit -m "feat(dynamic): lease-manager scheduler + post-unseal sweep"
```

---

## Task 9: AuthZ actions — `DynamicManage` + `DynamicIssue`

**Files:**
- Modify: `internal/authz/actions.go`
- Test: `internal/authz/actions_test.go` (or the existing matrix test file)

- [ ] **Step 1: Write the failing test**

Find the existing role-matrix test (search `internal/authz` for a test asserting `roleAllows`). Add:
```go
func TestDynamicActionMatrix(t *testing.T) {
	// Manage is admin+; Issue is developer+ (a developer can lease creds).
	if roleAllows(RoleDeveloper, DynamicManage) {
		t.Fatal("developer must NOT have DynamicManage")
	}
	if !roleAllows(RoleAdmin, DynamicManage) {
		t.Fatal("admin must have DynamicManage")
	}
	if !roleAllows(RoleDeveloper, DynamicIssue) {
		t.Fatal("developer must have DynamicIssue")
	}
	if !roleAllows(RoleViewer, DynamicIssue) == true {
		// viewer must NOT issue
	}
	if roleAllows(RoleViewer, DynamicIssue) {
		t.Fatal("viewer must NOT have DynamicIssue")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/authz/ -run TestDynamicActionMatrix -v`
Expected: FAIL — `undefined: DynamicManage`.

- [ ] **Step 3: Add the actions**

In `internal/authz/actions.go`, add to the const block (after `SyncManage`):
```go
	DynamicManage Action = "dynamic:manage" // project-scoped (create/update/delete roles)
	DynamicIssue  Action = "dynamic:issue"  // project-scoped (issue/renew/revoke leases)
```
Add `DynamicIssue` to `developerActions`:
```go
	developerActions = union(viewerActions, setOf(SecretWrite, ConfigCreate, TransitUse, DynamicIssue))
```
Add `DynamicManage` to the `adminActions` set list (append to the existing `setOf(...)`):
```go
		TokenRead, TokenMint, TokenRevoke, UserManage, AuditRead, SysSeal, SysBackup, TransitManage, OIDCManage, RotationManage, SyncManage, DynamicManage))
```

- [ ] **Step 4: Run the test**

Run: `go test ./internal/authz/ -run TestDynamicActionMatrix -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/authz/actions.go internal/authz/actions_test.go
git commit -m "feat(dynamic): authz actions dynamic:manage + dynamic:issue"
```

---

## Task 10: API handlers + routes + error code

**Files:**
- Modify: `internal/api/errors.go` (add `CodeDynamicNotFound`)
- Create: `internal/api/dynamic_handlers.go`
- Modify: `internal/api/server.go` (add `dynamic` field, `New` param, routes)
- Test: `internal/api/dynamic_e2e_test.go`

- [ ] **Step 1: Add the error code**

In `internal/api/errors.go`, after `CodeSyncNotFound`:
```go
	CodeDynamicNotFound    = "dynamic_not_found"
```

- [ ] **Step 2: Write `dynamic_handlers.go`**

Mirror `internal/api/rotation_handlers.go` exactly (masked views, `resolveScopeResource`, `authorize`/`can`/`record`, `writeXErr`). `config` scope resolution is via `resolveScopeResource(ctx, "config", configID)`.

`internal/api/dynamic_handlers.go`:
```go
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/dynamic"
)

type dynamicConfigReq struct {
	AdminDSN             string `json:"admin_dsn,omitempty"`
	CreationStatements   string `json:"creation_statements,omitempty"`
	RevocationStatements string `json:"revocation_statements,omitempty"`
	RenewStatements      string `json:"renew_statements,omitempty"`
}

func (c dynamicConfigReq) toEngine() dynamic.RoleConfig {
	return dynamic.RoleConfig{
		AdminDSN: c.AdminDSN, CreationStatements: c.CreationStatements,
		RevocationStatements: c.RevocationStatements, RenewStatements: c.RenewStatements,
	}
}

type createRoleReq struct {
	ConfigID          string           `json:"config_id"`
	Name              string           `json:"name"`
	DefaultTTLSeconds int64            `json:"default_ttl_seconds"`
	MaxTTLSeconds     int64            `json:"max_ttl_seconds"`
	Config            dynamicConfigReq `json:"config"`
}

type updateRoleReq struct {
	DefaultTTLSeconds *int64            `json:"default_ttl_seconds"`
	MaxTTLSeconds     *int64            `json:"max_ttl_seconds"`
	Config            *dynamicConfigReq `json:"config"`
}

type roleViewJSON struct {
	ID                string `json:"id"`
	ProjectID         string `json:"project_id"`
	ConfigID          string `json:"config_id"`
	Name              string `json:"name"`
	DefaultTTLSeconds int64  `json:"default_ttl_seconds"`
	MaxTTLSeconds     int64  `json:"max_ttl_seconds"`
	CreatedAt         string `json:"created_at"`
}

func toRoleViewJSON(v dynamic.RoleView) roleViewJSON {
	return roleViewJSON{
		ID: v.ID, ProjectID: v.ProjectID, ConfigID: v.ConfigID, Name: v.Name,
		DefaultTTLSeconds: v.DefaultTTLSeconds, MaxTTLSeconds: v.MaxTTLSeconds,
		CreatedAt: v.CreatedAt.UTC().Format(time.RFC3339),
	}
}

type leaseViewJSON struct {
	ID           string  `json:"id"`
	RoleID       string  `json:"role_id"`
	Status       string  `json:"status"`
	DBUsername   string  `json:"db_username"`
	ExpiresAt    string  `json:"expires_at"`
	MaxExpiresAt string  `json:"max_expires_at"`
	RenewedAt    *string `json:"renewed_at,omitempty"`
	CreatedAt    string  `json:"created_at"`
}

func toLeaseViewJSON(v dynamic.LeaseView) leaseViewJSON {
	out := leaseViewJSON{
		ID: v.ID, RoleID: v.RoleID, Status: v.Status, DBUsername: v.DBUsername,
		ExpiresAt: v.ExpiresAt.UTC().Format(time.RFC3339), MaxExpiresAt: v.MaxExpiresAt.UTC().Format(time.RFC3339),
		CreatedAt: v.CreatedAt.UTC().Format(time.RFC3339),
	}
	if v.RenewedAt != nil {
		s := v.RenewedAt.UTC().Format(time.RFC3339)
		out.RenewedAt = &s
	}
	return out
}

func (s *Server) writeDynamicErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, dynamic.ErrNotFound):
		writeError(w, http.StatusNotFound, CodeDynamicNotFound, "dynamic role or lease not found")
	case errors.Is(err, dynamic.ErrExists):
		writeError(w, http.StatusConflict, "conflict", "a dynamic role already exists for this config and name")
	case errors.Is(err, dynamic.ErrInvalidConfig):
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid dynamic role configuration")
	case errors.Is(err, dynamic.ErrNotRenewable):
		writeError(w, http.StatusConflict, "conflict", "lease is not active")
	case errors.Is(err, dynamic.ErrSealed):
		writeError(w, http.StatusServiceUnavailable, CodeSealed, "server is sealed")
	default:
		s.writeServiceError(w, err)
	}
}

func (s *Server) handleDynamicRoleCreate(w http.ResponseWriter, r *http.Request) {
	var req createRoleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ConfigID == "" || req.Name == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "config_id, name, ttls, config are required")
		return
	}
	res, err := s.resolveScopeResource(r.Context(), "config", req.ConfigID)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !s.authorize(w, r, authz.DynamicManage, res, "dynamic.role.create", "configs/"+req.ConfigID) {
		return
	}
	v, err := s.dynamic.CreateRole(r.Context(), dynamic.RoleInput{
		ConfigID: req.ConfigID, Name: req.Name,
		DefaultTTLSeconds: req.DefaultTTLSeconds, MaxTTLSeconds: req.MaxTTLSeconds,
		Config: req.Config.toEngine(),
	}, principalName(r))
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	if err := s.record(r, "dynamic.role.create", "dynamic/roles/"+v.ID, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, toRoleViewJSON(v))
}

func (s *Server) handleDynamicRoleList(w http.ResponseWriter, r *http.Request) {
	configID := r.URL.Query().Get("config_id")
	if configID == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "config_id is required")
		return
	}
	res, err := s.resolveScopeResource(r.Context(), "config", configID)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.can(r, authz.DynamicManage, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	vs, err := s.dynamic.ListRolesByConfig(r.Context(), configID)
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	out := make([]roleViewJSON, 0, len(vs))
	for _, v := range vs {
		out = append(out, toRoleViewJSON(v))
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": out})
}

// dynamicRoleResource loads a role and returns its config-scoped authz resource.
func (s *Server) dynamicRoleResource(r *http.Request) (authz.Resource, dynamic.RoleView, error) {
	id := chi.URLParam(r, "id")
	v, err := s.dynamic.GetRole(r.Context(), id)
	if err != nil {
		return authz.Resource{}, dynamic.RoleView{}, err
	}
	res, err := s.resolveScopeResource(r.Context(), "config", v.ConfigID)
	return res, v, err
}

func (s *Server) handleDynamicRoleGet(w http.ResponseWriter, r *http.Request) {
	res, v, err := s.dynamicRoleResource(r)
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	if err := s.can(r, authz.DynamicManage, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toRoleViewJSON(v))
}

func (s *Server) handleDynamicRoleUpdate(w http.ResponseWriter, r *http.Request) {
	res, v, err := s.dynamicRoleResource(r)
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	if !s.authorize(w, r, authz.DynamicManage, res, "dynamic.role.update", "dynamic/roles/"+v.ID) {
		return
	}
	var req updateRoleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	var cfg *dynamic.RoleConfig
	if req.Config != nil {
		c := req.Config.toEngine()
		cfg = &c
	}
	updated, err := s.dynamic.UpdateRole(r.Context(), v.ID, req.DefaultTTLSeconds, req.MaxTTLSeconds, cfg)
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	if err := s.record(r, "dynamic.role.update", "dynamic/roles/"+v.ID, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, toRoleViewJSON(updated))
}

func (s *Server) handleDynamicRoleDelete(w http.ResponseWriter, r *http.Request) {
	res, v, err := s.dynamicRoleResource(r)
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	if !s.authorize(w, r, authz.DynamicManage, res, "dynamic.role.delete", "dynamic/roles/"+v.ID) {
		return
	}
	if err := s.dynamic.DeleteRole(r.Context(), v.ID); err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	if err := s.record(r, "dynamic.role.delete", "dynamic/roles/"+v.ID, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (s *Server) handleDynamicIssue(w http.ResponseWriter, r *http.Request) {
	res, v, err := s.dynamicRoleResource(r)
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	if !s.authorize(w, r, authz.DynamicIssue, res, "dynamic.creds.issue", "dynamic/roles/"+v.ID) {
		return
	}
	// The engine writes its own dynamic.creds.issue audit event (system actor).
	creds, err := s.dynamic.IssueCreds(r.Context(), v.ID, principalName(r))
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"lease_id":   creds.LeaseID,
		"username":   creds.Username,
		"password":   creds.Password,
		"expires_at": creds.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleDynamicLeaseList(w http.ResponseWriter, r *http.Request) {
	roleID := r.URL.Query().Get("role_id")
	if roleID == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "role_id is required")
		return
	}
	v, err := s.dynamic.GetRole(r.Context(), roleID)
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	res, err := s.resolveScopeResource(r.Context(), "config", v.ConfigID)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.can(r, authz.DynamicIssue, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	vs, err := s.dynamic.ListLeasesByRole(r.Context(), roleID)
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	out := make([]leaseViewJSON, 0, len(vs))
	for _, lv := range vs {
		out = append(out, toLeaseViewJSON(lv))
	}
	writeJSON(w, http.StatusOK, map[string]any{"leases": out})
}

// dynamicLeaseResource loads a lease, its role, and the config-scoped resource.
func (s *Server) dynamicLeaseResource(r *http.Request) (authz.Resource, dynamic.LeaseView, error) {
	id := chi.URLParam(r, "id")
	lv, err := s.dynamic.GetLease(r.Context(), id)
	if err != nil {
		return authz.Resource{}, dynamic.LeaseView{}, err
	}
	role, err := s.dynamic.GetRole(r.Context(), lv.RoleID)
	if err != nil {
		return authz.Resource{}, dynamic.LeaseView{}, err
	}
	res, err := s.resolveScopeResource(r.Context(), "config", role.ConfigID)
	return res, lv, err
}

func (s *Server) handleDynamicLeaseRenew(w http.ResponseWriter, r *http.Request) {
	res, lv, err := s.dynamicLeaseResource(r)
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	if !s.authorize(w, r, authz.DynamicIssue, res, "dynamic.lease.renew", "dynamic/leases/"+lv.ID) {
		return
	}
	updated, err := s.dynamic.RenewLease(r.Context(), lv.ID)
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toLeaseViewJSON(updated))
}

func (s *Server) handleDynamicLeaseRevoke(w http.ResponseWriter, r *http.Request) {
	res, lv, err := s.dynamicLeaseResource(r)
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	if !s.authorize(w, r, authz.DynamicIssue, res, "dynamic.lease.revoke", "dynamic/leases/"+lv.ID) {
		return
	}
	if err := s.dynamic.RevokeLease(r.Context(), lv.ID); err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": true})
}
```

- [ ] **Step 3: Add the engine methods the handlers require (`GetLease`, `ListLeasesByRole`)**

Append to `internal/dynamic/crud.go` (or a small `internal/dynamic/lease_query.go`):
```go
func (s *Service) GetLease(ctx context.Context, id string) (LeaseView, error) {
	l, err := s.leases.Get(ctx, id)
	if err != nil {
		return LeaseView{}, mapStoreErr(err)
	}
	return leaseView(l), nil
}

func (s *Service) ListLeasesByRole(ctx context.Context, roleID string) ([]LeaseView, error) {
	ls, err := s.leases.ListByRole(ctx, roleID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	out := make([]LeaseView, 0, len(ls))
	for _, l := range ls {
		out = append(out, leaseView(l))
	}
	return out, nil
}
```

- [ ] **Step 4: Wire the field, `New` param, and routes in `server.go`**

In `internal/api/server.go`:
- Add import `"github.com/steveokay/janus-secrets/internal/dynamic"`.
- Add field after `sync`: `dynamic *dynamic.Service // nil in unit-test servers that exercise no dynamic path`.
- Add `dyn *dynamic.Service` to the `New(...)` signature (after `syncSvc *secretsync.Service`) and set `dynamic: dyn` in the struct literal.
- Add the route group after the `s.sync != nil` block (mirror it):
```go
		if s.dynamic != nil {
			r.Group(func(r chi.Router) {
				r.Use(RequireUnsealed(s.keyring))
				r.Post("/v1/dynamic/roles", s.handleDynamicRoleCreate)
				r.Get("/v1/dynamic/roles", s.handleDynamicRoleList)
				r.Get("/v1/dynamic/roles/{id}", s.handleDynamicRoleGet)
				r.Patch("/v1/dynamic/roles/{id}", s.handleDynamicRoleUpdate)
				r.Delete("/v1/dynamic/roles/{id}", s.handleDynamicRoleDelete)
				r.Post("/v1/dynamic/roles/{id}/creds", s.handleDynamicIssue)
				r.Get("/v1/dynamic/leases", s.handleDynamicLeaseList)
				r.Post("/v1/dynamic/leases/{id}/renew", s.handleDynamicLeaseRenew)
				r.Post("/v1/dynamic/leases/{id}/revoke", s.handleDynamicLeaseRevoke)
			})
		}
```

> Check the exact middleware used by the rotation/sync groups in `server.go:240-260` (whether they wrap in `RequireUnsealed` or rely on the outer authed router). Match that grouping precisely rather than the sketch above.

- [ ] **Step 5: Update every `New(...)` caller**

Search: `grep -rn "api.New(\|= New(Config" internal/api cmd`. Update `boot.go` and any test constructing `New(...)` to pass the new `dyn` argument (pass `nil` in unit-test servers that don't exercise dynamic). Task 11 updates `boot.go` concretely.

- [ ] **Step 6: Write the e2e test**

Mirror `internal/api/rotation_e2e_test.go`. Cover: admin creates a role (developer gets 403 on manage), developer issues creds (200 + password present), viewer gets 403 on issue, renew + revoke succeed, masked list never contains admin DSN/statements/password. Open `rotation_e2e_test.go` for the exact test-server bootstrap (`newTestServer`, token minting, `do(...)` helper) and reuse it; the dynamic role `config` body needs a Postgres `admin_dsn` — use the e2e harness's test container DSN if available, else assert only the non-DB paths (create/list/authz/masking) and gate the issue/renew/revoke assertions on `testDSN != ""`.

- [ ] **Step 7: Build + run api tests**

Run: `go build ./... && go test ./internal/api/ -run Dynamic -v`
Expected: build OK; tests PASS (DB-dependent assertions SKIP without Docker).

- [ ] **Step 8: Commit**

```bash
git add internal/api/errors.go internal/api/dynamic_handlers.go internal/api/server.go internal/dynamic/crud.go internal/api/dynamic_e2e_test.go
git commit -m "feat(dynamic): /v1/dynamic API — roles, creds, leases"
```

---

## Task 11: Server wiring — BootConfig, env var, scheduler, sweep hook

**Files:**
- Modify: `internal/api/boot.go` (construct `dynamicSvc`, pass to `New`, launch scheduler)
- Modify: `internal/api/boot.go` `BootConfig` struct (add `DynamicTick`)
- Modify: `internal/api/sys.go` (`unsealNow` sweep hook)
- Modify: `cmd/janus/server.go` (parse `JANUS_DYNAMIC_TICK`, set `BootConfig.DynamicTick`)

- [ ] **Step 1: Add `DynamicTick` to `BootConfig`**

In `internal/api/boot.go`, after the `SyncTick` field (~line 50):
```go
	// DynamicTick is the dynamic lease-manager tick interval. Zero disables the
	// scheduler (tests).
	DynamicTick time.Duration
```

- [ ] **Step 2: Construct + wire the service in `Boot`**

In `internal/api/boot.go`, after `syncSvc := secretsync.New(...)` (~line 127):
```go
	dynamicSvc := dynamic.New(kr, st, auditRec, logger)
```
Add import `"github.com/steveokay/janus-secrets/internal/dynamic"`.
Update the `New(...)` call (~line 140) to pass `dynamicSvc` after `syncSvc`:
```go
	srv := New(Config{ListenAddr: bc.ListenAddr, SealType: sealType, Version: bc.Version}, kr, unsealer, seals, svc, transitSvc, rotationSvc, syncSvc, dynamicSvc, authSvc, authorizer, st, auditRec, logger)
```
After the sync scheduler goroutine (~line 150):
```go
	// Start the dynamic lease-manager scheduler on the same boot ctx. Zero tick
	// (tests) disables it.
	if bc.DynamicTick > 0 {
		go dynamicSvc.RunScheduler(ctx, bc.DynamicTick)
	}
```

- [ ] **Step 3: Add the post-unseal sweep hook**

In `internal/api/sys.go` `unsealNow`, wrap the transition. Replace the body so the sealed state is captured before unseal and the sweep fires only on a sealed→unsealed edge:
```go
func (s *Server) unsealNow(ctx context.Context) error {
	wasSealed := s.keyring.Sealed()
	master, err := s.unsealer.Unseal(ctx)
	if err != nil {
		return err
	}
	defer zero(master)
	if err := s.keyring.Unseal(master); err != nil && !errors.Is(err, crypto.ErrAlreadyUnsealed) {
		return err
	}
	if s.auth != nil {
		if err := s.auth.EnsureHMACKey(ctx); err != nil {
			s.logger.Warn("auth hmac-key bootstrap failed; auth endpoints unavailable until retried", "err", err)
		}
	}
	// On the sealed->unsealed edge, reclaim leases orphaned by a crash (and any
	// that expired while the server was down). Runs in the background on a
	// detached context so it never blocks the unseal response.
	if wasSealed && !s.keyring.Sealed() && s.dynamic != nil {
		go s.dynamic.SweepOrphanedLeases(context.Background())
	}
	return nil
}
```
Add `"context"` to the `sys.go` imports if not already present.

- [ ] **Step 4: Parse `JANUS_DYNAMIC_TICK` in the CLI**

In `cmd/janus/server.go`, after the `syncTick` block (~line 62):
```go
	dynamicTick := 60 * time.Second // production default; 0 disables
	if v := os.Getenv("JANUS_DYNAMIC_TICK"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return fmt.Errorf("invalid JANUS_DYNAMIC_TICK %q: use a Go duration like 60s, or 0 to disable", v)
		}
		dynamicTick = d
	}
```
And set it in the `api.BootConfig{...}` literal (after `SyncTick: syncTick,`):
```go
		DynamicTick:        dynamicTick,
```

- [ ] **Step 5: Build + run the full server/api test suite**

Run: `go build ./... && go test ./internal/api/ ./cmd/... -count=1`
Expected: build OK; PASS. Fix any remaining `New(...)` callers in api tests that need the new `nil` dynamic arg.

- [ ] **Step 6: Commit**

```bash
git add internal/api/boot.go internal/api/sys.go cmd/janus/server.go
git commit -m "feat(dynamic): wire lease scheduler + JANUS_DYNAMIC_TICK + post-unseal sweep"
```

---

## Task 12: CLI — `janus dynamic`

**Files:**
- Create: `cmd/janus/dynamic_commands.go`
- Modify: `cmd/janus/main.go` (register `newDynamicCmd()`)
- Test: `cmd/janus/dynamic_commands_test.go`

- [ ] **Step 1: Write `dynamic_commands.go`**

Mirror `cmd/janus/rotation_commands.go`. Uses the same `newAPIClient(address, token)` + `c.call(method, path, body, out)` helpers.

`cmd/janus/dynamic_commands.go`:
```go
package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newDynamicCmd() *cobra.Command {
	var address, token string
	cmd := &cobra.Command{
		Use:   "dynamic",
		Short: "Manage dynamic Postgres credentials",
	}
	cmd.PersistentFlags().StringVar(&address, "address", "", "server address (default: stored/env)")
	cmd.PersistentFlags().StringVar(&token, "token", "", "service token (default: stored/env)")

	roles := &cobra.Command{Use: "roles", Short: "Manage dynamic roles"}

	// roles create
	var configID, name, adminDSN, creation, revocation, renew string
	var defaultTTL, maxTTL int64
	rolesCreate := &cobra.Command{
		Use: "create", Short: "Create a dynamic role",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			body := map[string]any{
				"config_id": configID, "name": name,
				"default_ttl_seconds": defaultTTL, "max_ttl_seconds": maxTTL,
				"config": map[string]any{
					"admin_dsn": adminDSN, "creation_statements": creation,
					"revocation_statements": revocation, "renew_statements": renew,
				},
			}
			var out map[string]any
			if err := c.call("POST", "/v1/dynamic/roles", body, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created dynamic role %v (%v)\n", out["id"], out["name"])
			return nil
		},
	}
	rolesCreate.Flags().StringVar(&configID, "config", "", "target config id (required)")
	rolesCreate.Flags().StringVar(&name, "name", "", "role name (required)")
	rolesCreate.Flags().Int64Var(&defaultTTL, "default-ttl-seconds", 3600, "default lease TTL")
	rolesCreate.Flags().Int64Var(&maxTTL, "max-ttl-seconds", 86400, "maximum lease TTL")
	rolesCreate.Flags().StringVar(&adminDSN, "admin-dsn", "", "postgres admin DSN (required)")
	rolesCreate.Flags().StringVar(&creation, "creation", "", "creation SQL with {{name}}/{{password}}/{{expiration}} (required)")
	rolesCreate.Flags().StringVar(&revocation, "revocation", "", "revocation SQL (optional; default DROP ROLE IF EXISTS)")
	rolesCreate.Flags().StringVar(&renew, "renew", "", "renew SQL (optional; default ALTER ROLE ... VALID UNTIL)")

	// roles list
	var listConfig string
	rolesList := &cobra.Command{
		Use: "list", Short: "List dynamic roles for a config",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out struct {
				Roles []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"roles"`
			}
			if err := c.call("GET", "/v1/dynamic/roles?config_id="+listConfig, nil, &out); err != nil {
				return err
			}
			for _, r := range out.Roles {
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %s\n", r.ID, r.Name)
			}
			return nil
		},
	}
	rolesList.Flags().StringVar(&listConfig, "config", "", "config id (required)")

	// roles delete
	rolesDelete := &cobra.Command{
		Use: "delete <id>", Short: "Delete a dynamic role (revokes its leases)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			return c.call("DELETE", "/v1/dynamic/roles/"+args[0], nil, nil)
		},
	}
	roles.AddCommand(rolesCreate, rolesList, rolesDelete)

	// creds <role-id>
	creds := &cobra.Command{
		Use: "creds <role-id>", Short: "Issue dynamic credentials", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out struct {
				LeaseID   string `json:"lease_id"`
				Username  string `json:"username"`
				Password  string `json:"password"`
				ExpiresAt string `json:"expires_at"`
			}
			if err := c.call("POST", "/v1/dynamic/roles/"+args[0]+"/creds", nil, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "lease=%s\nusername=%s\npassword=%s\nexpires=%s\n",
				out.LeaseID, out.Username, out.Password, out.ExpiresAt)
			return nil
		},
	}

	// renew <lease-id>
	renewCmd := &cobra.Command{
		Use: "renew <lease-id>", Short: "Renew a lease", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out struct {
				ExpiresAt string `json:"expires_at"`
			}
			if err := c.call("POST", "/v1/dynamic/leases/"+args[0]+"/renew", nil, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "renewed → expires %s\n", out.ExpiresAt)
			return nil
		},
	}

	// revoke <lease-id>
	revokeCmd := &cobra.Command{
		Use: "revoke <lease-id>", Short: "Revoke a lease", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			return c.call("POST", "/v1/dynamic/leases/"+args[0]+"/revoke", nil, nil)
		},
	}

	// leases list --role
	var leaseRole string
	leases := &cobra.Command{
		Use: "leases", Short: "List leases for a role",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out struct {
				Leases []struct {
					ID         string `json:"id"`
					Status     string `json:"status"`
					DBUsername string `json:"db_username"`
					ExpiresAt  string `json:"expires_at"`
				} `json:"leases"`
			}
			if err := c.call("GET", "/v1/dynamic/leases?role_id="+leaseRole, nil, &out); err != nil {
				return err
			}
			for _, l := range out.Leases {
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %-12s %-24s exp=%s\n", l.ID, l.Status, l.DBUsername, l.ExpiresAt)
			}
			return nil
		},
	}
	leases.Flags().StringVar(&leaseRole, "role", "", "role id (required)")

	cmd.AddCommand(roles, creds, renewCmd, revokeCmd, leases)
	return cmd
}
```

- [ ] **Step 2: Register in `main.go`**

In `cmd/janus/main.go`, add to the `root.AddCommand(...)` list (after `newSyncCmd(),`):
```go
		newDynamicCmd(),
```

- [ ] **Step 3: Write the CLI test**

Mirror `cmd/janus/rotation_commands_test.go` (it builds the command tree and asserts subcommands/flags exist; some use an httptest server + the `call` helper). At minimum assert the command wires up:
```go
package main

import "testing"

func TestDynamicCmdStructure(t *testing.T) {
	cmd := newDynamicCmd()
	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"roles", "creds", "renew", "revoke", "leases"} {
		if !names[want] {
			t.Fatalf("missing subcommand %q", want)
		}
	}
}
```

- [ ] **Step 4: Build + run CLI test**

Run: `go build ./... && go test ./cmd/... -run TestDynamicCmdStructure -v`
Expected: build OK; PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/janus/dynamic_commands.go cmd/janus/main.go cmd/janus/dynamic_commands_test.go
git commit -m "feat(dynamic): janus dynamic CLI (roles/creds/renew/revoke/leases)"
```

---

## Task 13: Secret-leak test + full-suite gate + docs

**Files:**
- Modify/extend: the existing grep-based log-leak test (find via `grep -rln "leak" internal/`)
- Modify: `docs/` status doc if the repo tracks phase progress (check recent commits — the last commit was a `docs(status)` update)

- [ ] **Step 1: Extend the leak test to cover a generated dynamic password**

Locate the existing leak test (e.g. `internal/api/*leak*_test.go` or a top-level one). Add a case that: issues creds through the dynamic engine (or the API), captures logs + a masked lease list, and asserts the returned `password` value never appears in either. If the existing leak harness is DB-independent, gate the new case on `testDSN != ""`. Follow the exact capture mechanism the existing test uses (do not invent a new logger).

```go
// Pseudocode shape — adapt to the existing leak-test harness:
// creds := issueViaEngineOrAPI(t)
// logs := capturedLogBuffer.String()
// if strings.Contains(logs, creds.Password) { t.Fatal("password leaked to logs") }
// listJSON := getLeaseList(t, creds.RoleID)
// if strings.Contains(listJSON, creds.Password) { t.Fatal("password leaked in lease list") }
```

- [ ] **Step 2: Run the whole backend suite + vet + vuln/gosec gates**

Run:
```bash
go build ./... && go vet ./... && go test ./... -count=1
```
Expected: all PASS (DB tests SKIP only if Docker unavailable — prefer running with Docker so the dynamic integration tests execute).

Then the security gates the repo runs in CI:
```bash
govulncheck ./... && gosec ./...
```
Expected: no findings (treat any as build failures per CLAUDE.md).

- [ ] **Step 3: Update the phase status doc**

If a status/progress doc exists (the last main commit was `docs(status): record Phase 3.1 … + 3.2 …`), append a Phase 3.3 entry summarizing dynamic Postgres credentials + lease manager. Match that doc's existing format.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "test(dynamic): password-leak coverage + docs(status): Phase 3.3 dynamic creds"
```

---

## Self-Review (completed by plan author)

**Spec coverage:**
- Vault-style templates → Task 4/5 (`RoleConfig`, `interpolate`, placeholder validation Task 6). ✓
- Config-scoped, envelope-encrypted → Task 2 (AAD), Task 3 (schema), Task 4 (`sealConfig`/`openConfig`), Task 6 (`projectForConfig`). ✓
- Two tables, password-never-persisted → Task 1, Task 7 (`IssueCreds` returns password, stores only `db_username`). ✓
- Crash-safe persist→apply→commit → Task 7 (reserve `creating` → apply → `Activate`). ✓
- Lease manager TTL/renew/revoke → Task 7 (`RenewLease`/`RevokeLease`), Task 8 (`RunDue`/`RunScheduler`). ✓
- Revoke-on-startup sweep → Task 8 (`SweepOrphanedLeases`) + Task 11 (`unsealNow` sealed→unsealed hook). Grace-window reclaim of `creating` orphans → Task 3 (`ClaimDue`) + Task 4 (`creatingGrace`). ✓
- `RenewStatements` default `ALTER ROLE … VALID UNTIL` → Task 7 (`RenewLease`). ✓
- Injection safety → Task 4 (`generateUsername`/`identRe`), Task 5 (`interpolate` re-validates). ✓
- API `/v1/dynamic` + RBAC `DynamicManage`/`DynamicIssue` → Task 9, Task 10. ✓
- CLI `janus dynamic` → Task 12. ✓
- Audit (never password), audit-write-failure fails mutation → Task 7 (`recordIssue`), Task 10 (handler `record` gates 500). ✓
- `JANUS_DYNAMIC_TICK` (default 60s, 0 disables) → Task 11. ✓
- Tests: units + testcontainers + crash-safety + leak → Tasks 3–8, 13. ✓
- Non-goals (no UI, no `run` wiring, Postgres-only) → respected. ✓

**Type consistency:** `RoleConfig`, `RoleInput`, `RoleView`, `LeaseView`, `Creds`, `DynamicRole`, `DynamicLease`, repo method names (`ClaimDue`, `Activate`, `MarkRevoked`, `MarkRevokeFailed`, `Renew`), and engine methods (`CreateRole`/`GetRole`/`ListRolesByConfig`/`UpdateRole`/`DeleteRole`/`IssueCreds`/`RenewLease`/`RevokeLease`/`GetLease`/`ListLeasesByRole`/`RunDue`/`RunScheduler`/`SweepOrphanedLeases`) are used identically across store, engine, handlers, and CLI. ✓

**Known adaptation points flagged for the implementer** (not placeholders — real "match existing pattern" notes): store test seed helpers (Task 3), fake/real keyring test harness (Task 6/8), exact `New(...)` arg order and route-group middleware (Task 10), and the existing leak-test capture mechanism (Task 13). Each names the file to mirror.
