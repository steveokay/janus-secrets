# Sync Integrations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One-way replication of a Janus config's resolved secrets to external stores (GitHub Actions secrets, Kubernetes Secrets) via a pluggable, scheduled sync engine on branch `phase3-sync-integrations`.

**Architecture:** A new `internal/secretsync` engine (mirrors `internal/rotation`, which just merged in PR #52): a `Service` over a store repo + keyring + the `resolve` machinery + audit, with an in-process scheduler. Targets bind a config to an external destination; a reconcile loop resolves the config's secrets, detects change via a keyed fingerprint, and delegates transport to a `Provider` (github or k8s), both raw REST over `net/http`. Target credentials are envelope-encrypted exactly like rotation config.

**Tech Stack:** Go (stdlib `crypto/hmac`, `net/http`, `encoding/base64`, `golang.org/x/crypto/nacl/box`), Postgres, `chi`, `cobra`, testcontainers + `httptest` provider fakes.

**Spec:** `docs/superpowers/specs/2026-07-10-sync-integrations-design.md`

**PRIMARY REFERENCE — mirror the just-merged rotation feature.** This feature is structurally a twin of static rotation (Phase 3.1, merged to `main`). For every "boilerplate" task (store repo, engine skeleton, scheduler, engine CRUD, RBAC/wiring, REST handlers, CLI), open the rotation equivalent and mirror it, changing only the domain specifics called out here:
- Engine: `internal/rotation/rotation.go` (Service + New + envelope seal/open), `internal/rotation/scheduler.go`, `internal/rotation/execute.go` (backoff, attempt, audit), `internal/rotation/errors.go`.
- Store: `internal/store/rotation.go` (repo shape, `ClaimDue`, `MarkFailure`, `PrepareRotateNow`), `internal/store/backup.go` (backupTables inclusion).
- API: `internal/api/rotation_handlers.go`, the `if s.rotation != nil` route group in `internal/api/server.go`, RBAC action in `internal/authz/actions.go`, wiring in `internal/api/boot.go` + `internal/api/server.go` `New`, env parsing in `cmd/janus/server.go`.
- CLI: `cmd/janus/rotation_commands.go`.
- Crypto: `internal/crypto/keys.go` AAD helpers, `internal/crypto/keyring.go` (`NewDEK`, `UnwrapProjectKEK`, master-key access pattern).
- Migration: `migrations/000010_rotation.{up,down}.sql`.

**Gates for every commit:** `go build ./...`, `go vet ./...`, `go test ./...`. Full sweep before the PR: also `gosec -exclude-dir=internal/crypto/shamir ./...` and `govulncheck ./...`.

---

## File Structure

- `internal/crypto/keys.go` — **modify**: add `SyncCredsAAD`.
- `internal/crypto/keyring.go` — **modify**: add `Keyring.SyncFingerprint(data []byte) []byte`.
- `migrations/000011_sync.{up,down}.sql` — **create**.
- `internal/store/sync.go` — **create**: `SyncTarget` model + `SyncTargetRepo`.
- `internal/store/backup.go` — **modify**: add `sync_targets` to `backupTables`.
- `internal/secretsync/secretsync.go` — **create**: `Service`, `New`, envelope creds seal/open.
- `internal/secretsync/provider.go` — **create**: `Provider` interface + `Creds`/`Addr`/`ApplyResult` types + provider dispatch.
- `internal/secretsync/github_provider.go` — **create**.
- `internal/secretsync/k8s_provider.go` — **create**.
- `internal/secretsync/reconcile.go` — **create**: resolve → fingerprint → apply → commit, backoff, attempt, audit, system authorizer.
- `internal/secretsync/scheduler.go` — **create**.
- `internal/secretsync/crud.go` — **create**: engine CRUD + SyncNow + masked views.
- `internal/secretsync/errors.go` — **create**.
- `internal/api/sync_handlers.go` — **create**; `internal/api/server.go` route group; `internal/authz/actions.go` `SyncManage`; `internal/api/errors.go` `CodeSyncNotFound`; `internal/api/boot.go` + `server.go` wiring; `cmd/janus/server.go` `JANUS_SYNC_TICK`.
- `cmd/janus/sync_commands.go` — **create**; register in `cmd/janus/main.go`.
- `docs/ops/sync.md` — **create**; `docs/operations.md` — **modify**.

---

## Task 1: Crypto — SyncCredsAAD + SyncFingerprint

**Files:**
- Modify: `internal/crypto/keys.go`, `internal/crypto/keyring.go`
- Test: `internal/crypto/keys_test.go`, `internal/crypto/keyring_test.go` (append)

- [ ] **Step 1: Failing tests**

Append to `internal/crypto/keys_test.go`:

```go
func TestSyncCredsAAD(t *testing.T) {
	if bytes.Equal(SyncCredsAAD("t1"), SyncCredsAAD("t2")) {
		t.Fatal("SyncCredsAAD must bind to target id")
	}
	if bytes.Equal(SyncCredsAAD("ab"), SyncCredsAAD("a\x00b")) {
		t.Fatal("SyncCredsAAD must be injective over target id")
	}
	// distinct domain from rotation config AAD
	if bytes.Equal(SyncCredsAAD("x"), RotationConfigAAD("x")) {
		t.Fatal("sync creds AAD must differ from rotation config AAD")
	}
}
```

Append to `internal/crypto/keyring_test.go` (mirror an existing keyring test's unseal setup — a keyring is unsealed via `Unseal(master []byte)` with a 32-byte master; see existing tests):

```go
func TestSyncFingerprint(t *testing.T) {
	k := NewKeyring()
	master := make([]byte, KeySize)
	for i := range master { master[i] = byte(i) }
	if err := k.Unseal(master); err != nil { t.Fatal(err) }

	a := k.SyncFingerprint([]byte("hello"))
	b := k.SyncFingerprint([]byte("hello"))
	c := k.SyncFingerprint([]byte("world"))
	if !bytes.Equal(a, b) { t.Fatal("fingerprint must be deterministic") }
	if bytes.Equal(a, c) { t.Fatal("fingerprint must vary with input") }
	if len(a) != 32 { t.Fatalf("want 32-byte HMAC-SHA256, got %d", len(a)) }

	// sealed keyring returns nil (no master)
	k.Seal()
	if k.SyncFingerprint([]byte("hello")) != nil {
		t.Fatal("sealed keyring must return nil fingerprint")
	}
}
```

Ensure both test files import `"bytes"`.

- [ ] **Step 2: Run → FAIL** (`SyncCredsAAD`/`SyncFingerprint` undefined).

Run: `go test ./internal/crypto/ -run 'SyncCredsAAD|SyncFingerprint'`

- [ ] **Step 3: Implement**

Append to `internal/crypto/keys.go` (after `RotationPendingAAD`):

```go
// SyncCredsAAD binds a sync target's encrypted credentials blob (GitHub PAT or
// k8s token/CA) to its target, in a domain distinct from rotation AADs.
func SyncCredsAAD(targetID string) []byte {
	return appendField([]byte("janus:sync:creds"), targetID)
}
```

In `internal/crypto/keyring.go`, add (mirror how existing methods guard on `k.Sealed()` / access `k.master`; check the file for the exact field name — it is the in-memory master set by `Unseal`):

```go
// SyncFingerprint returns HMAC-SHA256 over data, keyed by a subkey derived from
// the master key with a fixed domain label, so the value stored for sync change
// detection is not a reversible hash of secret material. Returns nil while
// sealed (no master in memory).
func (k *Keyring) SyncFingerprint(data []byte) []byte {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.master == nil {
		return nil
	}
	sub := hmac.New(sha256.New, k.master)
	sub.Write([]byte("janus:sync:fingerprint-key"))
	mac := hmac.New(sha256.New, sub.Sum(nil))
	mac.Write(data)
	return mac.Sum(nil)
}
```

Add imports `crypto/hmac`, `crypto/sha256` to `keyring.go` if missing. **Verify the master field name and the mutex** by reading `keyring.go` first — mirror how `Sealed()`/`NewDEK` read the master and lock. If there is no `mu`/`RLock`, follow whatever synchronization the existing methods use (or none).

- [ ] **Step 4: Run → PASS.** `go test ./internal/crypto/ -run 'SyncCredsAAD|SyncFingerprint'`

- [ ] **Step 5: Commit**

```bash
git add internal/crypto/keys.go internal/crypto/keyring.go internal/crypto/keys_test.go internal/crypto/keyring_test.go
git commit -m "feat(crypto): sync creds AAD + keyed change-detection fingerprint"
```

---

## Task 2: Migration 000011 (sync_targets)

**Files:**
- Create: `migrations/000011_sync.up.sql`, `migrations/000011_sync.down.sql`
- Test: `internal/store/sync_migration_test.go`

- [ ] **Step 1: up migration** — `migrations/000011_sync.up.sql`:

```sql
CREATE TABLE sync_targets (
  id                     uuid PRIMARY KEY,
  project_id             uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  config_id              uuid NOT NULL REFERENCES configs(id)  ON DELETE CASCADE,
  provider               text NOT NULL CHECK (provider IN ('github','k8s')),
  prune                  bool NOT NULL DEFAULT true,
  interval_seconds       bigint NOT NULL CHECK (interval_seconds > 0),
  next_sync_at           timestamptz NOT NULL,
  status                 text NOT NULL DEFAULT 'active' CHECK (status IN ('active','failed','paused')),
  failure_count          int  NOT NULL DEFAULT 0,
  last_error             text,
  last_synced_at         timestamptz,
  synced_config_version  int,
  creds_ct               bytea NOT NULL,
  creds_nonce            bytea NOT NULL,
  creds_wrapped_dek      bytea NOT NULL,
  creds_dek_kek_version  int   NOT NULL,
  addr                   jsonb NOT NULL,
  managed_keys           text[] NOT NULL DEFAULT '{}',
  synced_fingerprint     bytea,
  created_by             text NOT NULL,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now()
);

-- One target per (config, provider, destination). addr is jsonb; hash it so the
-- destination participates in the uniqueness constraint.
CREATE UNIQUE INDEX sync_targets_dest ON sync_targets (config_id, provider, md5(addr::text));

-- Scheduler due-scan.
CREATE INDEX sync_targets_due ON sync_targets (next_sync_at) WHERE status = 'active';
```

`migrations/000011_sync.down.sql`:

```sql
DROP TABLE sync_targets;
```

- [ ] **Step 2: migration test** — `internal/store/sync_migration_test.go`, mirroring `internal/store/rotation_migration_test.go` exactly (uses `requireStore(t)` + an `information_schema.tables` EXISTS query, asserting `sync_targets` exists).

- [ ] **Step 3: Run** `go test ./internal/store/ -run TestMigration011` → PASS (and existing `TestMigrateDownUp` still passes, exercising the down file).

- [ ] **Step 4: Commit**

```bash
git add migrations/000011_sync.up.sql migrations/000011_sync.down.sql internal/store/sync_migration_test.go
git commit -m "feat(store): migration 000011 sync_targets"
```

---

## Task 3: Store repository + backup inclusion

**Files:**
- Create: `internal/store/sync.go`
- Modify: `internal/store/backup.go`
- Test: `internal/store/sync_test.go`

Mirror `internal/store/rotation.go` closely. Differences: `provider`/`prune`/`addr jsonb`/`managed_keys text[]`/`synced_fingerprint bytea`/`synced_config_version` instead of the rotation-specific columns; the pending-value columns do NOT exist here.

- [ ] **Step 1: model + repo**

`internal/store/sync.go`:

```go
package store

import (
	"context"
	"time"
)

// SyncTarget is one sync binding: replicate a config's resolved secrets to one
// external destination. creds_* is the envelope-encrypted provider credential
// blob; addr is non-secret destination coordinates; managed_keys + fingerprint
// drive prune and change-detection.
type SyncTarget struct {
	ID                  string
	ProjectID           string
	ConfigID            string
	Provider            string // "github" | "k8s"
	Prune               bool
	IntervalSeconds     int64
	NextSyncAt          time.Time
	Status              string // "active" | "failed" | "paused"
	FailureCount        int
	LastError           *string
	LastSyncedAt        *time.Time
	SyncedConfigVersion *int
	CredsCT             []byte
	CredsNonce          []byte
	CredsWrappedDEK     []byte
	CredsDEKKEKVersion  int
	Addr                []byte // raw jsonb bytes
	ManagedKeys         []string
	SyncedFingerprint   []byte
	CreatedBy           string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type SyncTargetRepo struct{ s *Store }

func NewSyncTargetRepo(s *Store) *SyncTargetRepo { return &SyncTargetRepo{s: s} }

const syncCols = `id::text, project_id::text, config_id::text, provider, prune,
	interval_seconds, next_sync_at, status, failure_count, last_error,
	last_synced_at, synced_config_version, creds_ct, creds_nonce, creds_wrapped_dek,
	creds_dek_kek_version, addr, managed_keys, synced_fingerprint,
	created_by, created_at, updated_at`
```

Implement (mirroring rotation repo method bodies, adjusting columns):
- `scanTarget(row)` — scan all `syncCols` in order. `Addr` scans into `[]byte` (pgx returns jsonb as bytes); `ManagedKeys` scans into `&t.ManagedKeys` (pgx maps `text[]` → `[]string`).
- `Create(ctx, *SyncTarget) (*SyncTarget, error)` — INSERT the create-time columns (id, project_id, config_id, provider, prune, interval_seconds, next_sync_at, creds_*, addr, created_by); `addr` bound as `$N` with the raw bytes cast `$N::jsonb`. Duplicate destination → `ErrAlreadyExists`.
- `Get(ctx, id)`, `ListByProject(ctx, projectID)` (newest first), `Delete(ctx, id)`.
- `Update(ctx, id string, intervalSeconds *int64, prune *bool, status *string, credsCT, credsNonce, credsWrapped []byte, credsKEKVer *int, addr []byte) error` — COALESCE partial update (nil leaves unchanged; addr as `COALESCE($N::jsonb, addr)`).
- `ClaimDue(ctx, now, limit)` — `WHERE status='active' AND next_sync_at <= $1 ORDER BY next_sync_at ASC LIMIT $2` (identical semantics to rotation's fixed ClaimDue — no pending clause).
- `MarkSynced(ctx, id string, managedKeys []string, fingerprint []byte, configVersion int, next time.Time) error` — sets managed_keys, synced_fingerprint, synced_config_version, last_synced_at=now(), failure_count=0, status='active', last_error=NULL, next_sync_at=$next.
- `MarkFailure(ctx, id, sanitizedErr string, next time.Time, threshold int) error` — identical to rotation's.
- `PrepareSyncNow(ctx, id string, now time.Time) error` — identical to rotation's `PrepareRotateNow` (marks due; reactivates + resets counters only if `status='failed'`).

- [ ] **Step 2: backup inclusion.** In `internal/store/backup.go`, add `{"sync_targets", "created_at, id"}` to `backupTables` immediately after the `{"rotation_policies", ...}` entry (it references projects+configs, already earlier).

- [ ] **Step 3: test** — `internal/store/sync_test.go`, mirroring `internal/store/rotation_test.go`: Create→Get round-trip (all fields incl. Addr bytes round-trip and ManagedKeys empty default), duplicate destination → `ErrAlreadyExists`, ListByProject ordering, Update (interval/prune/status + nil no-ops), ClaimDue (future not selected, past selected), MarkSynced (managed_keys/fingerprint/version set, failure reset), MarkFailure threshold flip to `failed`, PrepareSyncNow (future→due; failed→active+reset), Delete→Get `ErrNotFound`. Reuse `mkConfig`/`requireStore`/`resetDB`.

- [ ] **Step 4: Run** `go test ./internal/store/ -run 'Sync|Migration011'` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/sync.go internal/store/sync_test.go internal/store/backup.go
git commit -m "feat(store): sync_targets repo + backup inclusion"
```

---

## Task 4: Engine skeleton + Provider types + envelope creds

**Files:**
- Create: `internal/secretsync/secretsync.go`, `internal/secretsync/provider.go`, `internal/secretsync/errors.go`
- Test: `internal/secretsync/secretsync_test.go`

- [ ] **Step 1: errors** — `internal/secretsync/errors.go`:

```go
package secretsync

import "errors"

var (
	ErrNotFound      = errors.New("sync: target not found")
	ErrExists        = errors.New("sync: target already exists for this config/provider/destination")
	ErrSealed        = errors.New("sync: server is sealed")
	ErrInvalidType   = errors.New("sync: unknown provider")
	ErrInvalidConfig = errors.New("sync: invalid target configuration")
	ErrApplyFailed   = errors.New("sync: provider apply failed")
)
```

- [ ] **Step 2: provider types** — `internal/secretsync/provider.go`:

```go
package secretsync

import "context"

const (
	ProviderGitHub = "github"
	ProviderK8s    = "k8s"
)

// Creds is the decrypted provider credential blob (never logged/persisted clear).
type Creds struct {
	// github
	PAT string `json:"pat,omitempty"`
	// k8s
	APIURL string `json:"api_url,omitempty"`
	CACert string `json:"ca_cert,omitempty"`
	Token  string `json:"token,omitempty"`
}

// Addr is the non-secret destination coordinates (stored as jsonb).
type Addr struct {
	// github
	Owner       string `json:"owner,omitempty"`
	Repo        string `json:"repo,omitempty"`
	Environment string `json:"environment,omitempty"`
	// k8s
	Namespace  string `json:"namespace,omitempty"`
	SecretName string `json:"secret_name,omitempty"`
}

// ApplyResult reports what a provider did.
type ApplyResult struct {
	Applied []string          // keys written to the target
	Skipped map[string]string // key -> value-free reason
}

// Provider applies a desired key/value map to one external destination.
// managedKeys is the set pushed on the previous successful sync (drives prune).
type Provider interface {
	Apply(ctx context.Context, creds Creds, addr Addr, desired map[string]string,
		managedKeys []string, prune bool) (ApplyResult, error)
	Name() string
}
```

- [ ] **Step 3: Service + envelope creds** — `internal/secretsync/secretsync.go`. Mirror `internal/rotation/rotation.go`'s Service/New/`keyring` interface/`sealBlob`/`openBlob`/`unwrapProjectKEK`/`zero`/`mustJSON`/`mapStoreErr`, renamed. The engine additionally holds a `*secrets.Service` (for the resolver) and a `*resolve`-based resolver built per-reconcile. Concretely:

```go
// Package secretsync is Janus's outbound sync engine: scheduled one-way
// replication of a config's resolved secrets to external stores (GitHub Actions
// secrets, Kubernetes Secrets).
package secretsync

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
	failureThreshold = 5
	defaultBatch     = 50
)

type keyring interface {
	UnwrapProjectKEK(ct crypto.Ciphertext, projectID string) ([]byte, error)
	NewDEK(projectKEK, aad []byte) ([]byte, crypto.Ciphertext, error)
	SyncFingerprint(data []byte) []byte
	Sealed() bool
}

type Service struct {
	kr       keyring
	repo     *store.SyncTargetRepo
	projects *store.ProjectRepo
	secrets  *secrets.Service
	audit    *audit.Recorder
	logger   *slog.Logger
	st       *store.Store
	hc       *http.Client
	now      func() time.Time
}

func New(kr *crypto.Keyring, st *store.Store, sec *secrets.Service, aud *audit.Recorder, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		kr: kr, repo: store.NewSyncTargetRepo(st), projects: store.NewProjectRepo(st),
		secrets: sec, audit: aud, logger: logger, st: st,
		hc:  &http.Client{Timeout: 20 * time.Second},
		now: time.Now,
	}
}
```

Add, mirroring rotation exactly (copy the bodies, rename `RotationConfigAAD`→`SyncCredsAAD`, drop the pending variants): `zero`, `sealCreds(proj, targetID, Creds) (ct,nonce,wrapped []byte, kekVer int, err error)` (JSON-marshal Creds, `sealBlob` with `crypto.SyncCredsAAD(targetID)`, wipe plaintext after Encrypt — mirror rotation's hardened `sealBlob`), `openCreds(proj, *store.SyncTarget) (Creds, error)`, `sealBlob`, `openBlob`, `unwrapProjectKEK` (returns `ErrSealed` when sealed), `mustJSON`, `mapStoreErr` (maps `store.ErrNotFound`→`ErrNotFound`, `store.ErrAlreadyExists`→`ErrExists`).

- [ ] **Step 4: creds round-trip test** — `internal/secretsync/secretsync_test.go`: mirror `internal/rotation/rotation_test.go`'s harness (`TestMain` booting testcontainers Postgres + `testStore` + `testDSN`; real unsealed keyring; `secrets.NewService` to make a project/env/config). `TestCredsRoundTrip`: seal a `Creds{PAT:"ghp_secret"}`, persist a target via `store.SyncTargetRepo.Create`, `Get`, `openCreds` returns the same; tamper check that decrypting under a different AAD fails.

- [ ] **Step 5: Run** `go test ./internal/secretsync/ -run TestCredsRoundTrip` → PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/secretsync/secretsync.go internal/secretsync/provider.go internal/secretsync/errors.go internal/secretsync/secretsync_test.go
git commit -m "feat(secretsync): engine skeleton + provider interface + envelope creds"
```

---

## Task 5: GitHub provider

**Files:**
- Create: `internal/secretsync/github_provider.go`
- Test: `internal/secretsync/github_provider_test.go`

- [ ] **Step 1: implement** — `internal/secretsync/github_provider.go`:

```go
package secretsync

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"

	"golang.org/x/crypto/nacl/box"
)

// ghSecretNameRe is GitHub's Actions-secret name rule: letters/digits/underscore,
// not starting with a digit. (GitHub also reserves the GITHUB_ prefix.)
var ghSecretNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validGitHubSecretName(k string) bool {
	return ghSecretNameRe.MatchString(k) && len(k) <= 100 &&
		!hasPrefixFold(k, "GITHUB_")
}

func hasPrefixFold(s, p string) bool {
	if len(s) < len(p) {
		return false
	}
	for i := 0; i < len(p); i++ {
		a, b := s[i], p[i]
		if 'a' <= a && a <= 'z' {
			a -= 32
		}
		if 'a' <= b && b <= 'z' {
			b -= 32
		}
		if a != b {
			return false
		}
	}
	return true
}

type githubProvider struct {
	hc      *http.Client
	baseURL string // "https://api.github.com" in prod; overridden by tests
}

func (githubProvider) Name() string { return ProviderGitHub }

// secretsPath returns the repo- or environment-scoped secrets base path.
func (g githubProvider) secretsPath(a Addr) string {
	if a.Environment != "" {
		return fmt.Sprintf("/repos/%s/%s/environments/%s/secrets", a.Owner, a.Repo, a.Environment)
	}
	return fmt.Sprintf("/repos/%s/%s/actions/secrets", a.Owner, a.Repo)
}

type ghPublicKey struct {
	KeyID string `json:"key_id"`
	Key   string `json:"key"` // base64 32-byte NaCl public key
}

func (g githubProvider) Apply(ctx context.Context, creds Creds, addr Addr, desired map[string]string,
	managedKeys []string, prune bool) (ApplyResult, error) {
	if creds.PAT == "" || addr.Owner == "" || addr.Repo == "" {
		return ApplyResult{}, ErrInvalidConfig
	}
	base := g.baseURL + g.secretsPath(addr)

	// 1. fetch the public key
	pk, err := g.publicKey(ctx, creds.PAT, base)
	if err != nil {
		return ApplyResult{}, err
	}
	recipient, err := decodeKey(pk.Key)
	if err != nil {
		return ApplyResult{}, ErrApplyFailed
	}

	res := ApplyResult{Skipped: map[string]string{}}
	for name, val := range desired {
		if !validGitHubSecretName(name) {
			res.Skipped[name] = "invalid github secret name"
			continue
		}
		enc, err := sealBox(recipient, []byte(val))
		if err != nil {
			return res, ErrApplyFailed
		}
		if err := g.putSecret(ctx, creds.PAT, base, name, enc, pk.KeyID); err != nil {
			return res, err
		}
		res.Applied = append(res.Applied, name)
	}

	// 2. prune managed keys no longer desired
	if prune {
		desiredSet := map[string]bool{}
		for _, k := range res.Applied {
			desiredSet[k] = true
		}
		for _, k := range managedKeys {
			if !desiredSet[k] && validGitHubSecretName(k) {
				if err := g.deleteSecret(ctx, creds.PAT, base, k); err != nil {
					return res, err
				}
			}
		}
	}
	return res, nil
}

// sealBox encrypts value as a libsodium sealed box under recipient (GitHub's format).
func sealBox(recipient *[32]byte, value []byte) (string, error) {
	sealed, err := box.SealAnonymous(nil, value, recipient, rand.Reader)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func decodeKey(b64 string) (*[32]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != 32 {
		return nil, fmt.Errorf("bad key")
	}
	var k [32]byte
	copy(k[:], raw)
	return &k, nil
}

func (g githubProvider) publicKey(ctx context.Context, pat, base string) (ghPublicKey, error) {
	var pk ghPublicKey
	if err := g.doJSON(ctx, http.MethodGet, pat, base+"/public-key", nil, &pk); err != nil {
		return ghPublicKey{}, err
	}
	return pk, nil
}

func (g githubProvider) putSecret(ctx context.Context, pat, base, name, encVal, keyID string) error {
	body, _ := json.Marshal(map[string]string{"encrypted_value": encVal, "key_id": keyID})
	return g.doJSON(ctx, http.MethodPut, pat, base+"/"+name, body, nil)
}

func (g githubProvider) deleteSecret(ctx context.Context, pat, base, name string) error {
	return g.doJSON(ctx, http.MethodDelete, pat, base+"/"+name, nil, nil)
}

// doJSON performs an authenticated GitHub API call. Errors carry only the status
// code — never the PAT, body, or secret value.
func (g githubProvider) doJSON(ctx context.Context, method, pat, url string, body []byte, out any) error {
	var r *bytes.Reader
	if body != nil {
		r = bytes.NewReader(body)
	} else {
		r = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+pat)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: request error", ErrApplyFailed)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: github status %d", ErrApplyFailed, resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
```

- [ ] **Step 2: test** — `internal/secretsync/github_provider_test.go`. Use `httptest.NewServer` to emulate GitHub. The provider's `baseURL` points at the test server. Cover:
  - `TestGitHubApplySealsAndPuts`: server returns a real NaCl public key (generate a keypair with `box.GenerateKey`; base64 its public key for `/public-key`); capture the `PUT` bodies; after `Apply`, decrypt each `encrypted_value` with `box.OpenAnonymous(nil, sealed, pub, priv)` and assert it equals the original value (proves sealed-box correctness end-to-end); assert `key_id` echoed.
  - `TestGitHubSkipsInvalidNames`: desired includes `bad-name` and `github_x` → both in `ApplyResult.Skipped`, valid ones Applied.
  - `TestGitHubPrunesManagedKeys`: `managedKeys=["OLD","KEEP"]`, desired has `KEEP` only, `prune=true` → server sees `DELETE .../OLD` and no delete of `KEEP`.
  - `TestGitHubPruneFalseNoDelete`: `prune=false` → no DELETE calls.
  - `TestGitHubEnvironmentPath`: `Addr.Environment` set → requests hit the `/environments/{env}/secrets` path.

- [ ] **Step 3: Run** `go test ./internal/secretsync/ -run TestGitHub` → PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/secretsync/github_provider.go internal/secretsync/github_provider_test.go
git commit -m "feat(secretsync): github Actions provider (nacl sealed-box + raw REST)"
```

---

## Task 6: Kubernetes provider

**Files:**
- Create: `internal/secretsync/k8s_provider.go`
- Test: `internal/secretsync/k8s_provider_test.go`

- [ ] **Step 1: implement** — `internal/secretsync/k8s_provider.go`:

```go
package secretsync

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
)

type k8sProvider struct {
	// newClient builds an HTTP client that trusts caPEM (overridable in tests).
	newClient func(caPEM string) (*http.Client, error)
}

func (k8sProvider) Name() string { return ProviderK8s }

func defaultK8sClient(caPEM string) (*http.Client, error) {
	pool := x509.NewCertPool()
	if caPEM != "" {
		if !pool.AppendCertsFromPEM([]byte(caPEM)) {
			return nil, ErrInvalidConfig
		}
	}
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
	}}, nil
}

func (p k8sProvider) client(caPEM string) (*http.Client, error) {
	if p.newClient != nil {
		return p.newClient(caPEM)
	}
	return defaultK8sClient(caPEM)
}

func (p k8sProvider) Apply(ctx context.Context, creds Creds, addr Addr, desired map[string]string,
	managedKeys []string, prune bool) (ApplyResult, error) {
	if creds.APIURL == "" || creds.Token == "" || addr.Namespace == "" || addr.SecretName == "" {
		return ApplyResult{}, ErrInvalidConfig
	}
	hc, err := p.client(creds.CACert)
	if err != nil {
		return ApplyResult{}, err
	}

	data := make(map[string]string, len(desired))
	applied := make([]string, 0, len(desired))
	for k, v := range desired {
		data[k] = base64.StdEncoding.EncodeToString([]byte(v))
		applied = append(applied, k)
	}
	obj := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   map[string]any{"name": addr.SecretName, "namespace": addr.Namespace},
		"type":       "Opaque",
		"data":       data,
	}
	body, _ := json.Marshal(obj)

	url := fmt.Sprintf("%s/api/v1/namespaces/%s/secrets/%s", creds.APIURL, addr.Namespace, addr.SecretName)
	// Server-side apply: fieldManager=janus gives per-key ownership; force resolves
	// conflicts on keys Janus owns. prune is implicit under SSA (dropping a
	// previously-owned key removes it). prune=false → non-pruning merge patch.
	contentType := "application/apply-patch+yaml"
	if prune {
		url += "?fieldManager=janus&force=true"
	} else {
		contentType = "application/merge-patch+json"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(body))
	if err != nil {
		return ApplyResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+creds.Token)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("%w: request error", ErrApplyFailed)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ApplyResult{}, fmt.Errorf("%w: k8s status %d", ErrApplyFailed, resp.StatusCode)
	}
	_ = managedKeys // SSA handles prune server-side; managedKeys unused for k8s
	return ApplyResult{Applied: applied, Skipped: map[string]string{}}, nil
}
```

> Note: JSON is valid YAML, so a JSON body is accepted for `application/apply-patch+yaml`. Keep the body as marshaled JSON.

- [ ] **Step 2: test** — `internal/secretsync/k8s_provider_test.go`. Use `httptest.NewTLSServer` (gives a self-signed cert); pass the server's CA to `Creds.CACert` by extracting `srv.Certificate()` PEM (or set `newClient` to a client that trusts `srv.Client()`'s transport — simplest: set `p.newClient = func(string)(*http.Client,error){ return srv.Client(), nil }` so TLS trust is handled, and separately unit-test `defaultK8sClient` rejects a bad CA). Cover:
  - `TestK8sApplyServerSideApply`: capture the PATCH request; assert method `PATCH`, path `/api/v1/namespaces/{ns}/secrets/{name}`, `?fieldManager=janus&force=true`, `Content-Type: application/apply-patch+yaml`, `Authorization: Bearer <token>`, and a body whose `data` map holds base64 of the desired values; respond 200. Assert `ApplyResult.Applied` lists the keys.
  - `TestK8sPruneFalseMergePatch`: `prune=false` → `Content-Type: application/merge-patch+json`, no `fieldManager` query.
  - `TestK8sBadCACertRejected`: `defaultK8sClient("not a pem")` → `ErrInvalidConfig`.
  - `TestK8sMissingConfig`: empty api_url/token/namespace/secret_name → `ErrInvalidConfig`.

- [ ] **Step 3: Run** `go test ./internal/secretsync/ -run TestK8s` → PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/secretsync/k8s_provider.go internal/secretsync/k8s_provider_test.go
git commit -m "feat(secretsync): kubernetes Secret provider (server-side apply, verified TLS)"
```

---

## Task 7: Reconcile + fingerprint + resolver + backoff

**Files:**
- Create: `internal/secretsync/reconcile.go`
- Test: `internal/secretsync/reconcile_test.go`, `internal/secretsync/backoff_test.go`

- [ ] **Step 1: backoff (TDD)** — identical to rotation's. `internal/secretsync/backoff_test.go` mirrors `internal/rotation/backoff_test.go`. In `reconcile.go` add the same `backoff(failureCount int) time.Duration` (base 1m, doubling, cap 1h) + `backoffBase`/`backoffCap` consts.

- [ ] **Step 2: reconcile core** — `internal/secretsync/reconcile.go`:

```go
package secretsync

import (
	"context"
	"encoding/binary"
	"errors"
	"sort"
	"time"

	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/resolve"
	"github.com/steveokay/janus-secrets/internal/store"
)

const (
	backoffBase = 1 * time.Minute
	backoffCap  = 1 * time.Hour
)

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

// projectAuthorizer implements resolve.Authorizer for system-driven sync: a
// reference is followed only if its target config is in the SAME project as the
// sync target. This prevents a project admin from exfiltrating another project's
// secrets by syncing a config that references across projects.
type projectAuthorizer struct{ projectID string }

func (a projectAuthorizer) CanReadSecrets(_ context.Context, t resolve.RawConfig) error {
	if t.ProjectID != a.projectID {
		return resolve.ErrForbiddenReference
	}
	return nil
}

func (s *Service) providerFor(name string) (Provider, error) {
	switch name {
	case ProviderGitHub:
		return githubProvider{hc: s.hc, baseURL: "https://api.github.com"}, nil
	case ProviderK8s:
		return k8sProvider{}, nil
	default:
		return nil, ErrInvalidType
	}
}

// resolveDesired returns the config's resolved key/value map (references
// expanded), authorized to the target's own project.
func (s *Service) resolveDesired(ctx context.Context, t *store.SyncTarget) (map[string]string, error) {
	r := resolve.New(s.secrets, projectAuthorizer{projectID: t.ProjectID})
	raw, _, err := r.Resolve(ctx, t.ConfigID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		out[k] = string(v)
	}
	return out, nil
}

// fingerprint canonically serializes the desired map (sorted, length-prefixed)
// and returns the keyed HMAC via the keyring (nil while sealed).
func (s *Service) fingerprint(desired map[string]string) []byte {
	keys := make([]string, 0, len(desired))
	for k := range desired {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf []byte
	for _, k := range keys {
		buf = appendField(buf, k)
		buf = appendField(buf, desired[k])
	}
	return s.kr.SyncFingerprint(buf)
}

func appendField(b []byte, f string) []byte {
	b = binary.BigEndian.AppendUint64(b, uint64(len(f)))
	return append(b, f...)
}

// reconcile syncs one target. force skips change-detection (manual sync-now).
func (s *Service) reconcile(ctx context.Context, t *store.SyncTarget, force bool) error {
	proj, err := s.projects.Get(ctx, t.ProjectID)
	if err != nil {
		return mapStoreErr(err)
	}
	creds, err := s.openCreds(proj, t) // ErrSealed while sealed
	if err != nil {
		return err
	}
	desired, err := s.resolveDesired(ctx, t)
	if err != nil {
		return err
	}
	fp := s.fingerprint(desired)
	if fp == nil {
		return ErrSealed
	}
	if !force && t.SyncedFingerprint != nil && bytesEqual(fp, t.SyncedFingerprint) {
		return nil // unchanged — skip, no external calls
	}

	prov, err := s.providerFor(t.Provider)
	if err != nil {
		return err
	}
	var addr Addr
	if err := jsonUnmarshal(t.Addr, &addr); err != nil {
		return ErrInvalidConfig
	}
	res, err := prov.Apply(ctx, creds, addr, desired, t.ManagedKeys, t.Prune)
	if err != nil {
		return err
	}
	next := s.now().Add(time.Duration(t.IntervalSeconds) * time.Second)
	cv := 0
	if t.SyncedConfigVersion != nil {
		cv = *t.SyncedConfigVersion // best-effort; the exact version is not load-bearing
	}
	if err := s.repo.MarkSynced(ctx, t.ID, res.Applied, fp, cv, next); err != nil {
		return mapStoreErr(err)
	}
	if len(res.Skipped) > 0 {
		s.logger.Warn("sync skipped keys", "target", t.ID, "skipped_count", len(res.Skipped))
	}
	return nil
}

// attempt reconciles and records audit + failure bookkeeping. Single entry
// point for scheduler and manual sync-now. Sealed is not a failure.
func (s *Service) attempt(ctx context.Context, t *store.SyncTarget, force bool) error {
	err := s.reconcile(ctx, t, force)
	if err != nil {
		if errors.Is(err, ErrSealed) {
			s.logger.Debug("sync skipped: server sealed", "target", t.ID)
			return err
		}
		next := s.now().Add(backoff(t.FailureCount + 1))
		if merr := s.repo.MarkFailure(ctx, t.ID, sanitize(err), next, failureThreshold); merr != nil {
			s.logger.Warn("sync mark-failure failed", "target", t.ID, "err", merr)
		}
		s.recordSync(ctx, t, "failure", sanitize(err))
		return err
	}
	s.recordSync(ctx, t, "success", "")
	return nil
}

func sanitize(err error) string {
	switch {
	case errors.Is(err, ErrSealed):
		return "sealed"
	case errors.Is(err, ErrApplyFailed):
		return "apply failed"
	case errors.Is(err, ErrInvalidConfig):
		return "invalid config"
	case errors.Is(err, resolve.ErrForbiddenReference):
		return "forbidden reference"
	default:
		return "sync error"
	}
}

func (s *Service) recordSync(ctx context.Context, t *store.SyncTarget, result, detail string) {
	if s.audit == nil {
		return
	}
	if err := s.audit.Record(ctx, audit.Event{
		Actor:    audit.Actor{Kind: "system", Name: "sync:" + t.ID},
		Action:   "sync.reconcile",
		Resource: "configs/" + t.ConfigID + " → " + t.Provider,
		Detail:   detail,
		Result:   result,
	}); err != nil {
		s.logger.Warn("sync audit write failed", "target", t.ID, "err", err)
	}
}
```

Add small helpers `bytesEqual` (use `bytes.Equal`, import `bytes`) and `jsonUnmarshal` (use `encoding/json`.Unmarshal, import `encoding/json`) — or inline `bytes.Equal`/`json.Unmarshal` and add the imports. Confirm `resolve.RawConfig` has a `ProjectID` field and `resolve.ErrForbiddenReference` exists (they do — see `internal/api/resolve_adapter.go`).

- [ ] **Step 3: tests** — `internal/secretsync/reconcile_test.go` (real Postgres + keyring + seeded config/secrets; a fake provider via an httptest GitHub server, or a test-only in-package `Provider` injected by making `providerFor` overridable — simplest: use the github provider pointed at an httptest server by adding a test seam `s.githubBaseURL`; OR test reconcile through `attempt` with a webhook-like fake. Prefer: seed a github target whose creds/addr point at an httptest GitHub fake, drive `attempt`). Cover:
  - `TestReconcileSyncsResolvedSecrets`: config with two keys → fake GitHub receives both (sealed) → target `MarkSynced` (managed_keys set, fingerprint set, status active).
  - `TestReconcileSkipsWhenUnchanged`: run reconcile twice; the second run makes NO external calls (fake server hit-count unchanged) because the fingerprint matches. A `force=true` run DOES call again.
  - `TestReconcileForbidsCrossProjectReference`: a config referencing another project → `attempt` fails with a sanitized "forbidden reference", `failure_count` bumped.
  - `TestReconcileSealedNoop`: sealed keyring → `attempt` returns `ErrSealed`, no failure bump, no external call.
  - `TestReconcileFailureBackoff`: fake server returns 500 → `failure_count=1`, `status=active`, `last_error="apply failed"`, next_sync advanced ~1m; 5th failure → `failed`.
  - `TestBackoff` (from Step 1).

- [ ] **Step 4: Run** `go test ./internal/secretsync/ -run 'Reconcile|Backoff'` → PASS. Then `go test ./internal/secretsync/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/secretsync/reconcile.go internal/secretsync/reconcile_test.go internal/secretsync/backoff_test.go
git commit -m "feat(secretsync): reconcile with keyed change-detection, project-scoped resolve, backoff"
```

> If a test seam is needed to point the github provider at the httptest server, add an unexported field `githubBaseURL string` on `Service` (defaulting to `https://api.github.com` in `New`), and have `providerFor` use it. Note this seam in your report.

---

## Task 8: Scheduler

**Files:**
- Create: `internal/secretsync/scheduler.go`
- Test: `internal/secretsync/scheduler_test.go`

Mirror `internal/rotation/scheduler.go` exactly, renamed: `RunDue(ctx)` (sealed-skip, `ClaimDue`, `attempt(ctx, t, false)` per target, abort on `ctx.Err()`), `RunScheduler(ctx, tick)` (tick<=0 disables; ticker; stops on ctx.Done). Tests mirror `internal/rotation/scheduler_test.go`: `TestRunDueSyncsDueTargets` (one due, one future — only due synced), `TestRunDueSealedNoop`. Commit:

```bash
git add internal/secretsync/scheduler.go internal/secretsync/scheduler_test.go
git commit -m "feat(secretsync): scheduler (RunDue/RunScheduler)"
```

---

## Task 9: Engine CRUD + SyncNow + masked views

**Files:**
- Modify/Create: `internal/secretsync/crud.go`
- Test: `internal/secretsync/crud_test.go`

Mirror `internal/rotation/rotation.go`'s CRUD section (the `PolicyInput`/`PolicyView`/`view`/`projectForConfig`/`Create`/`Get`/`ListByProject`/`Update`/`Delete`/`RotateNow` block). Adapt to sync:

- `TargetInput{ConfigID, Provider string; Prune bool; IntervalSeconds int64; Addr Addr; Creds Creds}`.
- `TargetView{ID, ProjectID, ConfigID, Provider string; Prune bool; IntervalSeconds int64; Status string; FailureCount int; LastError *string; NextSyncAt time.Time; LastSyncedAt *time.Time; ManagedKeys []string; CreatedAt time.Time}` — **masked**: no Creds/Addr-secret/fingerprint. (Addr is non-secret coordinates — include it as a struct; it holds no credentials.)
- `Create(ctx, TargetInput, createdBy)`: validate provider ∈ {github,k8s}; validate per-provider addr+creds minimally (github: Owner/Repo/PAT non-empty; k8s: APIURL/Token/Namespace/SecretName non-empty); resolve project from config; seal creds; marshal addr to jsonb bytes; insert.
- `Get`/`ListByProject` → masked view. `Update(ctx, id, intervalSeconds *int64, prune *bool, status *string, creds *Creds, addr *Addr)` (re-seal creds if provided; re-marshal addr if provided; reject status other than active/paused). `Delete`.
- `SyncNow(ctx, id) error` (mirror `RotateNow`): Get → sealed-check → `repo.PrepareSyncNow(ctx, id, s.now())` → reload → `attempt(ctx, t, true)` (force=true). Return nil on success (there is no single "version" to return; the caller reports success).

Tests (`crud_test.go`) mirror rotation's: create github + k8s, validation rejects, duplicate destination → `ErrExists`, Get/List, Update, SyncNow clears a failed target, and a `TargetView` leak-guard test asserting no field equals the PAT/token.

Run `go test ./internal/secretsync/` → PASS. Commit:

```bash
git add internal/secretsync/crud.go internal/secretsync/crud_test.go
git commit -m "feat(secretsync): engine CRUD + SyncNow with masked views"
```

---

## Task 10: RBAC + error code + server/boot wiring (NO route group)

**Files:** Modify `internal/authz/actions.go`, `internal/api/errors.go`, `internal/api/server.go`, `internal/api/boot.go`, `cmd/janus/server.go`

Mirror rotation Task 10 (which is now in `main` — see commit history / the current files). Specifics:
- `internal/authz/actions.go`: add `SyncManage Action = "sync:manage" // project-scoped` and include it in `adminActions`.
- `internal/api/errors.go`: add `CodeSyncNotFound = "sync_not_found"`.
- `internal/api/server.go`: import `internal/secretsync`; add field `sync *secretsync.Service // nil in unit-test servers`; add `syncSvc *secretsync.Service,` param to `New(...)` immediately AFTER the `rot *rotation.Service,` param; set `sync: syncSvc,` in the struct literal. **Do NOT add routes here.** Update EVERY `New(...)` caller (grep `New(Config{` / `api.New(` in `internal/api/*_test.go`) to pass an extra `nil` in the new position (right after the rotation arg).
- `internal/api/boot.go`: add `SyncTick time.Duration` to `BootConfig`; after `rotationSvc := rotation.New(...)`, add `syncSvc := secretsync.New(kr, st, svc, auditRec, logger)`; pass `syncSvc` to `New(...)` after `rotationSvc`; after the rotation scheduler start, add `if bc.SyncTick > 0 { go syncSvc.RunScheduler(ctx, bc.SyncTick) }`. Import `internal/secretsync`.
- `cmd/janus/server.go`: parse `JANUS_SYNC_TICK` (mirror `JANUS_ROTATION_TICK`, default 60s, 0 disables) → `SyncTick` in the `BootConfig` literal.

Verify: `go build ./... && go vet ./...` and compile all tests (`go test ./... -run zzz_no_match 2>&1 | grep -v "no test files"`). Commit:

```bash
git add internal/authz/actions.go internal/api/errors.go internal/api/server.go internal/api/boot.go cmd/janus/server.go internal/api/*_test.go
git commit -m "feat(api): sync RBAC action, engine wiring, scheduler start (no routes yet)"
```

---

## Task 11: REST handlers + route group + e2e

**Files:** Create `internal/api/sync_handlers.go`; modify `internal/api/server.go` (route group); test `internal/api/sync_e2e_test.go`

Mirror `internal/api/rotation_handlers.go` exactly, renamed for sync. Endpoints (added as an `if s.sync != nil { r.Group(...) }` block right after the rotation group):
- `POST /v1/sync/targets` `handleSyncCreate`
- `GET /v1/sync/targets` `handleSyncList` (requires `project_id`)
- `GET /v1/sync/targets/{id}` `handleSyncGet`
- `PATCH /v1/sync/targets/{id}` `handleSyncUpdate`
- `DELETE /v1/sync/targets/{id}` `handleSyncDelete`
- `POST /v1/sync/targets/{id}/sync` `handleSyncNow`

Request/view types: `createSyncReq{ConfigID, Provider string; Prune *bool; IntervalSeconds int64; Addr syncAddrReq; Creds syncCredsReq}` where `syncCredsReq{PAT, APIURL, CACert, Token string}` and `syncAddrReq{Owner, Repo, Environment, Namespace, SecretName string}` mapping to `secretsync.Creds`/`secretsync.Addr`. `syncView` is the masked JSON projection of `secretsync.TargetView` (RFC3339 timestamps, `managed_keys` names, NO creds). `writeSyncErr` maps `secretsync.ErrNotFound`→404 `sync_not_found`, `ErrExists`→409 `"conflict"`, `ErrInvalidType`/`ErrInvalidConfig`→400 `validation`, `ErrSealed`→503 `sealed`. Project-scoped authz via `resolveScopeResource(ctx,"config", configID)` + `s.authorize(..., authz.SyncManage, ...)` on create; `syncResource(r)` (load target → resolve its config's project) for {id} routes; list authorizes on the `project_id` query. Create/update/delete write `s.record(...)` audit (fail-closed); sync-now relies on the engine's `sync.reconcile` audit event (do not double-audit). Use `principalName(r)` (already exists from rotation) for `created_by`.

e2e (`sync_e2e_test.go`) mirrors `rotation_e2e_test.go` using `authStackFull`: create a github target (Creds/Addr point at an httptest GitHub fake), 201 masked (assert body has NO PAT/token), list/get/patch, `POST .../sync` → 200, delete → 200, get → 404. Forbidden-for-non-admin (developer role → 403). Masking test asserts the raw response never contains the PAT.

Run `go test ./internal/api/ -run TestSync` → PASS; `go build ./... && go vet ./internal/api/`. Commit:

```bash
git add internal/api/sync_handlers.go internal/api/server.go internal/api/sync_e2e_test.go
git commit -m "feat(api): sync REST handlers (masked, project-scoped, audited)"
```

---

## Task 12: CLI

**Files:** Create `cmd/janus/sync_commands.go`; register in `cmd/janus/main.go`; test `cmd/janus/sync_commands_test.go`

Mirror `cmd/janus/rotation_commands.go`. `newSyncCmd()` with subcommands `create|list|get|update|delete|sync`, wired to `/v1/sync/targets`. `create` flags: `--config`, `--provider`, `--prune` (bool, default true), `--interval-seconds`, github: `--owner --repo --environment --pat`, k8s: `--api-url --ca-cert --token --namespace --secret-name`. `sync <id>` POSTs `/v1/sync/targets/{id}/sync` and prints success. Register `newSyncCmd()` in `main.go`'s `root.AddCommand(...)` after `newRotationCmd()`. Structural test mirrors `rotation_commands_test.go` (asserts subcommands + create flags).

Run `go test ./cmd/janus/ -run Sync`; `go build ./...`. Commit:

```bash
git add cmd/janus/sync_commands.go cmd/janus/sync_commands_test.go cmd/janus/main.go
git commit -m "feat(cli): janus sync create/list/get/update/delete/sync"
```

---

## Task 13: Docs

**Files:** Create `docs/ops/sync.md`; modify `docs/operations.md`

Read `docs/ops/rotation.md` for tone/structure. `docs/ops/sync.md` covers: overview (one-way replication of resolved secrets; declarative full-mirror with prune); the two providers and their credential setup + **least privilege** (github: fine-grained PAT with Actions-secrets write on the target repo only; k8s: a service account with `create`/`patch` on Secrets in one namespace, plus the cluster CA); **prune semantics** (only Janus-managed keys are pruned — the manifest; per-target `prune` toggle); **github key-name constraint** (uppercase/underscore, non-conforming keys skipped and reported); **change detection** (unchanged configs are skipped; `janus sync <id>` forces); **cross-project references are refused** during sync (security); sealed behavior (paused, not a failure); failure/backoff (1m→1h, `failed` after 5); `JANUS_SYNC_TICK` env var (default 60s, 0 disables); CLI examples for both providers; RBAC (`sync:manage`) + audit (`sync.reconcile`, masked responses); backup/restore note (`sync_targets` travel with the dump). Add `janus sync …` CLI rows + a `JANUS_SYNC_TICK` env row to `docs/operations.md`, and a "Sync integrations" section linking `docs/ops/sync.md` (mirror how the rotation section links its runbook).

Commit:

```bash
git add docs/ops/sync.md docs/operations.md
git commit -m "docs(sync): runbook + operations references"
```

---

## Task 14: Full-suite gates + PR

- [ ] Run: `go build ./...`, `go vet ./...`, `go test ./...`, `gosec -exclude-dir=internal/crypto/shamir ./...`, `govulncheck ./...` — all green. If gosec flags the k8s/github dynamic URL construction, confirm inputs are validated (owner/repo/namespace/name from validated addr) and annotate `// #nosec` only with a written rationale.
- [ ] Leak check: confirm `internal/secretsync` + api e2e assert no PAT / k8s token / CA / secret value in captured logs across a sync run.
- [ ] Push + PR:

```bash
git push -u origin phase3-sync-integrations
gh pr create --title "feat: sync integrations (Phase 3.2)" --body "<summary + spec link>"
```

---

## Self-Review (author checklist — completed)

**Spec coverage:**
- §1 boundaries → engine skeleton (T4). ✓
- §2 provider interface + github + k8s mechanics → T4 (interface), T5 (github sealed-box + prune + name-skip + env path), T6 (k8s SSA + merge-patch + CA TLS). ✓
- §3 data model → migration (T2), repo (T3), backup inclusion (T3). ✓
- §4 reconcile + fingerprint change-detection + prune=false → T7 (reconcile, fingerprint, force), providers honor prune (T5/T6). ✓
- §5 scheduler + failure/backoff + sealed-not-failure + sync-now recovery → T7 (attempt/backoff/sealed), T8 (scheduler), T9 (SyncNow via PrepareSyncNow). ✓
- §6 crypto (SyncCredsAAD, keyed fingerprint, no new deps, verified TLS, zero leak) → T1, T4 (creds envelope), T6 (CA verify), T7 (sanitize). ✓
- §7 API/CLI/RBAC (masked, project-scoped, sync:manage) → T10 (RBAC/wiring), T11 (handlers/e2e), T12 (CLI). ✓
- §8 testing (unit + httptest fakes + testcontainers + leak) → per-task tests + T14. ✓

**Type consistency:** `Provider.Apply(ctx, Creds, Addr, desired map[string]string, managedKeys []string, prune bool) (ApplyResult, error)` is identical across `provider.go`, `github_provider.go`, `k8s_provider.go`, and `reconcile.go`'s `providerFor`. `secretsync.Creds`/`Addr`/`ApplyResult` are the single definitions (provider.go). Store methods `ClaimDue`/`MarkSynced`/`MarkFailure`/`PrepareSyncNow` used consistently in reconcile/scheduler/crud. `New(...)` param order (rotation then sync) matches server.go and boot.go. Engine field `sync *secretsync.Service` on Server.

**Reconciliations flagged for implementers (grep-and-adjust, not placeholders):** keyring master field name + locking in `SyncFingerprint` (verify in keyring.go); `resolve.RawConfig.ProjectID` + `resolve.ErrForbiddenReference` exist (confirmed via resolve_adapter.go); the github test seam (`githubBaseURL` on Service) if reconcile tests need to point at an httptest fake; unit-test `api.New(...)` call sites need the extra `nil`.
