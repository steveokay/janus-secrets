# Static Rotation Framework Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build Janus's static-rotation framework — scheduled rotation of an existing secret via a Postgres single-role reset or a generic HMAC-signed webhook, with crash-safe apply, an optional value-free notify webhook, REST + CLI + RBAC, all on branch `phase3-static-rotation`.

**Architecture:** A new `internal/rotation` engine (`Service`, mirroring `internal/transit`) owns rotation logic; a store repository (`internal/store/rotation.go`) persists `rotation_policies`; a background scheduler goroutine (started by `api.Boot`, tied to the server shutdown context) rotates due policies. Rotator config (Postgres admin DSN, webhook HMAC key) is envelope-encrypted exactly like secret values (per-blob DEK wrapped by the target project's KEK). The new value is committed by reusing `secrets.Service.SetSecrets`, which creates the immutable config version.

**Tech Stack:** Go (stdlib `crypto/hmac`, `crypto/rand`, `net/http`, `pgx/v5`), Postgres, `chi`, `cobra`, testcontainers.

**Spec:** `docs/superpowers/specs/2026-07-10-static-rotation-design.md`

**Conventions to follow (read these first if unsure):**
- Engine shape: `internal/transit/transit.go` (`Service` + `New`).
- Store repo shape: `internal/store/transit.go` (`withTx`, `execAffectingOne`, `mapError`, column consts, `id::uuid` casts, `id::text` selects). `store.NewID(ctx) (string, error)` mints ids.
- Envelope encryption: `internal/secrets/secrets.go` (`keyring.NewDEK(kek, aad)`, `crypto.Encrypt`, `crypto.UnwrapKey`, `crypto.Decrypt`, `crypto.ParseCiphertext`, `Ciphertext.Marshal()`); `unwrapProjectKEK` there is the model to copy.
- AAD helpers: `internal/crypto/keys.go` (`DEKAAD`, `ProjectKEKAAD`, `appendField`).
- Project-scoped authz in a handler: `internal/api/configs_handlers.go` (`resolveScopeResource(ctx,"config",cid)` → `s.authorize(...)` → do work → `s.record(...)`).
- Audit: `internal/api/audit.go`; the recorder is `s.audit.Record(ctx, audit.Event{Actor: audit.Actor{Kind,ID,Name}, Action, Resource, Detail, Result, ResultCode, IP})`.
- CLI: `cmd/janus/apiclient.go` (`c.call(method, path, in, out)`), any `cmd/janus/*_commands.go`.
- e2e harness: `internal/api/boot_test.go` (`bootPostgres`), `internal/api/auth_e2e_test.go` (`authStackFull`, `doAuthed`, `login`), `internal/api/backup_e2e_test.go` (`drillStack`).

**Gates for every commit:** `go build ./...`, `go vet ./...`, `go test ./...`. Full-suite gates before opening the PR: also `gosec -exclude-dir=internal/crypto/shamir ./...` and `govulncheck ./...`.

---

## File Structure

- `internal/crypto/keys.go` — **modify**: add `RotationConfigAAD` + `RotationPendingAAD`.
- `migrations/000010_rotation.up.sql` / `.down.sql` — **create**.
- `internal/store/rotation.go` — **create**: `RotationPolicy` model + `RotationRepo` (CRUD, `ClaimDue`, `SetPending`, `MarkRotated`, `MarkFailure`).
- `internal/store/backup.go` — **modify**: add `rotation_policies` to `backupTables`.
- `internal/rotation/rotation.go` — **create**: `Service`, `New`, config/pending seal+open, `rotatorConfig` type.
- `internal/rotation/errors.go` — **create**: sentinels.
- `internal/rotation/generate.go` — **create**: random value generator.
- `internal/rotation/webhook_rotator.go` — **create**: generic webhook rotator + HMAC signing.
- `internal/rotation/postgres_rotator.go` — **create**: `ALTER ROLE` rotator.
- `internal/rotation/notify.go` — **create**: post-rotation value-free notify.
- `internal/rotation/execute.go` — **create**: crash-safe `rotate`/`attempt`, `backoff`.
- `internal/rotation/scheduler.go` — **create**: `RunScheduler`, `RunDue`.
- `internal/api/rotation_handlers.go` — **create**: REST handlers + view/request types.
- `internal/api/server.go` — **modify**: `rotation` field, `New` param, route group.
- `internal/api/boot.go` — **modify**: wire `rotation.New`, `BootConfig.RotationTick`, start scheduler.
- `internal/authz/actions.go` — **modify**: add `RotationManage`, include in `adminActions`.
- `internal/api/errors.go` — **modify**: add `CodeRotationNotFound`.
- `cmd/janus/rotation_commands.go` — **create**: `janus rotation …`.
- `cmd/janus/root.go` (or wherever commands register) — **modify**: register rotation cmd.
- `cmd/janus/server.go` — **modify**: parse `JANUS_ROTATION_TICK` into `BootConfig.RotationTick`.
- `docs/ops/rotation.md` — **create**: runbook.
- `docs/operations.md` — **modify**: CLI + env-var rows.

---

## Task 1: Crypto AAD helpers

**Files:**
- Modify: `internal/crypto/keys.go`
- Test: `internal/crypto/keys_test.go` (append; if absent, create)

- [ ] **Step 1: Write the failing test**

Append to `internal/crypto/keys_test.go`:

```go
func TestRotationAADs(t *testing.T) {
	// Config vs pending for the same policy must differ (distinct domains).
	if bytes.Equal(RotationConfigAAD("p1"), RotationPendingAAD("p1")) {
		t.Fatal("config and pending AAD must differ for the same policy")
	}
	// Different policies must differ (binding).
	if bytes.Equal(RotationConfigAAD("p1"), RotationConfigAAD("p2")) {
		t.Fatal("config AAD must bind to policy id")
	}
	// Injective over the id (length-prefix guard, mirrors DEKAAD design).
	if bytes.Equal(RotationConfigAAD("ab"), RotationConfigAAD("a\x00b")) {
		t.Fatal("AAD must be injective over policy id")
	}
}
```

Ensure `keys_test.go` imports `"bytes"`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/crypto/ -run TestRotationAADs`
Expected: FAIL — `RotationConfigAAD` undefined.

- [ ] **Step 3: Implement**

Append to `internal/crypto/keys.go` (after `OIDCClientSecretAAD`):

```go
// RotationConfigAAD binds a rotation policy's encrypted rotator-config blob
// (admin DSN, webhook HMAC key) to its policy. A blob copied onto another
// policy's row fails to decrypt. Mirrors DEKAAD's length-prefixed encoding.
func RotationConfigAAD(policyID string) []byte {
	return appendField([]byte("janus:rotation:config"), policyID)
}

// RotationPendingAAD binds a rotation policy's encrypted pending value (the
// generated-but-not-yet-committed new secret value) to its policy, in a domain
// distinct from RotationConfigAAD so the two ciphertext slots can never be
// swapped.
func RotationPendingAAD(policyID string) []byte {
	return appendField([]byte("janus:rotation:pending"), policyID)
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/crypto/ -run TestRotationAADs`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/crypto/keys.go internal/crypto/keys_test.go
git commit -m "feat(crypto): rotation config + pending AAD helpers"
```

---

## Task 2: Migration 000010 (rotation_policies)

**Files:**
- Create: `migrations/000010_rotation.up.sql`, `migrations/000010_rotation.down.sql`
- Test: existing `internal/store/migrate_test.go` exercises up-migration on boot; add a targeted assertion.

- [ ] **Step 1: Write the up migration**

`migrations/000010_rotation.up.sql`:

```sql
CREATE TABLE rotation_policies (
  id                     uuid PRIMARY KEY,
  project_id             uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  config_id              uuid NOT NULL REFERENCES configs(id)  ON DELETE CASCADE,
  secret_key             text NOT NULL,
  type                   text NOT NULL CHECK (type IN ('postgres','webhook')),
  interval_seconds       bigint NOT NULL CHECK (interval_seconds > 0),
  next_rotation_at       timestamptz NOT NULL,
  status                 text NOT NULL DEFAULT 'active' CHECK (status IN ('active','failed','paused')),
  failure_count          int  NOT NULL DEFAULT 0,
  last_error             text,
  last_rotated_at        timestamptz,
  last_config_version    int,
  config_ct              bytea NOT NULL,
  config_nonce           bytea NOT NULL,
  config_wrapped_dek     bytea NOT NULL,
  config_dek_kek_version int   NOT NULL,
  pending_ct             bytea,
  pending_nonce          bytea,
  pending_wrapped_dek    bytea,
  pending_state          text CHECK (pending_state IN ('applying')),
  created_by             text NOT NULL,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  UNIQUE (config_id, secret_key)
);

-- Scheduler due-scan: partial index over active policies by due time.
CREATE INDEX rotation_policies_due ON rotation_policies (next_rotation_at)
  WHERE status = 'active';
```

`migrations/000010_rotation.down.sql`:

```sql
DROP TABLE rotation_policies;
```

- [ ] **Step 2: Add a migration assertion**

In `internal/store/migrate_test.go` add (mirror the existing table-existence style in that file; if it asserts a table for a prior migration, copy that shape):

```go
func TestMigration010CreatesRotationPolicies(t *testing.T) {
	dsn := bootPostgresStore(t) // use the file's existing test-DB helper name
	st, err := Open(context.Background(), dsn)
	if err != nil { t.Fatal(err) }
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil { t.Fatal(err) }
	var exists bool
	err = st.Pool().QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		 WHERE table_schema='public' AND table_name='rotation_policies')`).Scan(&exists)
	if err != nil { t.Fatal(err) }
	if !exists { t.Fatal("rotation_policies table missing after migrate") }
}
```

> **Note:** Match the store package's existing test-DB bootstrap helper and pool accessor (grep `migrate_test.go` / `store_test.go` for how they obtain a DSN and reach the pool — e.g. `st.pool` inside-package). Adjust the snippet to the real helper names; the assertion logic stays.

- [ ] **Step 3: Run**

Run: `go test ./internal/store/ -run TestMigration010CreatesRotationPolicies`
Expected: PASS (migration applies, table present).

- [ ] **Step 4: Commit**

```bash
git add migrations/000010_rotation.up.sql migrations/000010_rotation.down.sql internal/store/migrate_test.go
git commit -m "feat(store): migration 000010 rotation_policies"
```

---

## Task 3: Store repository + backup inclusion

**Files:**
- Create: `internal/store/rotation.go`
- Modify: `internal/store/backup.go`
- Test: `internal/store/rotation_test.go`

- [ ] **Step 1: Define the model and repo skeleton**

`internal/store/rotation.go`:

```go
package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// RotationPolicy is one rotation binding: a rotator over a single secret key.
// The *_ct/nonce/wrapped_dek fields hold the envelope-encrypted rotator config
// blob; pending_* holds an in-flight generated value awaiting commit.
type RotationPolicy struct {
	ID                  string
	ProjectID           string
	ConfigID            string
	SecretKey           string
	Type                string // "postgres" | "webhook"
	IntervalSeconds     int64
	NextRotationAt      time.Time
	Status              string // "active" | "failed" | "paused"
	FailureCount        int
	LastError           *string
	LastRotatedAt       *time.Time
	LastConfigVersion   *int
	ConfigCT            []byte
	ConfigNonce         []byte
	ConfigWrappedDEK    []byte
	ConfigDEKKEKVersion int
	PendingCT           []byte
	PendingNonce        []byte
	PendingWrappedDEK   []byte
	PendingState        *string // nil or "applying"
	CreatedBy           string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// RotationRepo persists rotation policies (crypto-blind).
type RotationRepo struct{ s *Store }

func NewRotationRepo(s *Store) *RotationRepo { return &RotationRepo{s: s} }

const rotationCols = `id::text, project_id::text, config_id::text, secret_key, type,
	interval_seconds, next_rotation_at, status, failure_count, last_error,
	last_rotated_at, last_config_version, config_ct, config_nonce, config_wrapped_dek,
	config_dek_kek_version, pending_ct, pending_nonce, pending_wrapped_dek, pending_state,
	created_by, created_at, updated_at`

func scanPolicy(row interface{ Scan(...any) error }) (*RotationPolicy, error) {
	var p RotationPolicy
	if err := row.Scan(&p.ID, &p.ProjectID, &p.ConfigID, &p.SecretKey, &p.Type,
		&p.IntervalSeconds, &p.NextRotationAt, &p.Status, &p.FailureCount, &p.LastError,
		&p.LastRotatedAt, &p.LastConfigVersion, &p.ConfigCT, &p.ConfigNonce, &p.ConfigWrappedDEK,
		&p.ConfigDEKKEKVersion, &p.PendingCT, &p.PendingNonce, &p.PendingWrappedDEK, &p.PendingState,
		&p.CreatedBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, mapError(err)
	}
	return &p, nil
}
```

- [ ] **Step 2: Implement CRUD + scheduler ops**

Append to `internal/store/rotation.go`:

```go
// Create inserts a policy. Duplicate (config_id, secret_key) → ErrAlreadyExists.
func (r *RotationRepo) Create(ctx context.Context, p *RotationPolicy) (*RotationPolicy, error) {
	_, err := r.s.pool.Exec(ctx,
		`INSERT INTO rotation_policies
		 (id, project_id, config_id, secret_key, type, interval_seconds, next_rotation_at,
		  config_ct, config_nonce, config_wrapped_dek, config_dek_kek_version, created_by)
		 VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		p.ID, p.ProjectID, p.ConfigID, p.SecretKey, p.Type, p.IntervalSeconds, p.NextRotationAt,
		p.ConfigCT, p.ConfigNonce, p.ConfigWrappedDEK, p.ConfigDEKKEKVersion, p.CreatedBy)
	if err != nil {
		return nil, mapError(err)
	}
	return r.Get(ctx, p.ID)
}

func (r *RotationRepo) Get(ctx context.Context, id string) (*RotationPolicy, error) {
	return scanPolicy(r.s.pool.QueryRow(ctx,
		`SELECT `+rotationCols+` FROM rotation_policies WHERE id = $1::uuid`, id))
}

// GetByConfigKey resolves the unique policy on (config_id, secret_key).
func (r *RotationRepo) GetByConfigKey(ctx context.Context, configID, key string) (*RotationPolicy, error) {
	return scanPolicy(r.s.pool.QueryRow(ctx,
		`SELECT `+rotationCols+` FROM rotation_policies WHERE config_id = $1::uuid AND secret_key = $2`,
		configID, key))
}

// ListByProject returns policies for a project, newest first.
func (r *RotationRepo) ListByProject(ctx context.Context, projectID string) ([]*RotationPolicy, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+rotationCols+` FROM rotation_policies WHERE project_id = $1::uuid ORDER BY created_at DESC, id`,
		projectID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*RotationPolicy
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, mapError(rows.Err())
}

// Update sets interval/status and (optionally) a new encrypted config blob.
// nil config* leaves the blob unchanged.
func (r *RotationRepo) Update(ctx context.Context, id string, intervalSeconds *int64, status *string,
	configCT, configNonce, configWrappedDEK []byte, configKEKVer *int) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE rotation_policies SET
		   interval_seconds       = COALESCE($2, interval_seconds),
		   status                 = COALESCE($3, status),
		   config_ct              = COALESCE($4, config_ct),
		   config_nonce           = COALESCE($5, config_nonce),
		   config_wrapped_dek     = COALESCE($6, config_wrapped_dek),
		   config_dek_kek_version = COALESCE($7, config_dek_kek_version),
		   updated_at             = now()
		 WHERE id = $1::uuid`,
		id, intervalSeconds, status, configCT, configNonce, configWrappedDEK, configKEKVer)
}

func (r *RotationRepo) Delete(ctx context.Context, id string) error {
	return r.s.execAffectingOne(ctx, `DELETE FROM rotation_policies WHERE id = $1::uuid`, id)
}

// ClaimDue returns active policies that are due or have an in-flight pending
// value (crash recovery), oldest-due first, up to limit.
//
// Single-node deployments run exactly one scheduler goroutine, so a plain
// SELECT is race-free; we deliberately do NOT hold FOR UPDATE row locks here
// because rotation performs network I/O (ALTER ROLE / webhook) that must not
// run inside a long-lived transaction. A future multi-node design would add a
// claimed_at column + FOR UPDATE SKIP LOCKED.
func (r *RotationRepo) ClaimDue(ctx context.Context, now time.Time, limit int) ([]*RotationPolicy, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+rotationCols+` FROM rotation_policies
		 WHERE status = 'active' AND (next_rotation_at <= $1 OR pending_state IS NOT NULL)
		 ORDER BY next_rotation_at ASC LIMIT $2`, now, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*RotationPolicy
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, mapError(rows.Err())
}

// SetPending stores an in-flight encrypted value and marks the policy applying.
func (r *RotationRepo) SetPending(ctx context.Context, id string, ct, nonce, wrappedDEK []byte) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE rotation_policies SET pending_ct=$2, pending_nonce=$3, pending_wrapped_dek=$4,
		   pending_state='applying', updated_at=now() WHERE id=$1::uuid`,
		id, ct, nonce, wrappedDEK)
}

// MarkRotated records a successful rotation: clears pending, resets failure
// state, advances next_rotation_at, and stores the produced config version.
func (r *RotationRepo) MarkRotated(ctx context.Context, id string, configVersion int, next time.Time) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE rotation_policies SET
		   pending_ct=NULL, pending_nonce=NULL, pending_wrapped_dek=NULL, pending_state=NULL,
		   failure_count=0, status='active', last_error=NULL,
		   last_rotated_at=now(), last_config_version=$2, next_rotation_at=$3, updated_at=now()
		 WHERE id=$1::uuid`, id, configVersion, next)
}

// MarkFailure records a failed attempt: bumps failure_count, stores a sanitized
// error, sets the backoff retry time, and flips to 'failed' at the threshold.
func (r *RotationRepo) MarkFailure(ctx context.Context, id, sanitizedErr string, next time.Time, threshold int) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE rotation_policies SET
		   failure_count = failure_count + 1,
		   last_error    = $2,
		   next_rotation_at = $3,
		   status = CASE WHEN failure_count + 1 >= $4 THEN 'failed' ELSE status END,
		   updated_at = now()
		 WHERE id=$1::uuid`, id, sanitizedErr, next, threshold)
}
```

- [ ] **Step 3: Include rotation_policies in backups**

In `internal/store/backup.go`, add to the `backupTables` slice, immediately AFTER the `{"config_version_entries", ...}` entry and before `{"service_tokens", ...}` (it references `projects` and `configs`, both already earlier in the list):

```go
	{"rotation_policies", "created_at, id"},
```

- [ ] **Step 4: Write the store test**

`internal/store/rotation_test.go` — table/scenario test covering: Create→Get round-trip (all fields), GetByConfigKey, duplicate `(config_id, secret_key)` → `ErrAlreadyExists`, ListByProject ordering, Update (interval + status, and a nil-config no-op leaves blob intact), SetPending then Get shows `PendingState=="applying"`, MarkRotated clears pending + sets version/next, MarkFailure increments and flips to `failed` at threshold, Delete then Get → `ErrNotFound`.

Mirror `internal/store/transit_test.go` for boot/seed helpers (creating a project + config to satisfy FKs — reuse the same project/config seed helpers those tests use).

- [ ] **Step 5: Run**

Run: `go test ./internal/store/ -run 'Rotation|Migration010'`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/store/rotation.go internal/store/rotation_test.go internal/store/backup.go
git commit -m "feat(store): rotation_policies repo + backup inclusion"
```

---

## Task 4: Rotation engine skeleton (Service, envelope seal/open)

**Files:**
- Create: `internal/rotation/rotation.go`, `internal/rotation/errors.go`
- Test: `internal/rotation/rotation_test.go`

- [ ] **Step 1: Errors**

`internal/rotation/errors.go`:

```go
package rotation

import "errors"

var (
	ErrNotFound       = errors.New("rotation: policy not found")
	ErrExists         = errors.New("rotation: policy already exists for this config/key")
	ErrSealed         = errors.New("rotation: server is sealed")
	ErrInvalidType    = errors.New("rotation: unknown rotator type")
	ErrInvalidConfig  = errors.New("rotation: invalid rotator config")
	ErrApplyFailed    = errors.New("rotation: rotator apply failed")
)
```

- [ ] **Step 2: Service + config types + seal/open**

`internal/rotation/rotation.go`:

```go
// Package rotation is Janus's static-rotation engine: scheduled rotation of an
// existing secret's value via a Postgres single-role reset or a generic
// HMAC-signed webhook, with crash-safe apply and optional notify webhooks.
package rotation

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

const (
	TypePostgres = "postgres"
	TypeWebhook  = "webhook"

	failureThreshold = 5                // consecutive failures → status='failed'
	defaultBatch     = 50               // policies claimed per tick
	defaultPasswdLen = 32               // generated value length
)

// rotatorConfig is the decrypted rotator-config blob (never logged/persisted in clear).
type rotatorConfig struct {
	// postgres
	AdminDSN    string `json:"admin_dsn,omitempty"`
	Role        string `json:"role,omitempty"`
	PasswordLen int    `json:"password_len,omitempty"`
	// webhook
	URL     string `json:"url,omitempty"`
	HMACKey string `json:"hmac_key,omitempty"`
	// optional notify (either type)
	NotifyURL     string `json:"notify_url,omitempty"`
	NotifyHMACKey string `json:"notify_hmac_key,omitempty"`
}

// keyring is the subset of *crypto.Keyring the engine needs (fakeable in tests).
type keyring interface {
	UnwrapProjectKEK(ct crypto.Ciphertext, projectID string) ([]byte, error)
	NewDEK(projectKEK, aad []byte) ([]byte, crypto.Ciphertext, error)
	Sealed() bool
}

// Service is the rotation engine.
type Service struct {
	kr       keyring
	repo     *store.RotationRepo
	projects *store.ProjectRepo
	secrets  *secrets.Service
	audit    *audit.Recorder
	logger   *slog.Logger
	st       *store.Store
	hc       *http.Client
	now      func() time.Time // injectable clock (tests)
}

// New wires the engine.
func New(kr *crypto.Keyring, st *store.Store, sec *secrets.Service, aud *audit.Recorder, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		kr: kr, repo: store.NewRotationRepo(st), projects: store.NewProjectRepo(st),
		secrets: sec, audit: aud, logger: logger, st: st,
		hc:  &http.Client{Timeout: 15 * time.Second},
		now: time.Now,
	}
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// sealConfig encrypts a rotatorConfig blob under proj's KEK, bound to policyID.
func (s *Service) sealConfig(proj *store.Project, policyID string, cfg rotatorConfig) (ct, nonce, wrapped []byte, kekVer int, err error) {
	return s.sealBlob(proj, crypto.RotationConfigAAD(policyID), mustJSON(cfg))
}

// openConfig decrypts the stored rotatorConfig blob.
func (s *Service) openConfig(proj *store.Project, p *store.RotationPolicy) (rotatorConfig, error) {
	pt, err := s.openBlob(proj, crypto.RotationConfigAAD(p.ID), p.ConfigWrappedDEK, p.ConfigNonce, p.ConfigCT)
	if err != nil {
		return rotatorConfig{}, err
	}
	defer zero(pt)
	var cfg rotatorConfig
	if err := json.Unmarshal(pt, &cfg); err != nil {
		return rotatorConfig{}, ErrInvalidConfig
	}
	return cfg, nil
}

// sealPending / openPending store the in-flight generated value (distinct AAD).
func (s *Service) sealPending(proj *store.Project, policyID, value string) (ct, nonce, wrapped []byte, err error) {
	c, n, w, _, e := s.sealBlob(proj, crypto.RotationPendingAAD(policyID), []byte(value))
	return c, n, w, e
}

func (s *Service) openPending(proj *store.Project, p *store.RotationPolicy) (string, error) {
	pt, err := s.openBlob(proj, crypto.RotationPendingAAD(p.ID), p.PendingWrappedDEK, p.PendingNonce, p.PendingCT)
	if err != nil {
		return "", err
	}
	defer zero(pt)
	return string(pt), nil
}

// sealBlob mints a DEK under proj's KEK and GCM-encrypts plaintext with aad.
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
	return c.Data, c.Nonce, wrappedDEK.Marshal(), proj.KEKVersion, nil
}

// openBlob unwraps the DEK and GCM-decrypts the stored ciphertext.
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

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

// mapStoreErr translates store sentinels to rotation sentinels.
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
```

- [ ] **Step 3: Write the round-trip test**

`internal/rotation/rotation_test.go` — an e2e-style store test (needs a real Postgres + real keyring + seeded project). Mirror `internal/store/transit_test.go`/`internal/secrets` tests for: boot store, init+unseal keyring, create a project (so `proj.WrappedKEK` is real), config, and a secret key.

Test `TestConfigBlobRoundTrip`: seal a `rotatorConfig{AdminDSN:"postgres://…", Role:"app", PasswordLen:24}`, persist via `repo.Create`, `Get`, then `openConfig` returns the same struct. Then tamper: call `openBlob` with `RotationPendingAAD(id)` over the config ciphertext and assert it fails (AAD-domain separation).

- [ ] **Step 4: Run**

Run: `go test ./internal/rotation/ -run TestConfigBlobRoundTrip`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/rotation/rotation.go internal/rotation/errors.go internal/rotation/rotation_test.go
git commit -m "feat(rotation): engine skeleton + envelope-encrypted rotator config"
```

---

## Task 5: Value generator + webhook rotator + notify

**Files:**
- Create: `internal/rotation/generate.go`, `internal/rotation/webhook_rotator.go`, `internal/rotation/notify.go`
- Test: `internal/rotation/webhook_rotator_test.go`

- [ ] **Step 1: Value generator (failing test first)**

`internal/rotation/generate_test.go`:

```go
package rotation

import "testing"

func TestGeneratePassword(t *testing.T) {
	got, err := generatePassword(32)
	if err != nil { t.Fatal(err) }
	if len(got) != 32 { t.Fatalf("len = %d, want 32", len(got)) }
	for _, c := range got {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			t.Fatalf("unsafe char %q in generated password", c)
		}
	}
	other, _ := generatePassword(32)
	if got == other { t.Fatal("two generations collided") }
	if _, err := generatePassword(0); err == nil { t.Fatal("want error for non-positive length") }
}
```

`internal/rotation/generate.go`:

```go
package rotation

import (
	"crypto/rand"
	"errors"
)

// alphabet is intentionally alphanumeric only: the generated value is
// interpolated into an ALTER ROLE ... PASSWORD literal, and excluding quotes /
// backslashes removes any SQL-literal escaping hazard at the source (defensive
// quoting is still applied in the postgres rotator).
const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// generatePassword returns n cryptographically-random alphanumeric characters.
func generatePassword(n int) (string, error) {
	if n <= 0 {
		return "", errors.New("rotation: password length must be positive")
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return string(buf), nil
}
```

> Note: modulo over a 62-char alphabet gives a slight bias; acceptable here — the value is high-entropy (n≥16) and secret. Do not "fix" with rejection sampling unless a reviewer insists; keep it simple.

Run: `go test ./internal/rotation/ -run TestGeneratePassword` → PASS.

- [ ] **Step 2: HMAC signing + webhook rotator (failing test first)**

`internal/rotation/webhook_rotator_test.go`:

```go
package rotation

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebhookRotatorSignsAndCommitsOn2xx(t *testing.T) {
	const key = "shhh"
	var gotSig, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotSig = r.Header.Get("X-Janus-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rot := webhookRotator{hc: srv.Client()}
	cfg := rotatorConfig{URL: srv.URL, HMACKey: key}
	err := rot.apply(context.Background(), cfg, "pol1", "secretKey", "newval123")
	if err != nil { t.Fatalf("apply: %v", err) }

	// signature verifies over the exact body
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(gotBody))
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if gotSig != want { t.Fatalf("sig = %q, want %q", gotSig, want) }
	if !strings.Contains(gotBody, `"new_value":"newval123"`) { t.Fatalf("body missing value: %s", gotBody) }
	var payload map[string]any
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil { t.Fatal(err) }
}

func TestWebhookRotatorFailsOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	rot := webhookRotator{hc: srv.Client()}
	err := rot.apply(context.Background(), rotatorConfig{URL: srv.URL, HMACKey: "k"}, "p", "K", "v")
	if err == nil { t.Fatal("want error on 500") }
	if strings.Contains(err.Error(), "v") && strings.Contains(err.Error(), "secret") {
		t.Fatal("error must not leak the value")
	}
}
```

`internal/rotation/webhook_rotator.go`:

```go
package rotation

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
)

// hmacHex returns "sha256=<hex>" of body under key.
func hmacHex(key string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// signedPost POSTs body to url with an X-Janus-Signature HMAC header and
// returns an error unless the response is 2xx. The error carries only the
// status code — never the body or the signed payload.
func signedPost(ctx context.Context, hc *http.Client, url, hmacKey string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Janus-Signature", hmacHex(hmacKey, body))
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: request error", ErrApplyFailed)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: endpoint returned status %d", ErrApplyFailed, resp.StatusCode)
	}
	return nil
}

// webhookRotator pushes the new value to a configured endpoint.
type webhookRotator struct{ hc *http.Client }

func (wr webhookRotator) apply(ctx context.Context, cfg rotatorConfig, policyID, secretKey, newValue string) error {
	if cfg.URL == "" {
		return ErrInvalidConfig
	}
	body, _ := json.Marshal(map[string]any{
		"policy_id":  policyID,
		"secret_key": secretKey,
		"new_value":  newValue,
	})
	return signedPost(ctx, wr.hc, cfg.URL, cfg.HMACKey, body)
}
```

- [ ] **Step 3: Notify webhook**

`internal/rotation/notify.go`:

```go
package rotation

import (
	"context"
	"encoding/json"
)

// notify fires a best-effort, value-free post-rotation event. A failure is
// logged and swallowed — the rotation already committed.
func (s *Service) notify(ctx context.Context, cfg rotatorConfig, p *store.RotationPolicy, newVersion int) {
	if cfg.NotifyURL == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{
		"policy_id":   p.ID,
		"project_id":  p.ProjectID,
		"config_id":   p.ConfigID,
		"secret_key":  p.SecretKey,
		"new_version": newVersion,
	})
	if err := signedPost(ctx, s.hc, cfg.NotifyURL, cfg.NotifyHMACKey, body); err != nil {
		s.logger.Warn("rotation notify webhook failed", "policy", p.ID, "err", err)
	}
}
```

Add the `store` import to `notify.go`.

- [ ] **Step 4: Run**

Run: `go test ./internal/rotation/ -run 'Webhook|GeneratePassword'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/rotation/generate.go internal/rotation/generate_test.go internal/rotation/webhook_rotator.go internal/rotation/webhook_rotator_test.go internal/rotation/notify.go
git commit -m "feat(rotation): value generator, HMAC-signed webhook rotator, notify hook"
```

---

## Task 6: Postgres rotator

**Files:**
- Create: `internal/rotation/postgres_rotator.go`
- Test: `internal/rotation/postgres_rotator_test.go`

- [ ] **Step 1: Failing e2e test (real Postgres via testcontainers)**

`internal/rotation/postgres_rotator_test.go`:

```go
package rotation

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestPostgresRotatorAltersRole(t *testing.T) {
	adminDSN := bootPostgresDSN(t) // superuser DSN from the test container (see harness note)
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil { t.Fatal(err) }
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, `CREATE ROLE app_rot LOGIN PASSWORD 'old_pw'`); err != nil {
		t.Fatal(err)
	}

	rot := postgresRotator{}
	cfg := rotatorConfig{AdminDSN: adminDSN, Role: "app_rot"}
	if err := rot.apply(ctx, cfg, "pol", "DB_PASSWORD", "brandNewPW123"); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// new password connects
	newDSN := replaceUserPass(adminDSN, "app_rot", "brandNewPW123")
	c, err := pgx.Connect(ctx, newDSN)
	if err != nil { t.Fatalf("new password should connect: %v", err) }
	c.Close(ctx)

	// idempotent: applying the same value again still succeeds
	if err := rot.apply(ctx, cfg, "pol", "DB_PASSWORD", "brandNewPW123"); err != nil {
		t.Fatalf("re-apply not idempotent: %v", err)
	}

	// bad role name is rejected before touching the DB
	if err := rot.apply(ctx, rotatorConfig{AdminDSN: adminDSN, Role: "bad; DROP"}, "p", "K", "v"); err == nil {
		t.Fatal("want rejection of invalid role identifier")
	}
	_ = fmt.Sprint // keep import if unused after edits
	_ = strings.TrimSpace
}
```

> **Harness note:** add a small helper in this package's test file that starts a Postgres testcontainer and returns a superuser DSN plus a `replaceUserPass(dsn, user, pass)` string helper. Mirror `internal/api/boot_test.go` `bootPostgres` (which already returns a DSN) — you can reuse the same container-start code; the returned DSN's user is a superuser and can `CREATE ROLE` / `ALTER ROLE`.

- [ ] **Step 2: Implement**

`internal/rotation/postgres_rotator.go`:

```go
package rotation

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

// roleRe restricts rotatable role names to plain SQL identifiers. Combined with
// Identifier.Sanitize below it removes any injection surface from the role.
var roleRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)

// quoteLiteral renders s as a Postgres string literal, doubling single quotes.
// The generated value is alphanumeric (no quotes), so this is defensive.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// postgresRotator resets a single role's password via ALTER ROLE.
type postgresRotator struct{}

func (postgresRotator) apply(ctx context.Context, cfg rotatorConfig, policyID, secretKey, newValue string) error {
	if cfg.AdminDSN == "" || !roleRe.MatchString(cfg.Role) {
		return ErrInvalidConfig
	}
	conn, err := pgx.Connect(ctx, cfg.AdminDSN)
	if err != nil {
		// never surface the DSN; pgx connect errors can include host/port.
		return fmt.Errorf("%w: admin connect failed", ErrApplyFailed)
	}
	defer conn.Close(ctx)

	// ALTER ROLE cannot bind the role identifier or password as parameters;
	// both are rendered safely (Sanitize double-quotes the identifier;
	// quoteLiteral escapes the literal). Value is alphanumeric by construction.
	stmt := fmt.Sprintf("ALTER ROLE %s WITH PASSWORD %s",
		pgx.Identifier{cfg.Role}.Sanitize(), quoteLiteral(newValue))
	if _, err := conn.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("%w: alter role failed", ErrApplyFailed)
	}
	return nil
}
```

- [ ] **Step 3: Run**

Run: `go test ./internal/rotation/ -run TestPostgresRotatorAltersRole`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/rotation/postgres_rotator.go internal/rotation/postgres_rotator_test.go
git commit -m "feat(rotation): postgres single-role ALTER ROLE rotator"
```

---

## Task 7: Crash-safe execution + backoff

**Files:**
- Create: `internal/rotation/execute.go`
- Test: `internal/rotation/execute_test.go`, `internal/rotation/backoff_test.go`

- [ ] **Step 1: Backoff (failing test first)**

`internal/rotation/backoff_test.go`:

```go
package rotation

import (
	"testing"
	"time"
)

func TestBackoff(t *testing.T) {
	// grows from 1m, doubling, capped at 1h; never below base.
	cases := []struct{ n int; want time.Duration }{
		{1, 1 * time.Minute},
		{2, 2 * time.Minute},
		{3, 4 * time.Minute},
		{7, 64 * time.Minute}, // would be 64m → capped
		{20, 1 * time.Hour},   // capped
	}
	for _, c := range cases {
		if got := backoff(c.n); got != capDur(c.want) {
			t.Errorf("backoff(%d) = %v, want %v", c.n, got, capDur(c.want))
		}
	}
}

func capDur(d time.Duration) time.Duration {
	if d > time.Hour { return time.Hour }
	return d
}
```

`internal/rotation/execute.go` (backoff portion):

```go
package rotation

import (
	"context"
	"time"

	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

const (
	backoffBase = 1 * time.Minute
	backoffCap  = 1 * time.Hour
)

// backoff returns the retry delay after failureCount consecutive failures:
// base*2^(n-1), capped. n is 1-based (first failure → base).
func backoff(failureCount int) time.Duration {
	d := backoffBase
	for i := 1; i < failureCount; i++ {
		d *= 2
		if d >= backoffCap {
			return backoffCap
		}
	}
	if d > backoffCap {
		return backoffCap
	}
	return d
}
```

Run: `go test ./internal/rotation/ -run TestBackoff` → PASS.

- [ ] **Step 2: The rotator dispatch + crash-safe rotate + attempt**

Append to `internal/rotation/execute.go`:

```go
// rotatorApplier is the per-type apply contract.
type rotatorApplier interface {
	apply(ctx context.Context, cfg rotatorConfig, policyID, secretKey, newValue string) error
}

func (s *Service) rotatorFor(typ string) (rotatorApplier, error) {
	switch typ {
	case TypePostgres:
		return postgresRotator{}, nil
	case TypeWebhook:
		return webhookRotator{hc: s.hc}, nil
	default:
		return nil, ErrInvalidType
	}
}

// rotate performs one crash-safe rotation of p: persist-pending → idempotent
// apply → commit (write new config version, clear pending) → notify. A pending
// value from a prior crash/attempt is reused so the external apply is idempotent.
func (s *Service) rotate(ctx context.Context, p *store.RotationPolicy) error {
	proj, err := s.projects.Get(ctx, p.ProjectID)
	if err != nil {
		return mapStoreErr(err)
	}
	cfg, err := s.openConfig(proj, p) // fails with ErrSealed while sealed
	if err != nil {
		return err
	}

	// Reuse an in-flight value (crash/retry recovery) or generate a new one.
	var newValue string
	if p.PendingState != nil {
		if newValue, err = s.openPending(proj, p); err != nil {
			return err
		}
	} else {
		n := cfg.PasswordLen
		if n <= 0 {
			n = defaultPasswdLen
		}
		if newValue, err = generatePassword(n); err != nil {
			return err
		}
		ct, nonce, wrapped, err := s.sealPending(proj, p.ID, newValue)
		if err != nil {
			return err
		}
		if err := s.repo.SetPending(ctx, p.ID, ct, nonce, wrapped); err != nil {
			return mapStoreErr(err)
		}
	}

	// Apply to the target system (idempotent for the same newValue).
	rot, err := s.rotatorFor(p.Type)
	if err != nil {
		return err
	}
	if err := rot.apply(ctx, cfg, p.ID, p.SecretKey, newValue); err != nil {
		return err
	}

	// Commit: write the new value as a config version, then clear pending.
	actor := "rotation:" + p.ID
	cv, err := s.secrets.SetSecrets(ctx, p.ConfigID,
		[]secrets.SecretChange{{Key: p.SecretKey, Value: []byte(newValue)}}, actor, actor)
	if err != nil {
		return err
	}
	next := s.now().Add(time.Duration(p.IntervalSeconds) * time.Second)
	if err := s.repo.MarkRotated(ctx, p.ID, cv.Version, next); err != nil {
		return mapStoreErr(err)
	}
	s.notify(ctx, cfg, p, cv.Version)
	return nil
}

// attempt runs rotate and records the audit event + failure bookkeeping. It is
// the single entry point for both the scheduler and manual rotate-now.
func (s *Service) attempt(ctx context.Context, p *store.RotationPolicy) error {
	err := s.rotate(ctx, p)
	if err != nil {
		next := s.now().Add(backoff(p.FailureCount + 1))
		if merr := s.repo.MarkFailure(ctx, p.ID, sanitize(err), next, failureThreshold); merr != nil {
			s.logger.Warn("rotation mark-failure failed", "policy", p.ID, "err", merr)
		}
		s.recordRotate(ctx, p, "failure", sanitize(err))
		return err
	}
	s.recordRotate(ctx, p, "success", "")
	return nil
}

// sanitize maps an apply/store error to a fixed, value-free category string
// safe to persist in last_error and audit detail.
func sanitize(err error) string {
	switch {
	case errorsIs(err, ErrSealed):
		return "sealed"
	case errorsIs(err, ErrApplyFailed):
		return "apply failed"
	case errorsIs(err, ErrInvalidConfig):
		return "invalid config"
	default:
		return "rotation error"
	}
}

// recordRotate writes a rotation.rotate audit event for a system actor. Detail
// is a value-free category on failure, empty on success.
func (s *Service) recordRotate(ctx context.Context, p *store.RotationPolicy, result, detail string) {
	if s.audit == nil {
		return
	}
	err := s.audit.Record(ctx, audit.Event{
		Actor:      audit.Actor{Kind: "system", Name: "rotation:" + p.ID},
		Action:     "rotation.rotate",
		Resource:   "configs/" + p.ConfigID + "/secrets/" + p.SecretKey,
		Detail:     detail,
		Result:     result,
		ResultCode: "",
	})
	if err != nil {
		s.logger.Warn("rotation audit write failed", "policy", p.ID, "err", err)
	}
}
```

Add an `errorsIs` helper or just import `errors` and use `errors.Is` directly (replace `errorsIs` with `errors.Is` and add the import). Verify `audit.Event` field names against `internal/audit/event.go` and `s.audit.Record`'s signature in `internal/audit/recorder.go`; adjust field names if they differ (e.g. IP may be required-empty).

- [ ] **Step 3: e2e test — full rotate + crash recovery**

`internal/rotation/execute_test.go` (real Postgres + real keyring + seeded project/config/secret; mirror Task 6 harness + Task 4 seed):

- `TestRotatePostgresEndToEnd`: create a role + a policy whose managed secret's current value is the old password; run `svc.attempt(ctx, policy)`; assert (a) a NEW config version exists for the key, (b) revealing it yields a value that connects to Postgres as the role, (c) `policy.Status=="active"`, `FailureCount==0`, `PendingState==nil`, `LastConfigVersion` set.
- `TestRotateResumesPending`: manually `SetPending` a known value + set `pending_state='applying'` on a webhook policy pointing at an httptest server that records the received value; run `attempt`; assert the server received the SAME pending value (reuse, not regenerate) and the committed config version holds it.
- `TestRotateFailureMarksBackoff`: webhook policy → httptest server returning 500; run `attempt`; assert it returns an error, `FailureCount==1`, `Status=="active"` (below threshold), `LastError=="apply failed"`, `NextRotationAt` advanced by ~1m, and a pending value is present (persisted before apply). Run 4 more times → `Status=="failed"` at the 5th.
- `TestRotateSealedNoop`: seal the keyring; `attempt` returns `ErrSealed` and writes nothing (no new version, no pending).

- [ ] **Step 4: Run**

Run: `go test ./internal/rotation/ -run 'Rotate|Backoff'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/rotation/execute.go internal/rotation/execute_test.go internal/rotation/backoff_test.go
git commit -m "feat(rotation): crash-safe rotate, backoff, failure state machine, audit"
```

---

## Task 8: Scheduler

**Files:**
- Create: `internal/rotation/scheduler.go`
- Test: `internal/rotation/scheduler_test.go`

- [ ] **Step 1: Implement**

`internal/rotation/scheduler.go`:

```go
package rotation

import (
	"context"
	"time"
)

// RunDue rotates every currently-due (or pending-recovery) policy once. It is
// a no-op while sealed. Per-policy errors are handled inside attempt (logged +
// backoff) and never abort the pass.
func (s *Service) RunDue(ctx context.Context) {
	if s.kr.Sealed() {
		return
	}
	policies, err := s.repo.ClaimDue(ctx, s.now(), defaultBatch)
	if err != nil {
		s.logger.Warn("rotation claim-due failed", "err", err)
		return
	}
	for _, p := range policies {
		if ctx.Err() != nil {
			return
		}
		_ = s.attempt(ctx, p)
	}
}

// RunScheduler ticks every `tick` and rotates due policies until ctx is done.
// tick <= 0 disables the scheduler (returns immediately). Ties to the server
// shutdown context so it stops cleanly on SIGTERM.
func (s *Service) RunScheduler(ctx context.Context, tick time.Duration) {
	if tick <= 0 {
		return
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	s.logger.Info("rotation scheduler started", "tick", tick.String())
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("rotation scheduler stopping")
			return
		case <-t.C:
			s.RunDue(ctx)
		}
	}
}
```

- [ ] **Step 2: Test**

`internal/rotation/scheduler_test.go`:
- `TestRunDueRotatesDuePolicies`: seed two webhook policies, one due (`next_rotation_at` in the past) and one not-yet-due (future); point both at an httptest server; call `svc.RunDue(ctx)`; assert only the due one committed a new version.
- `TestRunDueSealedNoop`: seal keyring, seed a due policy; `RunDue` does nothing.
- `TestRunDueRecoversPending`: seed a policy with `pending_state='applying'` but `next_rotation_at` far in the future; `RunDue` still picks it up (recovery clause) and commits.

- [ ] **Step 3: Run**

Run: `go test ./internal/rotation/ -run TestRunDue`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/rotation/scheduler.go internal/rotation/scheduler_test.go
git commit -m "feat(rotation): scheduler (RunDue/RunScheduler) with sealed-skip + recovery"
```

---

## Task 9: Engine CRUD methods (for the API layer)

**Files:**
- Modify: `internal/rotation/rotation.go` (append public CRUD)
- Test: `internal/rotation/crud_test.go`

- [ ] **Step 1: Define request/result types + CRUD**

Append to `internal/rotation/rotation.go`:

```go
// PolicyInput is the create/update payload (plaintext config; encrypted here).
type PolicyInput struct {
	ConfigID        string
	SecretKey       string
	Type            string
	IntervalSeconds int64
	Config          rotatorConfig
}

// PolicyView is the masked, safe-to-return projection (no secrets/DSN/keys).
type PolicyView struct {
	ID                string
	ProjectID         string
	ConfigID          string
	SecretKey         string
	Type              string
	IntervalSeconds   int64
	Status            string
	FailureCount      int
	LastError         *string
	NextRotationAt    time.Time
	LastRotatedAt     *time.Time
	LastConfigVersion *int
	CreatedAt         time.Time
}

func view(p *store.RotationPolicy) PolicyView {
	return PolicyView{
		ID: p.ID, ProjectID: p.ProjectID, ConfigID: p.ConfigID, SecretKey: p.SecretKey,
		Type: p.Type, IntervalSeconds: p.IntervalSeconds, Status: p.Status,
		FailureCount: p.FailureCount, LastError: p.LastError, NextRotationAt: p.NextRotationAt,
		LastRotatedAt: p.LastRotatedAt, LastConfigVersion: p.LastConfigVersion, CreatedAt: p.CreatedAt,
	}
}

// projectIDForConfig resolves the owning project of a config (for KEK + scope).
func (s *Service) projectIDForConfig(ctx context.Context, configID string) (*store.Project, error) {
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

// Create validates, encrypts the config blob, and inserts a policy.
func (s *Service) Create(ctx context.Context, in PolicyInput, createdBy string) (PolicyView, error) {
	if in.Type != TypePostgres && in.Type != TypeWebhook {
		return PolicyView{}, ErrInvalidType
	}
	if in.SecretKey == "" || in.IntervalSeconds <= 0 {
		return PolicyView{}, ErrInvalidConfig
	}
	if in.Type == TypePostgres && (in.Config.AdminDSN == "" || !roleRe.MatchString(in.Config.Role)) {
		return PolicyView{}, ErrInvalidConfig
	}
	if in.Type == TypeWebhook && in.Config.URL == "" {
		return PolicyView{}, ErrInvalidConfig
	}
	proj, err := s.projectIDForConfig(ctx, in.ConfigID)
	if err != nil {
		return PolicyView{}, err
	}
	id, err := s.st.NewID(ctx)
	if err != nil {
		return PolicyView{}, err
	}
	ct, nonce, wrapped, kekVer, err := s.sealConfig(proj, id, in.Config)
	if err != nil {
		return PolicyView{}, err
	}
	p := &store.RotationPolicy{
		ID: id, ProjectID: proj.ID, ConfigID: in.ConfigID, SecretKey: in.SecretKey,
		Type: in.Type, IntervalSeconds: in.IntervalSeconds,
		NextRotationAt:      s.now().Add(time.Duration(in.IntervalSeconds) * time.Second),
		ConfigCT:            ct,
		ConfigNonce:         nonce,
		ConfigWrappedDEK:    wrapped,
		ConfigDEKKEKVersion: kekVer,
		CreatedBy:           createdBy,
	}
	saved, err := s.repo.Create(ctx, p)
	if err != nil {
		return PolicyView{}, mapStoreErr(err)
	}
	return view(saved), nil
}

func (s *Service) Get(ctx context.Context, id string) (PolicyView, error) {
	p, err := s.repo.Get(ctx, id)
	if err != nil {
		return PolicyView{}, mapStoreErr(err)
	}
	return view(p), nil
}

func (s *Service) ListByProject(ctx context.Context, projectID string) ([]PolicyView, error) {
	ps, err := s.repo.ListByProject(ctx, projectID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	out := make([]PolicyView, 0, len(ps))
	for _, p := range ps {
		out = append(out, view(p))
	}
	return out, nil
}

// Update changes interval, status, and/or the config blob. Empty Config leaves
// the stored blob unchanged.
func (s *Service) Update(ctx context.Context, id string, intervalSeconds *int64, status *string, cfg *rotatorConfig) (PolicyView, error) {
	if status != nil && *status != "active" && *status != "paused" {
		return PolicyView{}, ErrInvalidConfig // callers cannot set 'failed' directly
	}
	p, err := s.repo.Get(ctx, id)
	if err != nil {
		return PolicyView{}, mapStoreErr(err)
	}
	var ct, nonce, wrapped []byte
	var kekVer *int
	if cfg != nil {
		proj, err := s.projects.Get(ctx, p.ProjectID)
		if err != nil {
			return PolicyView{}, mapStoreErr(err)
		}
		c, n, w, v, err := s.sealConfig(proj, id, *cfg)
		if err != nil {
			return PolicyView{}, err
		}
		ct, nonce, wrapped, kekVer = c, n, w, &v
	}
	if err := s.repo.Update(ctx, id, intervalSeconds, status, ct, nonce, wrapped, kekVer); err != nil {
		return PolicyView{}, mapStoreErr(err)
	}
	return s.Get(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	return mapStoreErr(s.repo.Delete(ctx, id))
}

// RotateNow runs an immediate rotation, clearing 'failed' status first so a
// manual trigger always attempts. Returns the produced config version.
func (s *Service) RotateNow(ctx context.Context, id string) (int, error) {
	p, err := s.repo.Get(ctx, id)
	if err != nil {
		return 0, mapStoreErr(err)
	}
	if s.kr.Sealed() {
		return 0, ErrSealed
	}
	if p.Status == "failed" {
		active := "active"
		if err := s.repo.Update(ctx, id, nil, &active, nil, nil, nil, nil); err != nil {
			return 0, mapStoreErr(err)
		}
		p.Status = "active"
		p.FailureCount = 0
	}
	if err := s.attempt(ctx, p); err != nil {
		return 0, err
	}
	np, err := s.repo.Get(ctx, id)
	if err != nil {
		return 0, mapStoreErr(err)
	}
	if np.LastConfigVersion == nil {
		return 0, nil
	}
	return *np.LastConfigVersion, nil
}
```

- [ ] **Step 2: Test**

`internal/rotation/crud_test.go`: Create (postgres + webhook), invalid type/interval/role/URL rejected, duplicate (config,key) → `ErrExists`, Get/ListByProject, Update interval+status, Update rejecting `status=failed`, RotateNow clearing a failed policy. Reuse the seeded-project harness.

- [ ] **Step 3: Run**

Run: `go test ./internal/rotation/`
Expected: PASS (full package).

- [ ] **Step 4: Commit**

```bash
git add internal/rotation/rotation.go internal/rotation/crud_test.go
git commit -m "feat(rotation): engine CRUD + RotateNow with masked views"
```

---

## Task 10: RBAC action + error code + server/boot wiring

**Files:**
- Modify: `internal/authz/actions.go`, `internal/api/errors.go`, `internal/api/server.go`, `internal/api/boot.go`, `cmd/janus/server.go`

- [ ] **Step 1: RBAC action**

In `internal/authz/actions.go`, add to the const block:

```go
	RotationManage Action = "rotation:manage" // project-scoped
```

and include it in `adminActions`:

```go
	adminActions = union(developerActions, setOf(
		ConfigDelete, EnvCreate, EnvDelete, ProjectCreate, MemberManage,
		TokenRead, TokenMint, TokenRevoke, UserManage, AuditRead, SysSeal, SysBackup,
		TransitManage, OIDCManage, RotationManage))
```

If `internal/authz` has a test asserting the admin action set (grep for `adminActions`/`RotationManage` in `*_test.go`), extend it to include `RotationManage`.

- [ ] **Step 2: Error code**

In `internal/api/errors.go` const block, add:

```go
	CodeRotationNotFound = "rotation_not_found"
```

- [ ] **Step 3: Server field + New param**

In `internal/api/server.go`:
- add import `"github.com/steveokay/janus-secrets/internal/rotation"`.
- add field to `Server`: `rotation *rotation.Service // nil in unit-test servers`.
- add param to `New(...)`: after `tr *transit.Service,` add `rot *rotation.Service,`.
- set it in the struct literal: `rotation: rot,`.
- **every existing caller of `New(...)` must pass the new arg.** `Boot` passes the real service (next step); any unit test constructing `New` directly passes `nil`. Grep `api.New(`/`New(Config{` across `internal/api/*_test.go` and insert `nil` in the transit-adjacent position.

- [ ] **Step 4: Route group** (added inside the `if s.auth != nil && s.authz != nil {` block, alongside the transit group):

```go
			if s.rotation != nil {
				r.Group(func(r chi.Router) {
					r.Use(RequireAuth(s.auth))
					r.Post("/v1/rotation/policies", s.handleRotationCreate)
					r.Get("/v1/rotation/policies", s.handleRotationList)
					r.Get("/v1/rotation/policies/{id}", s.handleRotationGet)
					r.Patch("/v1/rotation/policies/{id}", s.handleRotationUpdate)
					r.Delete("/v1/rotation/policies/{id}", s.handleRotationDelete)
					r.Post("/v1/rotation/policies/{id}/rotate", s.handleRotationRotateNow)
				})
			}
```

- [ ] **Step 5: Boot wiring + scheduler start**

In `internal/api/boot.go`:
- add `RotationTick time.Duration` to `BootConfig` (doc: "rotation scheduler tick; 0 disables; cmd/janus applies the production default").
- after `transitSvc := transit.New(kr, st)`, add: `rotationSvc := rotation.New(kr, st, svc, auditRec, logger)`.
- pass `rotationSvc` to `New(...)` in the transit-adjacent position.
- after `srv := New(...)` and `srv.MountUI(...)`, start the scheduler tied to the boot ctx:

```go
	if bc.RotationTick > 0 {
		go rotationSvc.RunScheduler(ctx, bc.RotationTick)
	}
```

Add import `"github.com/steveokay/janus-secrets/internal/rotation"`.

> The `ctx` passed to `Boot` is `runServer`'s `signal.NotifyContext` shutdown context, so the goroutine stops on SIGTERM. Tests calling `Boot` leave `RotationTick` zero → no background goroutine.

- [ ] **Step 6: cmd/janus env parsing**

In `cmd/janus/server.go` `runServer`, after the idle-timeout block, add (mirroring it):

```go
	rotationTick := 60 * time.Second // production default; 0 disables
	if v := os.Getenv("JANUS_ROTATION_TICK"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return fmt.Errorf("invalid JANUS_ROTATION_TICK %q: use a Go duration like 60s, or 0 to disable", v)
		}
		rotationTick = d
	}
```

and set `RotationTick: rotationTick,` in the `api.BootConfig{...}` literal.

- [ ] **Step 7: Build + vet**

Run: `go build ./... && go vet ./...`
Expected: clean (handlers referenced in the route group are added in Task 11 — if building before Task 11, temporarily comment the route group, or implement Task 11 first then this step. Recommended: do Task 11 Step 1 before this build).

- [ ] **Step 8: Commit**

```bash
git add internal/authz/actions.go internal/api/errors.go internal/api/server.go internal/api/boot.go cmd/janus/server.go
git commit -m "feat(api): rotation RBAC action, wiring, scheduler start"
```

---

## Task 11: REST handlers

**Files:**
- Create: `internal/api/rotation_handlers.go`
- Test: `internal/api/rotation_e2e_test.go`

- [ ] **Step 1: Handlers**

`internal/api/rotation_handlers.go`:

```go
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/rotation"
)

type rotationConfigReq struct {
	AdminDSN      string `json:"admin_dsn,omitempty"`
	Role          string `json:"role,omitempty"`
	PasswordLen   int    `json:"password_len,omitempty"`
	URL           string `json:"url,omitempty"`
	HMACKey       string `json:"hmac_key,omitempty"`
	NotifyURL     string `json:"notify_url,omitempty"`
	NotifyHMACKey string `json:"notify_hmac_key,omitempty"`
}

type createRotationReq struct {
	ConfigID        string            `json:"config_id"`
	SecretKey       string            `json:"secret_key"`
	Type            string            `json:"type"`
	IntervalSeconds int64             `json:"interval_seconds"`
	Config          rotationConfigReq `json:"config"`
}

type updateRotationReq struct {
	IntervalSeconds *int64             `json:"interval_seconds"`
	Status          *string            `json:"status"`
	Config          *rotationConfigReq `json:"config"`
}

// rotationView is the masked JSON projection.
type rotationView struct {
	ID                string  `json:"id"`
	ProjectID         string  `json:"project_id"`
	ConfigID          string  `json:"config_id"`
	SecretKey         string  `json:"secret_key"`
	Type              string  `json:"type"`
	IntervalSeconds   int64   `json:"interval_seconds"`
	Status            string  `json:"status"`
	FailureCount      int     `json:"failure_count"`
	LastError         *string `json:"last_error,omitempty"`
	NextRotationAt    string  `json:"next_rotation_at"`
	LastRotatedAt     *string `json:"last_rotated_at,omitempty"`
	LastConfigVersion *int    `json:"last_config_version,omitempty"`
	CreatedAt         string  `json:"created_at"`
}

func toRotationView(v rotation.PolicyView) rotationView {
	out := rotationView{
		ID: v.ID, ProjectID: v.ProjectID, ConfigID: v.ConfigID, SecretKey: v.SecretKey,
		Type: v.Type, IntervalSeconds: v.IntervalSeconds, Status: v.Status,
		FailureCount: v.FailureCount, LastError: v.LastError,
		NextRotationAt: v.NextRotationAt.UTC().Format(rfc3339), LastConfigVersion: v.LastConfigVersion,
		CreatedAt: v.CreatedAt.UTC().Format(rfc3339),
	}
	if v.LastRotatedAt != nil {
		s := v.LastRotatedAt.UTC().Format(rfc3339)
		out.LastRotatedAt = &s
	}
	return out
}

// writeRotationErr maps engine sentinels to the JSON envelope.
func (s *Server) writeRotationErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, rotation.ErrNotFound):
		writeError(w, http.StatusNotFound, CodeRotationNotFound, "rotation policy not found")
	case errors.Is(err, rotation.ErrExists):
		writeError(w, http.StatusConflict, CodeValidation, "a rotation policy already exists for this config and key")
	case errors.Is(err, rotation.ErrInvalidType), errors.Is(err, rotation.ErrInvalidConfig):
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid rotation policy configuration")
	case errors.Is(err, rotation.ErrSealed):
		writeError(w, http.StatusServiceUnavailable, CodeSealed, "server is sealed")
	default:
		s.writeServiceError(w, err)
	}
}

func (rc rotationConfigReq) toEngine() rotation.PolicyConfig {
	return rotation.PolicyConfig{
		AdminDSN: rc.AdminDSN, Role: rc.Role, PasswordLen: rc.PasswordLen,
		URL: rc.URL, HMACKey: rc.HMACKey, NotifyURL: rc.NotifyURL, NotifyHMACKey: rc.NotifyHMACKey,
	}
}

func (s *Server) handleRotationCreate(w http.ResponseWriter, r *http.Request) {
	var req createRotationReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ConfigID == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "config_id, secret_key, type are required")
		return
	}
	res, err := s.resolveScopeResource(r.Context(), "config", req.ConfigID)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !s.authorize(w, r, authz.RotationManage, res, "rotation.create", "configs/"+req.ConfigID) {
		return
	}
	p := PrincipalName(r) // helper: principal display/id for created_by
	v, err := s.rotation.Create(r.Context(), rotation.PolicyInput{
		ConfigID: req.ConfigID, SecretKey: req.SecretKey, Type: req.Type,
		IntervalSeconds: req.IntervalSeconds, Config: req.Config.toEngine(),
	}, p)
	if err != nil {
		s.writeRotationErr(w, err)
		return
	}
	if err := s.record(r, "rotation.create", "rotation/"+v.ID, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, toRotationView(v))
}

// handleRotationList requires project_id and authorizes on that project.
func (s *Server) handleRotationList(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	if projectID == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "project_id is required")
		return
	}
	if err := s.can(r, authz.RotationManage, authz.Resource{ProjectID: projectID}); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	vs, err := s.rotation.ListByProject(r.Context(), projectID)
	if err != nil {
		s.writeRotationErr(w, err)
		return
	}
	out := make([]rotationView, 0, len(vs))
	for _, v := range vs {
		out = append(out, toRotationView(v))
	}
	writeJSON(w, http.StatusOK, map[string]any{"policies": out})
}

// rotationResource loads a policy and returns its project-scoped authz resource.
func (s *Server) rotationResource(r *http.Request) (authz.Resource, rotation.PolicyView, error) {
	id := chi.URLParam(r, "id")
	v, err := s.rotation.Get(r.Context(), id)
	if err != nil {
		return authz.Resource{}, rotation.PolicyView{}, err
	}
	res, err := s.resolveScopeResource(r.Context(), "config", v.ConfigID)
	return res, v, err
}

func (s *Server) handleRotationGet(w http.ResponseWriter, r *http.Request) {
	res, v, err := s.rotationResource(r)
	if err != nil {
		s.writeRotationErr(w, err)
		return
	}
	if err := s.can(r, authz.RotationManage, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toRotationView(v))
}

func (s *Server) handleRotationUpdate(w http.ResponseWriter, r *http.Request) {
	res, v, err := s.rotationResource(r)
	if err != nil {
		s.writeRotationErr(w, err)
		return
	}
	if !s.authorize(w, r, authz.RotationManage, res, "rotation.update", "rotation/"+v.ID) {
		return
	}
	var req updateRotationReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	var cfg *rotation.PolicyConfig
	if req.Config != nil {
		c := req.Config.toEngine()
		cfg = &c
	}
	updated, err := s.rotation.Update(r.Context(), v.ID, req.IntervalSeconds, req.Status, cfg)
	if err != nil {
		s.writeRotationErr(w, err)
		return
	}
	if err := s.record(r, "rotation.update", "rotation/"+v.ID, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, toRotationView(updated))
}

func (s *Server) handleRotationDelete(w http.ResponseWriter, r *http.Request) {
	res, v, err := s.rotationResource(r)
	if err != nil {
		s.writeRotationErr(w, err)
		return
	}
	if !s.authorize(w, r, authz.RotationManage, res, "rotation.delete", "rotation/"+v.ID) {
		return
	}
	if err := s.rotation.Delete(r.Context(), v.ID); err != nil {
		s.writeRotationErr(w, err)
		return
	}
	if err := s.record(r, "rotation.delete", "rotation/"+v.ID, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (s *Server) handleRotationRotateNow(w http.ResponseWriter, r *http.Request) {
	res, v, err := s.rotationResource(r)
	if err != nil {
		s.writeRotationErr(w, err)
		return
	}
	if !s.authorize(w, r, authz.RotationManage, res, "rotation.rotate", "rotation/"+v.ID) {
		return
	}
	// The engine writes its own rotation.rotate audit event (system actor).
	ver, err := s.rotation.RotateNow(r.Context(), v.ID)
	if err != nil {
		s.writeRotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rotated": true, "config_version": ver})
}
```

> **Adjustments to reconcile with existing code (grep first):**
> - `rfc3339`: use the timestamp format constant already used by other handlers (e.g. configs handler uses `time.RFC3339`). Replace `rfc3339` with `time.RFC3339` and import `"time"`, or reuse an existing shared const.
> - `PrincipalName(r)`: there is an existing way handlers get the caller identity for `created_by`/audit (see `PrincipalFrom(r.Context())` in `audit.go`). Replace `PrincipalName(r)` with the real accessor, e.g. `p, _ := PrincipalFrom(r.Context()); created := p.Name` (fall back to `p.ID`).
> - `rotation.PolicyConfig`: the engine's `rotatorConfig` is unexported. Either (a) export it as `rotation.PolicyConfig` and use it as the `PolicyInput.Config` field type, or (b) add exported setter fields on `PolicyInput`. Simplest: rename `rotatorConfig` → exported `PolicyConfig` in `internal/rotation/rotation.go` and update references. Do this rename in Task 9's file when implementing, so the API layer can construct it. Keep the JSON tags.
> - `authz.Resource{ProjectID: projectID}` for list: confirm `authz.Resource` has a `ProjectID` field (it does — see `resolveScopeResource`). A project-only resource is sufficient for a project-scoped role check.

- [ ] **Step 2: e2e test**

`internal/api/rotation_e2e_test.go` — using `authStackFull` / `drillStack`-style harness (real stack, admin login, a project+env+config+secret seeded):
- `TestRotationCRUDViaAPI`: admin creates a webhook policy (httptest receiver) → 201 masked view (assert response JSON contains NO `admin_dsn`/`hmac_key`/`url`); list by project; get; patch interval; rotate-now → 200 with `config_version`; delete → 200; get → 404 `rotation_not_found`.
- `TestRotationForbiddenForViewer`: a viewer token/user → 403 on create.
- `TestRotationMaskingHidesSecrets`: create a postgres policy, GET it, assert the raw response body does not contain the admin DSN substring.
- `TestRotationRotateNowWhileSealed`: seal → rotate-now returns 503 `sealed`. (If the harness can't easily seal mid-test, assert via a unit-level engine call instead.)

- [ ] **Step 3: Run**

Run: `go test ./internal/api/ -run TestRotation`
Expected: PASS.

- [ ] **Step 4: Build the whole tree + commit**

```bash
go build ./... && go vet ./...
git add internal/api/rotation_handlers.go internal/api/rotation_e2e_test.go
git commit -m "feat(api): rotation REST handlers (masked, project-scoped, audited)"
```

---

## Task 12: CLI

**Files:**
- Create: `cmd/janus/rotation_commands.go`
- Modify: wherever root registers subcommands (grep `AddCommand(` in `cmd/janus/`)
- Test: `cmd/janus/rotation_commands_test.go` (light — flag parsing / request shaping; mirror any existing `*_commands_test.go`)

- [ ] **Step 1: Command tree**

`cmd/janus/rotation_commands.go`:

```go
package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newRotationCmd() *cobra.Command {
	var address, token string
	cmd := &cobra.Command{
		Use:   "rotation",
		Short: "Manage secret rotation policies",
	}
	cmd.PersistentFlags().StringVar(&address, "address", "", "server address (default: stored/env)")
	cmd.PersistentFlags().StringVar(&token, "token", "", "service token (default: stored/env)")

	// create
	var configID, secretKey, typ string
	var intervalSeconds int64
	var adminDSN, role string
	var passwordLen int
	var url, hmacKey, notifyURL, notifyHMACKey string
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a rotation policy",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			body := map[string]any{
				"config_id": configID, "secret_key": secretKey, "type": typ,
				"interval_seconds": intervalSeconds,
				"config": map[string]any{
					"admin_dsn": adminDSN, "role": role, "password_len": passwordLen,
					"url": url, "hmac_key": hmacKey,
					"notify_url": notifyURL, "notify_hmac_key": notifyHMACKey,
				},
			}
			var out map[string]any
			if err := c.call("POST", "/v1/rotation/policies", body, &out); err != nil {
				return err
			}
			fmt.Printf("created rotation policy %v (status %v)\n", out["id"], out["status"])
			return nil
		},
	}
	create.Flags().StringVar(&configID, "config", "", "target config id (required)")
	create.Flags().StringVar(&secretKey, "key", "", "secret key to rotate (required)")
	create.Flags().StringVar(&typ, "type", "", "rotator type: postgres|webhook (required)")
	create.Flags().Int64Var(&intervalSeconds, "interval-seconds", 0, "rotation interval in seconds (required)")
	create.Flags().StringVar(&adminDSN, "admin-dsn", "", "postgres admin DSN (postgres type)")
	create.Flags().StringVar(&role, "role", "", "postgres role to rotate (postgres type)")
	create.Flags().IntVar(&passwordLen, "password-len", 32, "generated password length")
	create.Flags().StringVar(&url, "url", "", "webhook URL (webhook type)")
	create.Flags().StringVar(&hmacKey, "hmac-key", "", "webhook HMAC signing key (webhook type)")
	create.Flags().StringVar(&notifyURL, "notify-url", "", "optional post-rotation notify URL")
	create.Flags().StringVar(&notifyHMACKey, "notify-hmac-key", "", "optional notify HMAC key")

	// list
	var projectID string
	list := &cobra.Command{
		Use:   "list",
		Short: "List rotation policies for a project",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out struct {
				Policies []struct {
					ID, SecretKey, Type, Status string
					NextRotationAt              string `json:"next_rotation_at"`
				} `json:"policies"`
			}
			if err := c.call("GET", "/v1/rotation/policies?project_id="+projectID, nil, &out); err != nil {
				return err
			}
			for _, p := range out.Policies {
				fmt.Printf("%s  %-20s %-8s %-8s next=%s\n", p.ID, p.SecretKey, p.Type, p.Status, p.NextRotationAt)
			}
			return nil
		},
	}
	list.Flags().StringVar(&projectID, "project", "", "project id (required)")

	// get / delete / rotate by id
	get := &cobra.Command{
		Use: "get <id>", Short: "Show a rotation policy", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out map[string]any
			if err := c.call("GET", "/v1/rotation/policies/"+args[0], nil, &out); err != nil {
				return err
			}
			fmt.Printf("%+v\n", out)
			return nil
		},
	}
	del := &cobra.Command{
		Use: "delete <id>", Short: "Delete a rotation policy", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			return c.call("DELETE", "/v1/rotation/policies/"+args[0], nil, nil)
		},
	}
	var setInterval int64
	var setStatus string
	update := &cobra.Command{
		Use: "update <id>", Short: "Update interval or status", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			body := map[string]any{}
			if setInterval > 0 {
				body["interval_seconds"] = setInterval
			}
			if setStatus != "" {
				body["status"] = setStatus
			}
			return c.call("PATCH", "/v1/rotation/policies/"+args[0], body, nil)
		},
	}
	update.Flags().Int64Var(&setInterval, "interval-seconds", 0, "new interval")
	update.Flags().StringVar(&setStatus, "status", "", "new status: active|paused")

	rotate := &cobra.Command{
		Use: "rotate <id>", Short: "Rotate now", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var out struct {
				ConfigVersion int `json:"config_version"`
			}
			if err := c.call("POST", "/v1/rotation/policies/"+args[0]+"/rotate", nil, &out); err != nil {
				return err
			}
			fmt.Printf("rotated → config version %d\n", out.ConfigVersion)
			return nil
		},
	}

	cmd.AddCommand(create, list, get, update, del, rotate)
	return cmd
}
```

- [ ] **Step 2: Register**

In the root command builder (grep `AddCommand(newServerCmd` or similar in `cmd/janus/`), add `rootCmd.AddCommand(newRotationCmd())`.

- [ ] **Step 3: Build + minimal test**

`cmd/janus/rotation_commands_test.go`: assert `newRotationCmd()` has subcommands `create/list/get/update/delete/rotate` and that `create` declares the expected flags. Mirror an existing command test if present.

Run: `go build ./... && go test ./cmd/janus/ -run Rotation`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/janus/rotation_commands.go cmd/janus/rotation_commands_test.go cmd/janus/*.go
git commit -m "feat(cli): janus rotation create/list/get/update/delete/rotate"
```

---

## Task 13: Docs

**Files:**
- Create: `docs/ops/rotation.md`
- Modify: `docs/operations.md`

- [ ] **Step 1: Runbook**

`docs/ops/rotation.md` covering:
- What rotation does (single-role Postgres reset; generic webhook push; optional notify).
- **Webhook receiver contract:** verify `X-Janus-Signature: sha256=<hex>` with a constant-time compare (`hmac.Equal`); must be **idempotent** (Janus resends the same pending value on retry/recovery); return 2xx only after the value is durably applied.
- **Postgres admin role least privilege:** the `admin_dsn` needs only `ALTER ROLE <target>` capability (a role with `CREATEROLE`, or ownership of the target); do not use a superuser if avoidable.
- **Sealed behavior:** rotation pauses while sealed; due policies rotate after unseal.
- **Failure handling:** backoff retries, `failed` after 5 consecutive failures, `janus rotation rotate <id>` (or PATCH `status=active`) to resume.
- **Env var:** `JANUS_ROTATION_TICK` (default 60s; 0 disables the scheduler).
- Security note: values and admin DSNs never appear in logs, audit, `last_error`, or API responses.

- [ ] **Step 2: operations.md rows**

Add to `docs/operations.md`:
- CLI rows for `janus rotation create|list|get|update|delete|rotate`.
- An env-var row for `JANUS_ROTATION_TICK` (mirror the `JANUS_SESSION_IDLE_TIMEOUT` row format).

- [ ] **Step 3: Commit**

```bash
git add docs/ops/rotation.md docs/operations.md
git commit -m "docs(rotation): runbook + operations references"
```

---

## Task 14: Full-suite gates + PR

- [ ] **Step 1: Run every gate**

```bash
go build ./...
go vet ./...
go test ./...
gosec -exclude-dir=internal/crypto/shamir ./...
govulncheck ./...
```

Expected: all green. gosec may flag the `fmt.Sprintf` ALTER ROLE statement — it is annotated/justified (identifier sanitized, literal quoted, value alphanumeric); add a `// #nosec G201` with that rationale on that exact line if gosec flags it, matching how `internal/store/backup.go` annotates its dynamic SQL.

- [ ] **Step 2: Leak check**

Confirm the `internal/rotation` tests (and the API e2e) assert no admin DSN / secret value appears in captured logs or error strings. If the repo has a central leak test (grep `func TestNo.*Leak` / `captured logs`), extend it to run a rotation and scan; otherwise the per-package assertions in Tasks 7 & 11 suffice.

- [ ] **Step 3: Push + open PR**

```bash
git push -u origin phase3-static-rotation
gh pr create --title "feat: static rotation framework (Phase 3.1)" --body "<summary + spec link>"
```

---

## Self-Review (author checklist — completed)

**Spec coverage:**
- §1 boundaries → engine skeleton (T4), two rotators (T5,T6), notify (T5). ✓
- §2 data model → migration (T2), repo (T3), backup inclusion (T3). ✓
- §3 crash-safe ordering → execute.go `rotate` persist-pending→apply→commit + recovery (T7). ✓
- §4 scheduler → scheduler.go + Boot wiring + `JANUS_ROTATION_TICK` (T8,T10). ✓
- §5 failure handling → backoff + MarkFailure threshold + audit every attempt (T3,T7). ✓
- §6 API (masked, project-scoped) → handlers (T11) + RBAC action (T10) + error code (T10). ✓
- §7 CLI → rotation_commands.go (T12). ✓
- §8 crypto/security → AAD helpers (T1), envelope seal/open (T4), constant-time receiver documented (T13), sanitized errors (T7). ✓
- §9 testing → per-task unit + testcontainers e2e + leak (T7,T11,T14). ✓

**Type consistency:** `rotatorConfig` is renamed to exported `PolicyConfig` (noted in T11) so the API layer constructs it; `PolicyInput.Config` uses it. `apply(ctx, cfg, policyID, secretKey, newValue)` signature is identical across `webhookRotator`, `postgresRotator`, and the `rotatorApplier` interface. `RotationRepo` method names (`ClaimDue`, `SetPending`, `MarkRotated`, `MarkFailure`) are used consistently in `execute.go`/`scheduler.go`. `New(...)` param order (transit then rotation) matches in `server.go` and `boot.go`.

**Known reconciliations flagged for implementers (grep-and-adjust, not placeholders):** timestamp format constant, principal accessor for `created_by`, store test-DB bootstrap helper names, audit `Event` field names, and any unit-test `api.New(...)` call sites needing a `nil` rotation arg. Each is called out at its task with the exact existing symbol to mirror.
