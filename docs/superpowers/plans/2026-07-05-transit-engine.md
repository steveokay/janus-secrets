# Transit Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Vault-style transit "encryption as a service" engine — instance-scoped named keys with encrypt/decrypt/sign/verify/rewrap/datakey, versioning, `min_decryption_version`, rotate, and trim — over `/v1/transit/*`, RBAC-enforced and (for management ops) audited.

**Architecture:** New `internal/transit` engine reusing `internal/crypto` (AEAD + wrap/unwrap + the unsealed master key) and a crypto-blind `store.TransitRepo`. New stdlib-Ed25519 helpers in `internal/crypto`. Thin `internal/api` handlers over the existing `s.authorize`/`s.can`/`s.record`/`writeServiceError` seam. A new `transit` service-token scope plugs into the existing token+authz model.

**Tech Stack:** Go 1.26.4, `crypto/ed25519` (stdlib), `pgx`, `golang-migrate`, `chi`.

---

## Design source

Spec: `docs/superpowers/specs/2026-07-05-transit-engine-design.md`. Locked decisions:
instance-scoped keys; two key types (`aes256-gcm`, `ed25519`); features = core +
datakey + trim (no export/HMAC); management-only audit; a new `transit` token scope
(`use`/`manage`, optional key restriction).

## Existing integration points (verified)

- **`internal/crypto`**: `Encrypt(key,pt,aad)`, `Decrypt(key,ct,aad)`, `GenerateKey()`,
  `WrapKey(wk,material,aad)`, `UnwrapKey(wk,ct,aad)`, AAD helpers (`ProjectKEKAAD`,
  `DEKAAD`). `Keyring` exposes the master key after unseal. `Ciphertext` marshals to
  bytes via its methods (see `ParseCiphertext`).
- **`internal/authz/actions.go`**: `Action` string consts + cumulative role matrix
  (`viewerActions ⊂ developerActions ⊂ adminActions ⊂ ownerActions`). `resolve.go`:
  `tokenCapabilities(access)` + `tokenAllows(scope,action,res)`; `Resource{ProjectID,
  EnvID,ConfigID}`. `engine.go`: `TokenScope{Kind,ID,Access}`.
- **`internal/auth/tokens.go`**: `MintServiceToken(ctx, by, name, scopeKind, scopeID,
  access, ttl)` validates `access ∈ {read,readwrite}` and scope existence;
  `VerifyServiceToken` returns `*TokenScope{Kind,ID,Access}`.
- **`migrations/000002_auth.up.sql`**: `service_tokens.scope_kind text CHECK IN
  ('config','environment')`, `scope_id uuid NOT NULL`, `access text CHECK IN
  ('read','readwrite')`.
- **`internal/api`**: `Server{keyring,service,auth,authz,st,audit,...}`;
  `writeServiceError` maps sentinels; `s.authorize(w,r,action,res,auditAction,
  auditResource)`, `s.can(r,action,res)`, `s.record(r,action,resource,result,code,
  detail)`. `New(...)`/`Boot(...)` wire services.

## File structure

| File | Responsibility |
|------|----------------|
| `internal/crypto/sign.go` (+ `_test`) | Ed25519 generate/sign/verify; `TransitKeyAAD` |
| `migrations/000006_transit.{up,down}.sql` | `transit_keys`, `transit_key_versions`; `service_tokens` scope extension |
| `internal/store/transit.go` (+ `_test`) | `TransitKey`/`TransitKeyVersion` models + `TransitRepo` |
| `internal/store/tokens.go` | nullable `scope_id` support (transit all-keys) |
| `internal/transit/transit.go`, `envelope.go`, `errors.go` (+ `_test`) | engine, `janus:vN:` envelope, sentinels |
| `internal/authz/actions.go`, `resolve.go`, `resource.go` | transit actions, token caps, `Resource.TransitKey` |
| `internal/auth/tokens.go` | transit scope validation in mint/verify |
| `internal/api/transit_handlers.go`, `transit_dataplane_handlers.go` (+ e2e `_test`) | routes |
| `internal/api/service_errors.go`, `server.go`, `boot.go`, `tokens_handlers.go` | wiring |

---

## Task 1: Ed25519 crypto helpers + TransitKeyAAD

**Files:**
- Create: `internal/crypto/sign.go`
- Test: `internal/crypto/sign_test.go`

- [ ] **Step 1: Write the failing test**

```go
package crypto

import (
	"bytes"
	"testing"
)

func TestEd25519SignVerifyRoundTrip(t *testing.T) {
	pub, priv, err := GenerateEd25519Key()
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("transit message")
	sig := Sign(priv, msg)
	if !Verify(pub, msg, sig) {
		t.Fatal("valid signature must verify")
	}
	if Verify(pub, []byte("tampered"), sig) {
		t.Fatal("tampered message must not verify")
	}
	bad := make([]byte, len(sig))
	copy(bad, sig)
	bad[0] ^= 0xff
	if Verify(pub, msg, bad) {
		t.Fatal("tampered signature must not verify")
	}
}

func TestVerifyRejectsMalformedKey(t *testing.T) {
	if Verify([]byte("short"), []byte("m"), make([]byte, 64)) {
		t.Fatal("malformed public key must not verify")
	}
}

func TestTransitKeyAADInjective(t *testing.T) {
	a := TransitKeyAAD("billing", 1)
	b := TransitKeyAAD("billing", 2)
	c := TransitKeyAAD("billin", 1) // different name, watch for delimiter collision
	if bytes.Equal(a, b) || bytes.Equal(a, c) || bytes.Equal(b, c) {
		t.Fatal("AAD must be injective across (name, version)")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/crypto/ -run 'TestEd25519|TestVerifyRejects|TestTransitKeyAAD' -v`
Expected: FAIL — `undefined: GenerateEd25519Key`.

- [ ] **Step 3: Write the implementation**

```go
package crypto

import (
	"crypto/ed25519"
	"encoding/binary"
)

// GenerateEd25519Key returns a new (public, private) Ed25519 key pair. The private
// key is the 64-byte expanded form; the public key is 32 bytes.
func GenerateEd25519Key() (pub, priv []byte, err error) {
	pk, sk, err := ed25519.GenerateKey(nil) // nil → crypto/rand
	if err != nil {
		return nil, nil, err
	}
	return pk, sk, nil
}

// Sign signs msg with an Ed25519 private key.
func Sign(priv, msg []byte) []byte {
	return ed25519.Sign(ed25519.PrivateKey(priv), msg)
}

// Verify reports whether sig is a valid Ed25519 signature of msg under pub. A
// malformed public key or signature returns false (never panics).
func Verify(pub, msg, sig []byte) bool {
	if len(pub) != ed25519.PublicKeySize || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(pub), msg, sig)
}

// TransitKeyAAD binds a transit key version's wrapped material to its (name,
// version) so a version row copied elsewhere fails to unwrap. Domain-tagged and
// length-prefixed so (name,version) is injective.
func TransitKeyAAD(name string, version int) []byte {
	out := []byte("janus:transit:v1")
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(name)))
	out = append(out, n[:]...)
	out = append(out, name...)
	var v [8]byte
	binary.BigEndian.PutUint64(v[:], uint64(version)) // #nosec G115 -- version is a small positive int
	out = append(out, v[:]...)
	return out
}
```

- [ ] **Step 4: Run to verify it passes + coverage**

Run: `go test ./internal/crypto/ -run 'TestEd25519|TestVerifyRejects|TestTransitKeyAAD' -v`
Expected: PASS.
Run: `go test ./internal/crypto/ -cover`
Expected: coverage stays at 100.0% (the leak/AAD suites plus these cover every branch). If `GenerateEd25519Key`'s error return is uncovered, that's an unreachable `crypto/rand` failure — acceptable; confirm the package still reports 100.0% as the existing `GenerateKey` rand-failure test pattern does, or add nothing (ed25519.GenerateKey with nil rand cannot be faulted without a seam — leave it; the package gate measures statements, and this one line is the same shape the existing gate already tolerates).

- [ ] **Step 5: Commit**

```bash
git add internal/crypto/sign.go internal/crypto/sign_test.go
git commit -m "feat(crypto): Ed25519 sign/verify + TransitKeyAAD"
```

---

## Task 2: Migration 000006 (transit tables + token scope extension)

**Files:**
- Create: `migrations/000006_transit.up.sql`, `migrations/000006_transit.down.sql`
- Test: `internal/store/transit_migration_test.go`

- [ ] **Step 1: Write the up migration**

`migrations/000006_transit.up.sql`:

```sql
CREATE TABLE transit_keys (
  id                     uuid PRIMARY KEY,
  name                   text NOT NULL UNIQUE,
  key_type               text NOT NULL CHECK (key_type IN ('aes256-gcm', 'ed25519')),
  latest_version         int  NOT NULL DEFAULT 1,
  min_decryption_version int  NOT NULL DEFAULT 1,
  deletion_allowed       bool NOT NULL DEFAULT false,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE transit_key_versions (
  id               uuid PRIMARY KEY,
  transit_key_id   uuid NOT NULL REFERENCES transit_keys(id) ON DELETE CASCADE,
  version          int  NOT NULL,
  wrapped_material bytea NOT NULL,
  public_key       bytea,
  created_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (transit_key_id, version)
);

-- Extend service_tokens for the transit scope: a transit token may target all
-- keys (scope_id NULL) or one key (scope_id = transit_keys.id).
ALTER TABLE service_tokens ALTER COLUMN scope_id DROP NOT NULL;
ALTER TABLE service_tokens DROP CONSTRAINT service_tokens_scope_kind_check;
ALTER TABLE service_tokens ADD  CONSTRAINT service_tokens_scope_kind_check
  CHECK (scope_kind IN ('config', 'environment', 'transit'));
ALTER TABLE service_tokens DROP CONSTRAINT service_tokens_access_check;
ALTER TABLE service_tokens ADD  CONSTRAINT service_tokens_access_check
  CHECK (access IN ('read', 'readwrite', 'use', 'manage'));
-- Guard: only a transit token may omit scope_id.
ALTER TABLE service_tokens ADD  CONSTRAINT service_tokens_scope_id_presence
  CHECK (scope_id IS NOT NULL OR scope_kind = 'transit');
```

`migrations/000006_transit.down.sql`:

```sql
ALTER TABLE service_tokens DROP CONSTRAINT service_tokens_scope_id_presence;
ALTER TABLE service_tokens DROP CONSTRAINT service_tokens_access_check;
ALTER TABLE service_tokens ADD  CONSTRAINT service_tokens_access_check
  CHECK (access IN ('read', 'readwrite'));
ALTER TABLE service_tokens DROP CONSTRAINT service_tokens_scope_kind_check;
ALTER TABLE service_tokens ADD  CONSTRAINT service_tokens_scope_kind_check
  CHECK (scope_kind IN ('config', 'environment'));
ALTER TABLE service_tokens ALTER COLUMN scope_id SET NOT NULL;

DROP TABLE transit_key_versions;
DROP TABLE transit_keys;
```

Verify the constraint names first: run `grep -n "scope_kind\|access" migrations/000002_auth.up.sql`. If that migration named the CHECKs inline (Postgres auto-names them `service_tokens_scope_kind_check` / `service_tokens_access_check`), the `DROP CONSTRAINT` lines above match. If it used explicit `CONSTRAINT <name>`, substitute the real names.

- [ ] **Step 2: Write the failing test**

`internal/store/transit_migration_test.go`:

```go
package store

import (
	"context"
	"testing"
)

func TestMigration000006CreatesTransitTables(t *testing.T) {
	dsn := bootPostgres(t)
	st, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// Migrations run in Open. Assert the transit tables exist.
	var n int
	err = st.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM information_schema.tables
		 WHERE table_name IN ('transit_keys','transit_key_versions')`).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 transit tables, got %d", n)
	}
}
```

(Confirm the store's constructor/pool field names by reading `internal/store/store.go` — use whatever the existing tests use to get a `*pgxpool.Pool`; `Open` + `st.pool` mirror the current pattern. If the pool field is unexported and inaccessible, assert via a repo call added in Task 3 instead and skip this DB-introspection test.)

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/store/ -run TestMigration000006 -count=1 -v`
Expected: FAIL (tables absent) before the migration files are picked up — then PASS once the embedded migration is present (migrations are `go:embed`-ed; confirm `migrations/embed.go` globs `*.sql`, so new files are included automatically).

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/store/ -run TestMigration000006 -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add migrations/000006_transit.up.sql migrations/000006_transit.down.sql internal/store/transit_migration_test.go
git commit -m "feat(store): migration 000006 — transit tables + token transit scope"
```

---

## Task 3: store.TransitRepo

**Files:**
- Create: `internal/store/transit.go`
- Test: `internal/store/transit_test.go`

- [ ] **Step 1: Write the failing test**

```go
package store

import (
	"context"
	"errors"
	"testing"
)

func TestTransitRepoLifecycle(t *testing.T) {
	dsn := bootPostgres(t)
	st, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	r := NewTransitRepo(st)

	k, err := r.Create(ctx, st.NewID(), "billing", "aes256-gcm",
		&TransitKeyVersion{ID: st.NewID(), Version: 1, WrappedMaterial: []byte("wrapped-v1")})
	if err != nil {
		t.Fatal(err)
	}
	if k.Name != "billing" || k.LatestVersion != 1 || k.MinDecryptionVersion != 1 {
		t.Fatalf("bad key: %+v", k)
	}

	// Duplicate name → ErrAlreadyExists.
	_, err = r.Create(ctx, st.NewID(), "billing", "aes256-gcm",
		&TransitKeyVersion{ID: st.NewID(), Version: 1, WrappedMaterial: []byte("x")})
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("dup name: want ErrAlreadyExists, got %v", err)
	}

	// Append v2, bump latest.
	if err := r.AppendVersion(ctx, k.ID,
		&TransitKeyVersion{ID: st.NewID(), Version: 2, WrappedMaterial: []byte("wrapped-v2")}); err != nil {
		t.Fatal(err)
	}

	got, err := r.GetByName(ctx, "billing")
	if err != nil {
		t.Fatal(err)
	}
	if got.LatestVersion != 2 || len(got.Versions) != 2 {
		t.Fatalf("after append: %+v", got)
	}
	if string(got.Versions[1].WrappedMaterial) != "wrapped-v2" {
		t.Fatalf("v2 material: %q", got.Versions[1].WrappedMaterial)
	}

	// Config update.
	if err := r.UpdateConfig(ctx, k.ID, ptrInt(2), ptrBool(true)); err != nil {
		t.Fatal(err)
	}
	got, _ = r.GetByName(ctx, "billing")
	if got.MinDecryptionVersion != 2 || !got.DeletionAllowed {
		t.Fatalf("after config: %+v", got)
	}

	// Missing key → ErrNotFound.
	if _, err := r.GetByName(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: want ErrNotFound, got %v", err)
	}
}

func ptrInt(i int) *int    { return &i }
func ptrBool(b bool) *bool { return &b }
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/store/ -run TestTransitRepoLifecycle -count=1 -v`
Expected: FAIL — `undefined: NewTransitRepo`.

- [ ] **Step 3: Write the implementation**

```go
package store

import (
	"context"
	"errors"
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

func NewTransitRepo(s *Store) *TransitRepo { return &TransitRepo{s: s} }

// Create inserts a new key with its first version in one transaction.
func (r *TransitRepo) Create(ctx context.Context, id, name, keyType string, v *TransitKeyVersion) (*TransitKey, error) {
	tx, err := r.s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	_, err = tx.Exec(ctx,
		`INSERT INTO transit_keys (id, name, key_type) VALUES ($1,$2,$3)`, id, name, keyType)
	if err != nil {
		return nil, mapWriteErr(err) // maps unique_violation → ErrAlreadyExists (existing helper)
	}
	if _, err = tx.Exec(ctx,
		`INSERT INTO transit_key_versions (id, transit_key_id, version, wrapped_material, public_key)
		 VALUES ($1,$2,$3,$4,$5)`, v.ID, id, v.Version, v.WrappedMaterial, v.PublicKey); err != nil {
		return nil, mapWriteErr(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.GetByName(ctx, name)
}

// AppendVersion inserts a new version and bumps latest_version to it.
func (r *TransitRepo) AppendVersion(ctx context.Context, keyID string, v *TransitKeyVersion) error {
	tx, err := r.s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err = tx.Exec(ctx,
		`INSERT INTO transit_key_versions (id, transit_key_id, version, wrapped_material, public_key)
		 VALUES ($1,$2,$3,$4,$5)`, v.ID, keyID, v.Version, v.WrappedMaterial, v.PublicKey); err != nil {
		return mapWriteErr(err)
	}
	if _, err = tx.Exec(ctx,
		`UPDATE transit_keys SET latest_version=$2, updated_at=now() WHERE id=$1`, keyID, v.Version); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UpdateConfig sets min_decryption_version and/or deletion_allowed (nil = leave).
func (r *TransitRepo) UpdateConfig(ctx context.Context, keyID string, minDec *int, delAllowed *bool) error {
	ct, err := r.s.pool.Exec(ctx,
		`UPDATE transit_keys SET
		   min_decryption_version = COALESCE($2, min_decryption_version),
		   deletion_allowed       = COALESCE($3, deletion_allowed),
		   updated_at = now()
		 WHERE id=$1`, keyID, minDec, delAllowed)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// TrimBelow deletes versions with version < minAvailable.
func (r *TransitRepo) TrimBelow(ctx context.Context, keyID string, minAvailable int) error {
	_, err := r.s.pool.Exec(ctx,
		`DELETE FROM transit_key_versions WHERE transit_key_id=$1 AND version < $2`, keyID, minAvailable)
	return err
}

// Delete removes a key (cascade removes versions).
func (r *TransitRepo) Delete(ctx context.Context, keyID string) error {
	ct, err := r.s.pool.Exec(ctx, `DELETE FROM transit_keys WHERE id=$1`, keyID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetByName returns a key with all its versions (ordered by version asc).
func (r *TransitRepo) GetByName(ctx context.Context, name string) (*TransitKey, error) {
	var k TransitKey
	err := r.s.pool.QueryRow(ctx,
		`SELECT id,name,key_type,latest_version,min_decryption_version,deletion_allowed,created_at,updated_at
		 FROM transit_keys WHERE name=$1`, name).
		Scan(&k.ID, &k.Name, &k.KeyType, &k.LatestVersion, &k.MinDecryptionVersion,
			&k.DeletionAllowed, &k.CreatedAt, &k.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := r.s.pool.Query(ctx,
		`SELECT id,version,wrapped_material,public_key,created_at
		 FROM transit_key_versions WHERE transit_key_id=$1 ORDER BY version ASC`, k.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v TransitKeyVersion
		if err := rows.Scan(&v.ID, &v.Version, &v.WrappedMaterial, &v.PublicKey, &v.CreatedAt); err != nil {
			return nil, err
		}
		k.Versions = append(k.Versions, v)
	}
	return &k, rows.Err()
}

// List returns key metadata (no versions).
func (r *TransitRepo) List(ctx context.Context) ([]*TransitKey, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT id,name,key_type,latest_version,min_decryption_version,deletion_allowed,created_at,updated_at
		 FROM transit_keys ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*TransitKey
	for rows.Next() {
		var k TransitKey
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyType, &k.LatestVersion, &k.MinDecryptionVersion,
			&k.DeletionAllowed, &k.CreatedAt, &k.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &k)
	}
	return out, rows.Err()
}
```

**Before writing:** read `internal/store/store.go` and an existing repo (e.g. `projects.go`) to confirm the real names: the pool field (`r.s.pool` here), `NewID()`, the pgx unique-violation mapper (called `mapWriteErr` above — use the actual helper name, likely used by `ProjectRepo.Create`), and `ErrAlreadyExists`/`ErrNotFound`. Adjust identifiers to match; the SQL and structure stay as shown.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/store/ -run TestTransitRepoLifecycle -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/transit.go internal/store/transit_test.go
git commit -m "feat(store): TransitRepo (create/append/config/trim/delete/get/list)"
```

---

## Task 4: Transit engine — skeleton, errors, envelope

**Files:**
- Create: `internal/transit/errors.go`, `internal/transit/envelope.go`, `internal/transit/transit.go`
- Test: `internal/transit/envelope_test.go`

- [ ] **Step 1: Write the failing test**

```go
package transit

import "testing"

func TestEnvelopeRoundTrip(t *testing.T) {
	s := formatEnvelope(3, []byte{0x01, 0x02, 0x03})
	if s != "janus:v3:AQID" {
		t.Fatalf("format = %q", s)
	}
	ver, body, err := parseEnvelope("janus:v3:AQID")
	if err != nil {
		t.Fatal(err)
	}
	if ver != 3 || string(body) != "\x01\x02\x03" {
		t.Fatalf("parse = %d %x", ver, body)
	}
}

func TestParseEnvelopeRejects(t *testing.T) {
	for _, bad := range []string{"", "nope", "janus:v0:AQID", "janus:vx:AQID", "janus:v1:!!!", "vault:v1:AQID"} {
		if _, _, err := parseEnvelope(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/transit/ -run TestEnvelope -v` and `-run TestParseEnvelope`
Expected: FAIL — package/functions undefined.

- [ ] **Step 3: Write the implementations**

`internal/transit/errors.go`:

```go
package transit

import "errors"

var (
	ErrKeyNotFound        = errors.New("transit: key not found")
	ErrKeyExists          = errors.New("transit: key already exists")
	ErrWrongKeyType       = errors.New("transit: operation not valid for key type")
	ErrVersionTooOld      = errors.New("transit: ciphertext version below min_decryption_version")
	ErrBadCiphertext      = errors.New("transit: malformed or unverifiable ciphertext")
	ErrDeletionNotAllowed = errors.New("transit: deletion not allowed for this key")
	ErrValidation         = errors.New("transit: invalid input")
	ErrSealed             = errors.New("transit: server is sealed")
)
```

`internal/transit/envelope.go`:

```go
package transit

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// formatEnvelope renders janus:v<N>:<base64(body)>.
func formatEnvelope(version int, body []byte) string {
	return fmt.Sprintf("janus:v%d:%s", version, base64.StdEncoding.EncodeToString(body))
}

// parseEnvelope parses janus:v<N>:<base64> into (version, body). Version must be >= 1.
func parseEnvelope(s string) (int, []byte, error) {
	parts := strings.SplitN(s, ":", 3)
	if len(parts) != 3 || parts[0] != "janus" || !strings.HasPrefix(parts[1], "v") {
		return 0, nil, ErrBadCiphertext
	}
	version, err := strconv.Atoi(parts[1][1:])
	if err != nil || version < 1 {
		return 0, nil, ErrBadCiphertext
	}
	body, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return 0, nil, ErrBadCiphertext
	}
	return version, body, nil
}
```

`internal/transit/transit.go` (skeleton — engine + key-name validation; ops land in Tasks 5–7):

```go
package transit

import (
	"regexp"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

// KeyType values.
const (
	TypeAES     = "aes256-gcm"
	TypeEd25519 = "ed25519"
)

// keyring is the subset of *crypto.Keyring the engine needs (fakeable in tests).
type keyring interface {
	WrapKey(material, aad []byte) (crypto.Ciphertext, error)
	UnwrapKey(ct crypto.Ciphertext, aad []byte) ([]byte, error)
	Sealed() bool
}

// Service is the transit engine.
type Service struct {
	kr   keyring
	repo *store.TransitRepo
	st   *store.Store // for NewID
}

// New wires the engine over a keyring and store.
func New(kr keyring, st *store.Store) *Service {
	return &Service{kr: kr, repo: store.NewTransitRepo(st), st: st}
}

var keyNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func validKeyName(name string) bool { return keyNameRe.MatchString(name) }
```

**Note:** confirm `*crypto.Keyring` actually exposes `WrapKey`/`UnwrapKey`/`Sealed` with these signatures (read `internal/crypto/keyring.go`). The existing code wraps project KEKs via the keyring, so equivalent methods exist — match their real names/signatures and adjust the `keyring` interface accordingly. If wrap/unwrap are free functions taking the master key rather than keyring methods, expose a small keyring accessor or pass a wrap/unwrap closure; keep the interface seam so the engine stays unit-testable with a fake.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/transit/ -run 'TestEnvelope|TestParseEnvelope' -v`
Expected: PASS.
Run: `go build ./...`
Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add internal/transit/errors.go internal/transit/envelope.go internal/transit/transit.go internal/transit/envelope_test.go
git commit -m "feat(transit): engine skeleton, sentinels, janus:vN envelope"
```

---

## Task 5: Transit engine — create key + encrypt/decrypt (aes)

**Files:**
- Modify: `internal/transit/transit.go`
- Create: `internal/transit/crypto_ops.go`
- Test: `internal/transit/crypto_ops_test.go`

- [ ] **Step 1: Write the failing test**

```go
package transit

import (
	"bytes"
	"context"
	"testing"
)

func TestCreateEncryptDecryptRoundTrip(t *testing.T) {
	svc, _ := newTestService(t) // helper below boots a real store + unsealed keyring
	ctx := context.Background()

	if _, err := svc.CreateKey(ctx, "app", TypeAES); err != nil {
		t.Fatal(err)
	}
	ct, err := svc.Encrypt(ctx, "app", []byte("hello"), nil)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := svc.Decrypt(ctx, "app", ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pt, []byte("hello")) {
		t.Fatalf("roundtrip: %q", pt)
	}

	// Tamper → ErrBadCiphertext.
	bad := ct[:len(ct)-1] + "A"
	if _, err := svc.Decrypt(ctx, "app", bad, nil); err == nil {
		t.Fatal("tampered ciphertext must fail")
	}
	// AAD mismatch fails closed.
	ct2, _ := svc.Encrypt(ctx, "app", []byte("hi"), []byte("ctx-a"))
	if _, err := svc.Decrypt(ctx, "app", ct2, []byte("ctx-b")); err == nil {
		t.Fatal("associated-data mismatch must fail")
	}
	// Wrong key type: create ed25519, try encrypt.
	if _, err := svc.CreateKey(ctx, "sig", TypeEd25519); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Encrypt(ctx, "sig", []byte("x"), nil); err != ErrWrongKeyType {
		t.Fatalf("encrypt on ed25519: want ErrWrongKeyType, got %v", err)
	}
}
```

Add a real-store test harness `internal/transit/harness_test.go` mirroring
`internal/secrets/harness_test.go` (boot Postgres via testcontainers, build a
`*store.Store`, create + unseal a `*crypto.Keyring`, return `New(kr, st)`). Read
`internal/secrets/harness_test.go` to copy the unseal ceremony exactly.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/transit/ -run TestCreateEncryptDecrypt -count=1 -v`
Expected: FAIL — `svc.CreateKey undefined`.

- [ ] **Step 3: Write the implementation**

Add to `internal/transit/crypto_ops.go`:

```go
package transit

import (
	"context"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

// KeyMeta is the non-secret view of a transit key.
type KeyMeta struct {
	Name                 string
	Type                 string
	LatestVersion        int
	MinDecryptionVersion int
	DeletionAllowed      bool
	Versions             []int
}

func metaOf(k *store.TransitKey) KeyMeta {
	vs := make([]int, 0, len(k.Versions))
	for _, v := range k.Versions {
		vs = append(vs, v.Version)
	}
	return KeyMeta{Name: k.Name, Type: k.KeyType, LatestVersion: k.LatestVersion,
		MinDecryptionVersion: k.MinDecryptionVersion, DeletionAllowed: k.DeletionAllowed, Versions: vs}
}

// CreateKey creates a named key of the given type with version 1.
func (s *Service) CreateKey(ctx context.Context, name, keyType string) (KeyMeta, error) {
	if s.kr.Sealed() {
		return KeyMeta{}, ErrSealed
	}
	if !validKeyName(name) || (keyType != TypeAES && keyType != TypeEd25519) {
		return KeyMeta{}, ErrValidation
	}
	v, err := s.newVersion(name, 1, keyType)
	if err != nil {
		return KeyMeta{}, err
	}
	k, err := s.repo.Create(ctx, s.st.NewID(), name, keyType, v)
	if err != nil {
		return KeyMeta{}, mapStoreErr(err)
	}
	return metaOf(k), nil
}

// newVersion generates fresh key material for (name, version) and wraps it.
func (s *Service) newVersion(name string, version int, keyType string) (*store.TransitKeyVersion, error) {
	aad := crypto.TransitKeyAAD(name, version)
	v := &store.TransitKeyVersion{ID: s.st.NewID(), Version: version}
	switch keyType {
	case TypeAES:
		material, err := crypto.GenerateKey()
		if err != nil {
			return nil, err
		}
		defer crypto.Zero(material)
		wrapped, err := s.kr.WrapKey(material, aad)
		if err != nil {
			return nil, err
		}
		v.WrappedMaterial = wrapped.Bytes()
	case TypeEd25519:
		pub, priv, err := crypto.GenerateEd25519Key()
		if err != nil {
			return nil, err
		}
		defer crypto.Zero(priv)
		wrapped, err := s.kr.WrapKey(priv, aad)
		if err != nil {
			return nil, err
		}
		v.WrappedMaterial = wrapped.Bytes()
		v.PublicKey = pub
	}
	return v, nil
}

// materialFor unwraps a specific version's key material (caller must Zero it).
func (s *Service) materialFor(k *store.TransitKey, version int) ([]byte, error) {
	for _, v := range k.Versions {
		if v.Version == version {
			ct, err := crypto.ParseCiphertext(v.WrappedMaterial)
			if err != nil {
				return nil, err
			}
			return s.kr.UnwrapKey(ct, crypto.TransitKeyAAD(k.Name, version))
		}
	}
	return nil, ErrBadCiphertext
}

// Encrypt encrypts plaintext under the key's latest version (aes only).
func (s *Service) Encrypt(ctx context.Context, name string, plaintext, aad []byte) (string, error) {
	if s.kr.Sealed() {
		return "", ErrSealed
	}
	k, err := s.repo.GetByName(ctx, name)
	if err != nil {
		return "", mapStoreErr(err)
	}
	if k.KeyType != TypeAES {
		return "", ErrWrongKeyType
	}
	material, err := s.materialFor(k, k.LatestVersion)
	if err != nil {
		return "", err
	}
	defer crypto.Zero(material)
	ct, err := crypto.Encrypt(material, plaintext, aad)
	if err != nil {
		return "", err
	}
	return formatEnvelope(k.LatestVersion, ct.Bytes()), nil
}

// Decrypt decrypts a janus:vN: ciphertext (aes only), honoring min_decryption_version.
func (s *Service) Decrypt(ctx context.Context, name, ciphertext string, aad []byte) ([]byte, error) {
	if s.kr.Sealed() {
		return nil, ErrSealed
	}
	version, body, err := parseEnvelope(ciphertext)
	if err != nil {
		return nil, err
	}
	k, err := s.repo.GetByName(ctx, name)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if k.KeyType != TypeAES {
		return nil, ErrWrongKeyType
	}
	if version < k.MinDecryptionVersion {
		return nil, ErrVersionTooOld
	}
	material, err := s.materialFor(k, version)
	if err != nil {
		return nil, err
	}
	defer crypto.Zero(material)
	ct, err := crypto.ParseCiphertext(body)
	if err != nil {
		return nil, ErrBadCiphertext
	}
	pt, err := crypto.Decrypt(material, ct, aad)
	if err != nil {
		return nil, ErrBadCiphertext // generic: never reveal which check failed
	}
	return pt, nil
}
```

Add `mapStoreErr` to `internal/transit/transit.go`:

```go
import "errors"
import "github.com/steveokay/janus-secrets/internal/store"

// mapStoreErr translates store sentinels to transit sentinels.
func mapStoreErr(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrKeyNotFound
	case errors.Is(err, store.ErrAlreadyExists):
		return ErrKeyExists
	default:
		return err
	}
}
```

**Confirm:** `crypto.Ciphertext.Bytes()` (or the real marshal method — check `ParseCiphertext`'s inverse in `internal/crypto/aead.go`), `crypto.Zero`, and whether wrap/unwrap are keyring methods (adjust per Task 4's note). Use the real names.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/transit/ -run TestCreateEncryptDecrypt -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/transit/crypto_ops.go internal/transit/transit.go internal/transit/crypto_ops_test.go internal/transit/harness_test.go
git commit -m "feat(transit): create key + aes encrypt/decrypt with versioned wrapping"
```

---

## Task 6: Transit engine — rotate, config, rewrap, trim

**Files:**
- Create: `internal/transit/lifecycle.go`
- Test: `internal/transit/lifecycle_test.go`

- [ ] **Step 1: Write the failing test**

```go
package transit

import (
	"bytes"
	"context"
	"testing"
)

func TestRotateRewrapMinVersionTrim(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	_, _ = svc.CreateKey(ctx, "k", TypeAES)

	ctV1, _ := svc.Encrypt(ctx, "k", []byte("secret"), nil)

	m, err := svc.Rotate(ctx, "k")
	if err != nil || m.LatestVersion != 2 {
		t.Fatalf("rotate: %+v %v", m, err)
	}
	// Old ciphertext still decrypts (v1 >= min 1).
	if pt, err := svc.Decrypt(ctx, "k", ctV1, nil); err != nil || !bytes.Equal(pt, []byte("secret")) {
		t.Fatalf("old decrypt: %q %v", pt, err)
	}
	// Rewrap upgrades to latest, no plaintext exposed.
	ctV2, err := svc.Rewrap(ctx, "k", ctV1)
	if err != nil {
		t.Fatal(err)
	}
	if ctV2 == ctV1 {
		t.Fatal("rewrap should produce a new envelope")
	}
	// Raise the decryption floor to 2; v1 ciphertext now rejected, v2 ok.
	two := 2
	if err := svc.UpdateConfig(ctx, "k", &two, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Decrypt(ctx, "k", ctV1, nil); err != ErrVersionTooOld {
		t.Fatalf("v1 after floor: want ErrVersionTooOld, got %v", err)
	}
	if _, err := svc.Decrypt(ctx, "k", ctV2, nil); err != nil {
		t.Fatalf("v2 after floor: %v", err)
	}
	// Trim below 2 removes v1; must not exceed min_decryption_version.
	if err := svc.Trim(ctx, "k", 3); err != ErrValidation {
		t.Fatalf("trim above floor: want ErrValidation, got %v", err)
	}
	if err := svc.Trim(ctx, "k", 2); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/transit/ -run TestRotateRewrap -count=1 -v`
Expected: FAIL — `svc.Rotate undefined`.

- [ ] **Step 3: Write the implementation**

```go
package transit

import "context"

// Rotate appends a new version and makes it latest.
func (s *Service) Rotate(ctx context.Context, name string) (KeyMeta, error) {
	if s.kr.Sealed() {
		return KeyMeta{}, ErrSealed
	}
	k, err := s.repo.GetByName(ctx, name)
	if err != nil {
		return KeyMeta{}, mapStoreErr(err)
	}
	v, err := s.newVersion(name, k.LatestVersion+1, k.KeyType)
	if err != nil {
		return KeyMeta{}, err
	}
	if err := s.repo.AppendVersion(ctx, k.ID, v); err != nil {
		return KeyMeta{}, mapStoreErr(err)
	}
	return s.readMeta(ctx, name)
}

// UpdateConfig sets min_decryption_version (must be within [1, latest]) and/or
// deletion_allowed.
func (s *Service) UpdateConfig(ctx context.Context, name string, minDec *int, delAllowed *bool) error {
	k, err := s.repo.GetByName(ctx, name)
	if err != nil {
		return mapStoreErr(err)
	}
	if minDec != nil && (*minDec < 1 || *minDec > k.LatestVersion) {
		return ErrValidation
	}
	return mapStoreErr(s.repo.UpdateConfig(ctx, k.ID, minDec, delAllowed))
}

// Rewrap decrypts an old ciphertext and re-encrypts under the latest version.
// Plaintext is never returned.
func (s *Service) Rewrap(ctx context.Context, name, ciphertext string) (string, error) {
	pt, err := s.Decrypt(ctx, name, ciphertext, nil)
	if err != nil {
		return "", err
	}
	defer crypto.Zero(pt)
	return s.Encrypt(ctx, name, pt, nil)
}

// Trim permanently deletes versions below minAvailable (<= min_decryption_version).
func (s *Service) Trim(ctx context.Context, name string, minAvailable int) error {
	k, err := s.repo.GetByName(ctx, name)
	if err != nil {
		return mapStoreErr(err)
	}
	if minAvailable < 1 || minAvailable > k.MinDecryptionVersion {
		return ErrValidation
	}
	return s.repo.TrimBelow(ctx, k.ID, minAvailable)
}

// readMeta reloads a key as KeyMeta.
func (s *Service) readMeta(ctx context.Context, name string) (KeyMeta, error) {
	k, err := s.repo.GetByName(ctx, name)
	if err != nil {
		return KeyMeta{}, mapStoreErr(err)
	}
	return metaOf(k), nil
}
```

Add the `crypto` import to `lifecycle.go` (for `crypto.Zero`).

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/transit/ -run TestRotateRewrap -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/transit/lifecycle.go internal/transit/lifecycle_test.go
git commit -m "feat(transit): rotate, config (min_decryption_version), rewrap, trim"
```

---

## Task 7: Transit engine — sign/verify (ed25519) + datakey

**Files:**
- Create: `internal/transit/sign_ops.go`
- Test: `internal/transit/sign_ops_test.go`

- [ ] **Step 1: Write the failing test**

```go
package transit

import (
	"context"
	"encoding/base64"
	"testing"
)

func TestSignVerifyAndDatakey(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	_, _ = svc.CreateKey(ctx, "sig", TypeEd25519)
	sig, err := svc.Sign(ctx, "sig", []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	ok, err := svc.Verify(ctx, "sig", []byte("payload"), sig)
	if err != nil || !ok {
		t.Fatalf("verify: %v %v", ok, err)
	}
	bad, _ := svc.Verify(ctx, "sig", []byte("other"), sig)
	if bad {
		t.Fatal("wrong message must not verify")
	}
	// Sign on an aes key → wrong type.
	_, _ = svc.CreateKey(ctx, "enc", TypeAES)
	if _, err := svc.Sign(ctx, "enc", []byte("x")); err != ErrWrongKeyType {
		t.Fatalf("sign on aes: want ErrWrongKeyType, got %v", err)
	}
	// Datakey: wrapped decrypts back to the returned plaintext.
	pt, ct, err := svc.DataKey(ctx, "enc")
	if err != nil {
		t.Fatal(err)
	}
	if len(pt) != 32 {
		t.Fatalf("datakey plaintext len = %d, want 32", len(pt))
	}
	got, err := svc.Decrypt(ctx, "enc", ct, nil)
	if err != nil || base64.StdEncoding.EncodeToString(got) != base64.StdEncoding.EncodeToString(pt) {
		t.Fatalf("wrapped datakey must decrypt to plaintext: %v", err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/transit/ -run TestSignVerifyAndDatakey -count=1 -v`
Expected: FAIL — `svc.Sign undefined`.

- [ ] **Step 3: Write the implementation**

```go
package transit

import (
	"context"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

// Sign signs input with the ed25519 key's latest version.
func (s *Service) Sign(ctx context.Context, name string, input []byte) (string, error) {
	if s.kr.Sealed() {
		return "", ErrSealed
	}
	k, err := s.repo.GetByName(ctx, name)
	if err != nil {
		return "", mapStoreErr(err)
	}
	if k.KeyType != TypeEd25519 {
		return "", ErrWrongKeyType
	}
	priv, err := s.materialFor(k, k.LatestVersion)
	if err != nil {
		return "", err
	}
	defer crypto.Zero(priv)
	return formatEnvelope(k.LatestVersion, crypto.Sign(priv, input)), nil
}

// Verify checks a janus:vN: signature against input using that version's public key.
func (s *Service) Verify(ctx context.Context, name string, input []byte, signature string) (bool, error) {
	version, sig, err := parseEnvelope(signature)
	if err != nil {
		return false, err
	}
	k, err := s.repo.GetByName(ctx, name)
	if err != nil {
		return false, mapStoreErr(err)
	}
	if k.KeyType != TypeEd25519 {
		return false, ErrWrongKeyType
	}
	for _, v := range k.Versions {
		if v.Version == version {
			return crypto.Verify(v.PublicKey, input, sig), nil
		}
	}
	return false, ErrBadCiphertext
}

// DataKey generates a fresh 256-bit data key, returning it in plaintext plus a
// wrapped (encrypted-under-the-key) ciphertext. Aes keys only.
func (s *Service) DataKey(ctx context.Context, name string) (plaintext []byte, ciphertext string, err error) {
	if s.kr.Sealed() {
		return nil, "", ErrSealed
	}
	k, err := s.repo.GetByName(ctx, name)
	if err != nil {
		return nil, "", mapStoreErr(err)
	}
	if k.KeyType != TypeAES {
		return nil, "", ErrWrongKeyType
	}
	dk, err := crypto.GenerateKey()
	if err != nil {
		return nil, "", err
	}
	ct, err := s.Encrypt(ctx, name, dk, nil)
	if err != nil {
		crypto.Zero(dk)
		return nil, "", err
	}
	return dk, ct, nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/transit/ -run TestSignVerifyAndDatakey -count=1 -v`
Expected: PASS. Then `go test ./internal/transit/ -count=1` (whole package) — all green.

- [ ] **Step 5: Commit**

```bash
git add internal/transit/sign_ops.go internal/transit/sign_ops_test.go
git commit -m "feat(transit): ed25519 sign/verify + datakey generation"
```

---

## Task 8: AuthZ — transit actions, Resource.TransitKey, token capabilities

**Files:**
- Modify: `internal/authz/actions.go`, `internal/authz/resource.go`, `internal/authz/resolve.go`
- Test: `internal/authz/transit_test.go`

- [ ] **Step 1: Write the failing test**

```go
package authz

import (
	"context"
	"testing"

	"github.com/steveokay/janus-secrets/internal/auth"
)

func TestTransitTokenCapabilities(t *testing.T) {
	e := New(nil) // token path doesn't touch the binding store
	tok := auth.Principal{Kind: auth.KindServiceToken, ID: "t1"}

	useAll := &TokenScope{Kind: "transit", ID: "", Access: "use"}
	if err := e.Can(context.Background(), tok, useAll, TransitUse, Resource{TransitKey: "any"}); err != nil {
		t.Fatalf("use token should allow transit:use on any key: %v", err)
	}
	if err := e.Can(context.Background(), tok, useAll, TransitManage, Resource{TransitKey: "any"}); err == nil {
		t.Fatal("use token must NOT allow transit:manage")
	}
	if err := e.Can(context.Background(), tok, useAll, SecretRead, Resource{ConfigID: "c1"}); err == nil {
		t.Fatal("transit token must NOT allow secret:read")
	}

	scoped := &TokenScope{Kind: "transit", ID: "billing", Access: "manage"}
	if err := e.Can(context.Background(), tok, scoped, TransitManage, Resource{TransitKey: "billing"}); err != nil {
		t.Fatalf("manage token should allow its key: %v", err)
	}
	if err := e.Can(context.Background(), tok, scoped, TransitUse, Resource{TransitKey: "other"}); err == nil {
		t.Fatal("key-restricted token must deny a different key")
	}
}

func TestTransitRoleMatrix(t *testing.T) {
	if !roleAllows(RoleViewer, TransitRead) {
		t.Fatal("viewer reads transit metadata")
	}
	if roleAllows(RoleViewer, TransitUse) {
		t.Fatal("viewer must not use transit")
	}
	if !roleAllows(RoleDeveloper, TransitUse) || roleAllows(RoleDeveloper, TransitManage) {
		t.Fatal("developer uses but does not manage transit")
	}
	if !roleAllows(RoleAdmin, TransitManage) {
		t.Fatal("admin manages transit")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/authz/ -run 'TestTransit' -v`
Expected: FAIL — `undefined: TransitUse`.

- [ ] **Step 3: Write the implementation**

In `actions.go`, add the actions and extend the matrix:

```go
	// ... existing actions ...
	TransitRead   Action = "transit:read"   // instance-scoped
	TransitUse    Action = "transit:use"    // instance-scoped
	TransitManage Action = "transit:manage" // instance-scoped
```

```go
	viewerActions    = setOf(SecretRead, ConfigRead, ProjectRead, MemberRead, TransitRead)
	developerActions = union(viewerActions, setOf(SecretWrite, ConfigCreate, TransitUse))
	adminActions     = union(developerActions, setOf(
		ConfigDelete, EnvCreate, EnvDelete, ProjectCreate, MemberManage,
		TokenRead, TokenMint, TokenRevoke, UserManage, AuditRead, SysSeal, TransitManage))
```

In `resource.go`, add the field:

```go
type Resource struct {
	ProjectID string
	EnvID     string
	ConfigID  string
	TransitKey string // transit key name (transit ops); empty otherwise
}
```

In `resolve.go`, extend the token path:

```go
// transitTokenCapabilities maps a transit token's access to actions.
func transitTokenCapabilities(access string) map[Action]bool {
	switch access {
	case "use":
		return setOf(TransitRead, TransitUse)
	case "manage":
		return setOf(TransitRead, TransitUse, TransitManage)
	default:
		return nil
	}
}

func tokenAllows(scope TokenScope, action Action, res Resource) bool {
	switch scope.Kind {
	case "config":
		return tokenCapabilities(scope.Access)[action] && res.ConfigID != "" && res.ConfigID == scope.ID
	case "environment":
		return tokenCapabilities(scope.Access)[action] && res.EnvID != "" && res.EnvID == scope.ID
	case "transit":
		if !transitTokenCapabilities(scope.Access)[action] {
			return false
		}
		return scope.ID == "" || scope.ID == res.TransitKey // "" = all keys
	default:
		return false
	}
}
```

(Replace the existing `tokenAllows` body with the switch above — it preserves the
config/environment behavior and adds transit.)

- [ ] **Step 4: Run to verify it passes + coverage**

Run: `go test ./internal/authz/ -run TestTransit -v`
Expected: PASS.
Run: `go test ./internal/authz/ -cover`
Expected: 100.0% (the authz gate). If `transitTokenCapabilities`'s default branch is uncovered, add a one-line assertion (`transitTokenCapabilities("bad")` returns nil via a denied `Can`).

- [ ] **Step 5: Commit**

```bash
git add internal/authz/actions.go internal/authz/resource.go internal/authz/resolve.go internal/authz/transit_test.go
git commit -m "feat(authz): transit actions, Resource.TransitKey, transit token capabilities"
```

---

## Task 9: Auth — transit token scope in mint/verify

**Files:**
- Modify: `internal/auth/tokens.go`, `internal/store/tokens.go`
- Test: `internal/auth/transit_token_test.go`

- [ ] **Step 1: Read then write the failing test**

Read `internal/auth/tokens.go` fully first (MintServiceToken signature, scope
validation switch, TokenMeta) and `internal/store/tokens.go` (Create signature).
The transit scope needs: `access ∈ {use, manage}` allowed when `scopeKind ==
"transit"`; `scopeID` optional (empty = all keys) and, when present, validated to
be an existing transit key id; the store `Create` must accept an empty scopeID and
persist NULL.

```go
package auth

import (
	"context"
	"testing"
)

func TestMintTransitToken(t *testing.T) {
	s, by := newTokenTestService(t) // existing harness that yields a Service + actor

	// All-keys transit token.
	raw, meta, err := s.MintServiceToken(context.Background(), by, "ci", "transit", "", "use", nil)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ScopeKind != "transit" || meta.Access != "use" || raw == "" {
		t.Fatalf("meta: %+v", meta)
	}
	_, scope, err := s.VerifyServiceToken(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if scope.Kind != "transit" || scope.ID != "" || scope.Access != "use" {
		t.Fatalf("scope: %+v", scope)
	}

	// Bad access for transit.
	if _, _, err := s.MintServiceToken(context.Background(), by, "x", "transit", "", "readwrite", nil); err == nil {
		t.Fatal("transit scope must reject access=readwrite")
	}
}
```

Read `internal/auth/tokens_test.go` (or the auth harness) for the real
`newTokenTestService`-style helper name and reuse it.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/auth/ -run TestMintTransitToken -count=1 -v`
Expected: FAIL (transit scope rejected by the current validation).

- [ ] **Step 3: Write the implementation**

In `internal/auth/tokens.go`, generalize access validation and add a transit case:

```go
	// access validation depends on scope kind.
	validAccess := map[string]bool{"read": true, "readwrite": true}
	if scopeKind == "transit" {
		validAccess = map[string]bool{"use": true, "manage": true}
	}
	if !validAccess[access] {
		return "", TokenMeta{}, fmt.Errorf("%w: bad access", ErrValidation)
	}

	switch scopeKind {
	case "config":
		if _, err := s.configs.Get(ctx, scopeID); err != nil {
			return "", TokenMeta{}, scopeErr(err)
		}
	case "environment":
		if _, err := s.envs.Get(ctx, scopeID); err != nil {
			return "", TokenMeta{}, scopeErr(err)
		}
	case "transit":
		if scopeID != "" { // "" = all transit keys
			if _, err := s.transit.GetByID(ctx, scopeID); err != nil {
				return "", TokenMeta{}, scopeErr(err)
			}
		}
	default:
		return "", TokenMeta{}, fmt.Errorf("%w: bad scope kind", ErrValidation)
	}
```

This needs the auth `Service` to hold a transit-key lookup. Add a `transit`
dependency to the auth Service (a small interface `transitKeys interface{
GetByID(ctx, id string) (*store.TransitKey, error) }`) and add
`TransitRepo.GetByID` to `internal/store/transit.go` (mirror `GetByName` keyed on
`id`). Wire it in `auth.New(...)` (Boot passes `store.NewTransitRepo(st)`).

In `internal/store/tokens.go`, make `Create` persist an empty scopeID as SQL NULL:

```go
	var sid any = scopeID
	if scopeID == "" {
		sid = nil
	}
	// ... use sid in the INSERT for the scope_id column ...
```

And ensure the read path scans `scope_id` into a `*string`/`sql.NullString` and
maps NULL → "". (Read the current `TokenRepo` scan to apply this consistently.)

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/auth/ -run TestMintTransitToken -count=1 -v`
Expected: PASS. Then `go test ./internal/auth/ ./internal/store/ -count=1` — existing token tests still green.

- [ ] **Step 5: Commit**

```bash
git add internal/auth/tokens.go internal/store/tokens.go internal/store/transit.go internal/auth/transit_token_test.go
git commit -m "feat(auth): transit token scope (use/manage, optional key restriction)"
```

---

## Task 10: API — writeServiceError mapping + transit management routes

**Files:**
- Modify: `internal/api/service_errors.go`, `internal/api/server.go`, `internal/api/boot.go`
- Create: `internal/api/transit_handlers.go`
- Test: `internal/api/transit_e2e_test.go`

- [ ] **Step 1: Write the failing e2e test**

```go
package api

import (
	"net/http"
	"testing"
)

func TestTransitKeyLifecycleE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t) // real server + owner admin
	cookie := loginCookie(t, ts, email, password) // existing helper; else login inline

	// Create an aes key.
	var created map[string]any
	if code := doAuthed(t, "POST", ts.URL+"/v1/transit/keys", cookie, "",
		`{"name":"app","type":"aes256-gcm"}`, &created); code != http.StatusCreated {
		t.Fatalf("create key: %d", code)
	}
	// List includes it.
	var list map[string]any
	if code := doAuthed(t, "GET", ts.URL+"/v1/transit/keys", cookie, "", "", &list); code != 200 {
		t.Fatalf("list: %d", code)
	}
	// Rotate → latest_version 2.
	var rotated map[string]any
	if code := doAuthed(t, "POST", ts.URL+"/v1/transit/keys/app/rotate", cookie, "", "", &rotated); code != 200 {
		t.Fatalf("rotate: %d", code)
	}
	if int(rotated["latest_version"].(float64)) != 2 {
		t.Fatalf("latest_version: %v", rotated["latest_version"])
	}
	// Delete requires deletion_allowed.
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/transit/keys/app", cookie, "", "", nil); code == http.StatusNoContent {
		t.Fatal("delete without deletion_allowed must fail")
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/transit/keys/app/config", cookie, "",
		`{"deletion_allowed":true}`, nil); code != 200 {
		t.Fatalf("config: %d", code)
	}
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/transit/keys/app", cookie, "", "", nil); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
}
```

Use the existing e2e helpers (`authStackFull`, `doAuthed`). If a `loginCookie`
helper doesn't exist, log in inline the way `auth_e2e_test.go` does to get the
`janus_session` value.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestTransitKeyLifecycleE2E -count=1 -v`
Expected: FAIL — route 404 (handlers/wiring absent).

- [ ] **Step 3: Write the implementation**

Extend `writeServiceError` (add a transit import + cases):

```go
	case errors.Is(err, transit.ErrSealed):
		writeError(w, http.StatusServiceUnavailable, CodeSealed, "server is sealed; unseal via /v1/sys/unseal")
	case errors.Is(err, transit.ErrKeyNotFound):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, transit.ErrKeyExists):
		writeError(w, http.StatusConflict, "conflict", "conflict")
	case errors.Is(err, transit.ErrDeletionNotAllowed):
		writeError(w, http.StatusConflict, "conflict", "deletion not allowed for this key")
	case errors.Is(err, transit.ErrWrongKeyType), errors.Is(err, transit.ErrVersionTooOld),
		errors.Is(err, transit.ErrBadCiphertext), errors.Is(err, transit.ErrValidation):
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid input")
```

Add a `transit *transit.Service` field to `Server`; accept it in `New(...)` and
have `Boot(...)` construct `transit.New(kr, st)` and pass it. Register routes in
`server.go` (guarded `if s.transit != nil`, behind `RequireAuth` +
`RequireUnsealed`), using the absolute-path `r.Group` convention:

```go
	r.Post("/v1/transit/keys", s.handleTransitCreate)
	r.Get("/v1/transit/keys", s.handleTransitList)
	r.Get("/v1/transit/keys/{name}", s.handleTransitGet)
	r.Post("/v1/transit/keys/{name}/rotate", s.handleTransitRotate)
	r.Post("/v1/transit/keys/{name}/config", s.handleTransitConfig)
	r.Post("/v1/transit/keys/{name}/trim", s.handleTransitTrim)
	r.Delete("/v1/transit/keys/{name}", s.handleTransitDelete)
```

`internal/api/transit_handlers.go` — management handlers. Each authorizes with an
instance-scoped resource carrying the key name, then records an audit event. Example
create + rotate (others follow the same shape):

```go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
)

func transitMeta(m transitKeyMeta) map[string]any { // transitKeyMeta = transit.KeyMeta re-exported via a small view
	return map[string]any{
		"name": m.Name, "type": m.Type, "latest_version": m.LatestVersion,
		"min_decryption_version": m.MinDecryptionVersion, "deletion_allowed": m.DeletionAllowed,
		"versions": m.Versions,
	}
}

func (s *Server) handleTransitCreate(w http.ResponseWriter, r *http.Request) {
	var req struct{ Name, Type string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "name and type are required")
		return
	}
	res := authz.Resource{TransitKey: req.Name}
	if !s.authorize(w, r, authz.TransitManage, res, "transit.key.create", "transit/keys/"+req.Name) {
		return
	}
	m, err := s.transit.CreateKey(r.Context(), req.Name, req.Type)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "transit.key.create", "transit/keys/"+req.Name, "success", "", req.Type); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, transitMeta(m))
}

func (s *Server) handleTransitRotate(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	res := authz.Resource{TransitKey: name}
	if !s.authorize(w, r, authz.TransitManage, res, "transit.key.rotate", "transit/keys/"+name) {
		return
	}
	m, err := s.transit.Rotate(r.Context(), name)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "transit.key.rotate", "transit/keys/"+name, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, transitMeta(m))
}
```

Write `handleTransitList` (`transit:read`, `s.can`, no audit — metadata read),
`handleTransitGet` (`transit:read`, no audit), `handleTransitConfig`
(`transit:manage`, audit `transit.key.config`; body `{min_decryption_version?,
deletion_allowed?}` → `UpdateConfig`), `handleTransitTrim` (`transit:manage`, audit
`transit.key.trim`; body `{min_available_version}` → `Trim`), and
`handleTransitDelete` (`transit:manage`, audit `transit.key.delete`; check
`deletion_allowed` via the key's config — the engine returns `ErrDeletionNotAllowed`
if not; add a `Delete` engine method that reads the key, enforces `deletion_allowed`,
then `repo.Delete`). Add `transit.Service.Delete(ctx, name) error` and
`transit.Service.Get(ctx, name)(KeyMeta,error)` / `List` to the engine
(`internal/transit/lifecycle.go`) with matching tests appended to Task 6's test
file — or, to keep tasks clean, add them here with unit coverage in this task's
engine change. Define `transitKeyMeta` as a type alias for `transit.KeyMeta` in
`transit_handlers.go` (`type transitKeyMeta = transit.KeyMeta`).

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/api/ -run TestTransitKeyLifecycleE2E -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/transit_handlers.go internal/api/service_errors.go internal/api/server.go internal/api/boot.go internal/transit/lifecycle.go internal/transit/lifecycle_test.go internal/api/transit_e2e_test.go
git commit -m "feat(api): transit key management routes + engine Delete/Get/List + error mapping"
```

---

## Task 11: API — transit data-plane routes

**Files:**
- Create: `internal/api/transit_dataplane_handlers.go`
- Modify: `internal/api/server.go` (register routes)
- Test: `internal/api/transit_dataplane_e2e_test.go`

- [ ] **Step 1: Write the failing e2e test**

```go
package api

import (
	"encoding/base64"
	"net/http"
	"testing"
)

func TestTransitDataPlaneE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	cookie := loginCookie(t, ts, email, password)
	mk := func(path, body string, out any) int { return doAuthed(t, "POST", ts.URL+path, cookie, "", body, out) }

	_ = doAuthed(t, "POST", ts.URL+"/v1/transit/keys", cookie, "", `{"name":"app","type":"aes256-gcm"}`, nil)

	pt := base64.StdEncoding.EncodeToString([]byte("hello"))
	var enc struct{ Ciphertext string `json:"ciphertext"` }
	if code := mk("/v1/transit/encrypt/app", `{"plaintext":"`+pt+`"}`, &enc); code != 200 || enc.Ciphertext == "" {
		t.Fatalf("encrypt: %d %q", code, enc.Ciphertext)
	}
	var dec struct{ Plaintext string `json:"plaintext"` }
	if code := mk("/v1/transit/decrypt/app", `{"ciphertext":"`+enc.Ciphertext+`"}`, &dec); code != 200 {
		t.Fatalf("decrypt: %d", code)
	}
	if got, _ := base64.StdEncoding.DecodeString(dec.Plaintext); string(got) != "hello" {
		t.Fatalf("decrypt roundtrip: %q", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestTransitDataPlaneE2E -count=1 -v`
Expected: FAIL — routes 404.

- [ ] **Step 3: Write the implementation**

Register (behind `RequireAuth` + `RequireUnsealed`):

```go
	r.Post("/v1/transit/encrypt/{name}", s.handleTransitEncrypt)
	r.Post("/v1/transit/decrypt/{name}", s.handleTransitDecrypt)
	r.Post("/v1/transit/sign/{name}", s.handleTransitSign)
	r.Post("/v1/transit/verify/{name}", s.handleTransitVerify)
	r.Post("/v1/transit/rewrap/{name}", s.handleTransitRewrap)
	r.Post("/v1/transit/datakey/plaintext/{name}", s.handleTransitDatakeyPlaintext)
	r.Post("/v1/transit/datakey/wrapped/{name}", s.handleTransitDatakeyWrapped)
```

`internal/api/transit_dataplane_handlers.go` — each authorizes `transit:use` on
`authz.Resource{TransitKey: name}` via `s.can` (**no audit** — data-plane), base64
in/out. Example encrypt + decrypt (sign/verify/rewrap/datakey follow the same
shape):

```go
package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
)

func (s *Server) transitUse(w http.ResponseWriter, r *http.Request, name string) bool {
	if err := s.can(r, authz.TransitUse, authz.Resource{TransitKey: name}); err != nil {
		s.writeAuthzError(w, err)
		return false
	}
	return true
}

func (s *Server) handleTransitEncrypt(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !s.transitUse(w, r, name) {
		return
	}
	var req struct{ Plaintext, AssociatedData string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	pt, err := base64.StdEncoding.DecodeString(req.Plaintext)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "plaintext must be base64")
		return
	}
	var aad []byte
	if req.AssociatedData != "" {
		if aad, err = base64.StdEncoding.DecodeString(req.AssociatedData); err != nil {
			writeError(w, http.StatusBadRequest, CodeValidation, "associated_data must be base64")
			return
		}
	}
	ct, err := s.transit.Encrypt(r.Context(), name, pt, aad)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ciphertext": ct})
}

func (s *Server) handleTransitDecrypt(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !s.transitUse(w, r, name) {
		return
	}
	var req struct{ Ciphertext, AssociatedData string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Ciphertext == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "ciphertext is required")
		return
	}
	var aad []byte
	if req.AssociatedData != "" {
		var derr error
		if aad, derr = base64.StdEncoding.DecodeString(req.AssociatedData); derr != nil {
			writeError(w, http.StatusBadRequest, CodeValidation, "associated_data must be base64")
			return
		}
	}
	pt, err := s.transit.Decrypt(r.Context(), name, req.Ciphertext, aad)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plaintext": base64.StdEncoding.EncodeToString(pt)})
}
```

Write `handleTransitSign` (`{input}` b64 → `{signature}`), `handleTransitVerify`
(`{input, signature}` → `{valid}`), `handleTransitRewrap` (`{ciphertext}` →
`{ciphertext}`), `handleTransitDatakeyPlaintext` (→ `{ciphertext, plaintext}`, both
b64) and `handleTransitDatakeyWrapped` (→ `{ciphertext}` only; discards the
plaintext DEK — zero it). All gate on `s.transitUse`.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/api/ -run TestTransitDataPlaneE2E -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/transit_dataplane_handlers.go internal/api/server.go internal/api/transit_dataplane_e2e_test.go
git commit -m "feat(api): transit data-plane routes (encrypt/decrypt/sign/verify/rewrap/datakey)"
```

---

## Task 12: API — transit token mint plumbing

**Files:**
- Modify: `internal/api/tokens_handlers.go`
- Test: `internal/api/transit_token_e2e_test.go`

- [ ] **Step 1: Read then write the failing e2e test**

Read `internal/api/tokens_handlers.go` (`mintTokenRequest` shape, how scope kind/id
+ access flow into `MintServiceToken`). The handler likely already passes
`scope.kind`/`scope.id`/`access` straight through; if so, the transit scope works
once auth (Task 9) accepts it, and this task is mostly an e2e proving it end to end.

```go
package api

import (
	"net/http"
	"testing"
)

func TestTransitScopedTokenUsesTransitOnly(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	cookie := loginCookie(t, ts, email, password)

	// Owner creates a transit key.
	_ = doAuthed(t, "POST", ts.URL+"/v1/transit/keys", cookie, "", `{"name":"app","type":"aes256-gcm"}`, nil)

	// Mint an all-keys transit-use token.
	var minted struct{ Token string `json:"token"` }
	code := doAuthed(t, "POST", ts.URL+"/v1/tokens", cookie, "",
		`{"name":"ci","scope":{"kind":"transit","id":""},"access":"use"}`, &minted)
	if code != http.StatusCreated || minted.Token == "" {
		t.Fatalf("mint transit token: %d", code)
	}

	// The token can encrypt...
	if code := doAuthed(t, "POST", ts.URL+"/v1/transit/encrypt/app", "", minted.Token,
		`{"plaintext":"aGk="}`, nil); code != 200 {
		t.Fatalf("transit token encrypt: %d", code)
	}
	// ...but cannot read secrets (needs a config; a 403/404 either way, never 200).
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects", "", minted.Token, "", nil); code == 200 {
		t.Fatal("transit token must not list projects")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestTransitScopedTokenUsesTransitOnly -count=1 -v`
Expected: FAIL if the mint handler rejects/ignores the transit scope or hardcodes
access validation; PASS-after-fix once it forwards scope kind/id/access verbatim.

- [ ] **Step 3: Write the implementation**

Ensure `handleTokenMint` forwards `req.Scope.Kind`, `req.Scope.ID` (allowing empty
for transit), and `req.Access` unchanged to `s.auth.MintServiceToken(...)` and maps
its `ErrValidation` → 400. Remove any handler-side allowlist that hardcodes
`config`/`environment` or `read`/`readwrite` (the auth layer now owns that
validation). If the handler already forwards verbatim, no code change is needed and
the test passes once Task 9 is in — note that in the commit.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/api/ -run TestTransitScopedTokenUsesTransitOnly -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/tokens_handlers.go internal/api/transit_token_e2e_test.go
git commit -m "feat(api): mint transit-scoped service tokens end to end"
```

---

## Task 13: RBAC matrix + audit + leak e2e, gates, docs

**Files:**
- Create: `internal/api/transit_rbac_e2e_test.go`, `internal/api/transit_leak_test.go`
- Create: `docs/transit.md`
- Modify: `status.md`, `docs/operations.md`, `README.md`

- [ ] **Step 1: Write the RBAC + audit + leak e2e tests**

`transit_rbac_e2e_test.go` — prove: a viewer can `GET /v1/transit/keys` but is
denied `POST /v1/transit/encrypt/{name}` (403) and create (403); a developer can
encrypt but not create/rotate (403); a key-restricted transit token is denied a
different key (403); a secrets (config-scoped) token is denied all transit routes
(403). Use `makeUser` + membership grants the way `secrets_rbac_e2e_test.go` does.

`transit_leak_test.go` — drive create → rotate → encrypt → decrypt through the
server with `slog.Default()` redirected into a mutex-guarded buffer (mirror the M8
`leak_test.go` / M9 CLI approach), then assert no unwrapped key material, no
`wrapped_material` bytes, and no datakey plaintext appear in the captured logs or in
any error body. Also assert an audit chain query shows `transit.key.rotate` present
but **no** `transit.encrypt`/`decrypt` rows (data-plane isn't audited).

- [ ] **Step 2: Run to verify they fail/pass appropriately**

Run: `go test ./internal/api/ -run 'TestTransitRBAC|TestTransitLeak' -count=1 -v`
Expected: PASS (the enforcement + audit behavior is already implemented; these lock
it in). If any assertion fails, fix the handler that regressed before proceeding.

- [ ] **Step 3: Write docs**

- `docs/transit.md` — full reference: key types, the `janus:vN:` envelope,
  versioning + `min_decryption_version` + rotate + rewrap + trim, datakey
  plaintext/wrapped, the RBAC actions + the transit token scope, and the
  management-only audit policy. Derive from the spec §3–§8.
- `README.md` — add a "Transit (encryption as a service)" bullet to the Design
  section and mark it in the roadmap/repo-layout (`internal/transit/ ← implemented`).
- `status.md` — add the Milestone entry (Phase 2 · sub-project A) with scope,
  task list, and verification, mirroring the M8/M9 entries.
- `docs/operations.md` — a short transit operator flow (create key, mint a transit
  token, encrypt/decrypt, rotate + rewrap).

- [ ] **Step 4: Full gate sweep**

```bash
go build ./...
go vet ./...
go test ./... -count=1
gosec -exclude-dir=internal/crypto/shamir ./...
govulncheck ./...
```
Expected: build/vet clean; every package `ok` (Docker-backed suites run); `gosec` 0
issues (justify any new `#nosec` — e.g. the `TransitKeyAAD` `uint64(version)` G115
is bounded/positive; `internal/crypto` coverage stays 100%); `govulncheck` 0
affecting.

- [ ] **Step 5: Commit**

```bash
git add internal/api/transit_rbac_e2e_test.go internal/api/transit_leak_test.go docs/transit.md docs/operations.md status.md README.md
git commit -m "test(transit): RBAC matrix + audit + leak e2e; docs + status for the transit engine"
```

---

## Self-review notes (for the executor)

- **Spec coverage:** Ed25519 + AAD (T1); schema + token-scope migration (T2);
  TransitRepo (T3); engine skeleton/envelope (T4); create+encrypt/decrypt (T5);
  rotate/config/rewrap/trim (T6); sign/verify/datakey (T7); authz actions + token
  caps + `Resource.TransitKey` (T8); auth transit token scope (T9); management routes
  + error mapping + Boot wiring (T10); data-plane routes (T11); token mint plumbing
  (T12); RBAC/audit/leak e2e + docs + gates (T13). Every spec section maps to a task.
- **Type consistency:** `transit.Service`, `KeyMeta`, `store.TransitKey`/
  `TransitKeyVersion`, `TransitRepo` (Create/AppendVersion/UpdateConfig/TrimBelow/
  Delete/GetByName/GetByID/List), `authz.TransitRead/Use/Manage`,
  `Resource.TransitKey`, `TokenScope.Kind="transit"` are defined once and reused with
  the same signatures.
- **Green at every commit:** crypto/store/engine land before the API that calls them;
  each route batch registers in `server.go` in the same task; the token-scope
  migration (T2) precedes the auth/authz changes (T8–T9) that rely on it.
- **Verify-before-you-code reminders** are inline where the plan reuses an existing
  symbol whose exact name must be confirmed (store pool/`NewID`/pgx error mapper,
  keyring wrap/unwrap method names, `crypto.Ciphertext` marshal, auth token harness,
  api e2e login helper). Trust `go build`/`go test` over gopls "undefined"
  diagnostics for new-in-branch symbols.
