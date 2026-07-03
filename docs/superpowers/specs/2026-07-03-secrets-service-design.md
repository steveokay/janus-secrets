# Secrets Service (encryption orchestration + core CRUD) — Design Spec

**Milestone 3 (Phase 1).** Date: 2026-07-03.

## Goal

Introduce `internal/secrets`, the domain service that stitches `internal/crypto`
(the unsealed key hierarchy) to `internal/store` (crypto-blind persistence) so
that, for the first time, Janus can create a project, set a **plaintext** secret,
and read it back decrypted — through Go APIs, with full envelope encryption and
version-bound AEAD binding.

This milestone delivers the encryption orchestration and core CRUD only. It is
not yet reachable over HTTP or the CLI (those are later milestones), and it does
**not** implement config inheritance resolution, secret references, server
bootstrap/unseal wiring, or the audit log.

## Scope

**In scope:**

- New package `internal/secrets` with a `Service` façade.
- Project KEK lifecycle: generate + wrap a project KEK at project creation.
- Environment/config creation passthrough (no crypto).
- Batched secret set (`SetSecrets`) with per-value envelope encryption.
- Masked list (`ListSecrets`, `KeyHistory`) — metadata only, no decryption.
- Reveal/decrypt (`GetSecret`, `RevealConfig`, `GetSecretVersion`).
- Version operations (`ListVersions`, `DiffVersions`, `Rollback`) — crypto-free.
- Service-level error surface with sealed-state handling.
- Best-effort zeroization of all transient plaintext and key material.
- Two small `internal/store` API changes required by AAD binding (below).

**Out of scope (deferred to later specs):**

- Config inheritance *resolution* (the `inherits_from` column is carried
  unresolved, exactly as the store already does).
- Secret references (`${projects.other.prod.KEY}`) and cycle-checking.
- Server bootstrap: unseal-at-startup, `janus init` / `unseal` CLI. The service
  takes an already-unsealed `*crypto.Keyring` by injection.
- Audit log emission (this spec only *keeps the seam* — reveal lives on its own
  named methods, distinct from masked reads — so audit can wrap it later).
- Key rotation (KEK/master re-wrap) and dynamic secrets.
- HTTP handlers and CLI wiring.

## Architecture

`internal/secrets` is the first and only component that holds an unsealed key or
sees plaintext, and only transiently within a single call. It sits above the
store and crypto packages:

- `internal/store` stays crypto-blind: it persists and returns opaque
  `EncryptedValue` bytes and never holds a key or plaintext.
- `internal/crypto` stays storage-blind: it wraps/unwraps keys and
  encrypts/decrypts bytes, knowing nothing about Postgres.
- `internal/secrets` orchestrates the two and owns the plaintext boundary.

```go
type Service struct {
    st       *store.Store // retained for Store.NewID (project id pre-generation)
    projects *store.ProjectRepo
    envs     *store.EnvironmentRepo
    configs  *store.ConfigRepo
    secrets  *store.SecretRepo
    keyring  *crypto.Keyring
}

// NewService retains st and builds the repos from it.
func NewService(st *store.Store, kr *crypto.Keyring) *Service
```

The unsealed `*crypto.Keyring` is **injected**, not constructed here — a later
bootstrap milestone unseals it and hands it in. HTTP handlers (later) stay thin
and call this façade.

**Files (rough):**

- `service.go` — `Service` type + `NewService` + `zeroize` helper.
- `projects.go` — project lifecycle + KEK generation/wrap.
- `hierarchy.go` — environment/config creation passthrough.
- `secrets.go` — set / get / reveal + the envelope encrypt/decrypt.
- `versions.go` — list / diff / rollback / history.
- `errors.go` — service-level error sentinels.
- plus `*_test.go`.

## Store API changes (two, both driven by AAD binding)

The AAD for a wrapped key must bind an identifier the service knows **before**
the crypto operation runs. Two spots in the (pre-release) store currently assign
that identifier only *after* the crypto op would need it, so each gets a small
change. The store remains crypto-blind in both — it still only ever handles
opaque bytes.

### 1. `Change` carries an encrypt closure

The DEK AAD must bind the secret's `value_version`, but the store assigns that
version inside its transaction. So `store.Change` swaps its pre-built value for a
callback the store invokes with the version it just assigned:

```go
// Change is one edit within a batched save. Encrypt == nil means delete the key
// (a tombstone). Otherwise the store calls Encrypt with the value_version it
// assigns, and the callback returns the opaque encrypted value bound to that
// exact version.
type Change struct {
    Key     string
    Encrypt func(valueVersion int) (*EncryptedValue, error)
}
```

In `SecretRepo.SaveConfigVersion`, the collapse map becomes
`map[string]func(int)(*EncryptedValue,error)` (nil = delete; same last-write-wins
batch-collapse semantics). Inside the set branch, after the store computes
`nextVV` (the next `value_version` for the key), it calls the closure instead of
using a pre-built value:

```go
ev, err := encrypt(nextVV)   // encrypt bound to the just-assigned value_version
if err != nil {
    return err               // aborts the tx → rolls back, nothing persisted
}
// INSERT using ev.WrappedDEK, ev.Ciphertext, ev.Nonce, ev.DEKKeyVersion
```

The store hands the version *out* and gets ciphertext *back*; it never inspects
the bytes. A crypto failure cleanly rolls back the whole batch. The store's own
tests swap `Value: ev("x")` for
`Encrypt: func(int) (*EncryptedValue, error) { return ev("x"), nil }`.

### 2. `ProjectRepo.Create` accepts a caller-supplied id; add `Store.NewID`

The project KEK AAD is `crypto.ProjectKEKAAD(projectID)`, but a DB-default UUID
isn't known until after insert. So the service learns the id first:

- `Store.NewID(ctx) (string, error)` runs `SELECT gen_random_uuid()::text` — no
  new UUID dependency, ids stay DB-flavored.
- `ProjectRepo.Create` gains a leading `id string` parameter and inserts that id
  instead of relying on the column default.

The only current caller is the store's own `mkConfigNamed` test helper, updated
to pass an id from `NewID`.

## Component behavior

### Project & KEK lifecycle

```go
func (s *Service) CreateProject(ctx context.Context, slug, name string) (*store.Project, error)
```

1. `id := s.st.NewID(ctx)` — learn the UUID before wrapping.
2. `kek := crypto.GenerateKey()` — fresh 256-bit project KEK (plaintext, memory).
3. `wrapped, kekVer := s.keyring.WrapProjectKEK(kek, id)` — wrapped by the master
   key, AAD-bound to `ProjectKEKAAD(id)`. Returns `crypto.ErrSealed` if sealed.
4. `defer zeroize(kek)` — wipe the plaintext KEK once wrapped.
5. `s.projects.Create(ctx, id, slug, name, wrapped, kekVer)` — persist.

The plaintext project KEK exists only inside `CreateProject`, only long enough to
wrap it; it is never persisted, logged, or returned.

### Environments & configs (passthrough, no crypto)

```go
func (s *Service) CreateEnvironment(ctx, projectID, slug, name string) (*store.Environment, error)
func (s *Service) CreateConfig(ctx, envID, name string, inheritsFrom *string) (*store.Config, error)
```

These hold no keys and touch no plaintext; they delegate straight to the store
repos and exist on the façade so callers have one entry point.

### Secret set flow

```go
type SecretChange struct {
    Key    string
    Value  []byte // plaintext
    Delete bool
}

func (s *Service) SetSecrets(ctx context.Context, configID string,
    changes []SecretChange, message, actor string) (store.ConfigVersion, error)
```

1. **Resolve the KEK once per batch:** `configs.Get(configID)` →
   `envs.Get(cfg.EnvironmentID)` → `projects.Get(env.ProjectID)`. Then
   `kek := s.keyring.UnwrapProjectKEK(proj.WrappedKEK, proj.ID)` (AAD
   `ProjectKEKAAD(proj.ID)`; `ErrSealed` if sealed). `defer zeroize(kek)`.
2. **Build one `store.Change` per edit.** `Delete` → `Encrypt: nil`. Otherwise a
   closure that, given `valueVersion`:
   - `dek := crypto.NewDEK()`; `defer zeroize(dek)`
   - `aad := crypto.DEKAAD(proj.ID, configID+"/"+key, uint64(valueVersion))`
   - `nonce, ct := crypto.Encrypt(dek, change.Value, aad)`
   - wrap `dek` under `kek` → `EncryptedValue{WrappedDEK, Ciphertext: ct,
     Nonce: nonce, DEKKeyVersion: proj.KEKVersion}`
   - `zeroize(change.Value)` after encrypting.
3. `s.secrets.SaveConfigVersion(ctx, configID, storeChanges, message, actor)` —
   the store drives the closures inside its transaction, assigning each key its
   `value_version` and thereby binding each ciphertext's AAD to it.

**AAD binding:** every ciphertext is pinned to `(proj.ID, configID/key,
valueVersion)`. A ciphertext copied to another key, config, project, or version
fails to decrypt — tamper-evidence for free. Rollback reuses the same
`secret_value` row (same id, same `value_version`), so its AAD still matches on
later reads; no re-encryption.

### Reveal / decrypt path

Two read shapes, distinguished by whether plaintext is decrypted — because
revealing a value is an auditable read (later milestone) while listing masked
metadata is not.

**Masked list (no decryption, no KEK):**

```go
type SecretMeta struct {
    Key          string
    ValueVersion int
    CreatedAt    time.Time
}

func (s *Service) ListSecrets(ctx, configID string) (store.ConfigVersion, []SecretMeta, error)
```

Returns metadata only; never touches the KEK or ciphertext. This backs a masked
config/editor view.

**Reveal (decrypts):**

```go
type Secret struct {
    Key          string
    Value        []byte // plaintext
    ValueVersion int
}

func (s *Service) GetSecret(ctx, configID, key string) (Secret, error)
func (s *Service) RevealConfig(ctx, configID string) (store.ConfigVersion, map[string]Secret, error)
func (s *Service) GetSecretVersion(ctx, configID, key string, valueVersion int) (Secret, error)
```

Decrypt flow (KEK unwrapped once per call, even for a whole config):

1. Resolve `configID → env → project`; read the live `store.SecretValue` for the
   key via `GetLatest`/`GetVersion` (or `GetKeyHistory` for `GetSecretVersion`).
   Missing key/version → `ErrNotFound`.
2. `kek := s.keyring.UnwrapProjectKEK(proj.WrappedKEK, proj.ID)`;
   `defer zeroize(kek)` (`ErrSealed` if sealed).
3. `dek := unwrap sv.WrappedDEK under kek`; `defer zeroize(dek)`.
4. `aad := crypto.DEKAAD(proj.ID, configID+"/"+key, uint64(sv.ValueVersion))` —
   identical construction to the set path.
5. `plaintext := crypto.Decrypt(dek, sv.Nonce, sv.Ciphertext, aad)`.

`GetSecret`/`RevealConfig`/`GetSecretVersion` are the audit seam: the audit
milestone wraps exactly these to emit "secret revealed" events. They take the
same `ctx` (which will carry actor identity once auth exists). This spec does not
implement audit; it only keeps reveal on its own clearly-named methods, distinct
from `ListSecrets`.

### Version operations (crypto-free)

```go
func (s *Service) ListVersions(ctx, configID string) ([]store.ConfigVersion, error)
func (s *Service) DiffVersions(ctx, configID string, vA, vB int) (store.Diff, error)
func (s *Service) KeyHistory(ctx, configID, key string) ([]SecretMeta, error) // masked
func (s *Service) Rollback(ctx, configID string, targetVersion int, message, actor string) (store.ConfigVersion, error)
```

- `ListVersions` / `DiffVersions` delegate straight to the store (diff compares
  by `(key, secret_value_id)` — no plaintext).
- `KeyHistory` returns masked metadata (when a key changed, not its values).
- `Rollback` delegates to `store.Rollback`, which repoints the manifest at the
  target version's existing `secret_value` rows. Because each row's AAD is pinned
  to its own `value_version`, those rows still decrypt after rollback. No
  re-encryption; the service adds nothing crypto-side.

## Error handling

The service defines its own sentinels and maps inward errors to them, so callers
never see pgx or crypto internals:

```go
var (
    ErrSealed     = errors.New("secrets: server is sealed")
    ErrNotFound   = errors.New("secrets: not found")
    ErrConflict   = errors.New("secrets: conflict")
    ErrValidation = errors.New("secrets: invalid input")
    ErrDecrypt    = errors.New("secrets: decryption failed")
)
```

- `crypto.ErrSealed` from any keyring call → `ErrSealed` (future API → HTTP 503).
- `store.ErrNotFound` / `store.ErrConflict` → the service's `ErrNotFound` /
  `ErrConflict` (matched with `errors.Is`).
- A **failed AEAD decrypt** on data that was found → `ErrDecrypt`, *not*
  `ErrNotFound`. A decrypt failure on present data is an integrity signal
  (tampered/relocated ciphertext, wrong key), and must be distinguishable. Its
  message carries no plaintext or key material.
- Input validation at the boundary (key names, slugs, identifiers) → `ErrValidation`
  before any store/crypto call (defense-in-depth; the store already parameterizes
  all SQL).

## Zeroization

One internal `zeroize([]byte)` helper (`for i := range b { b[i] = 0 }`, guarded
with `runtime.KeepAlive`) wipes every transient plaintext and key: plaintext KEKs
(create), unwrapped KEKs (set/get batches), DEKs (per value), and caller-supplied
plaintext values after encryption. `defer`-scheduled at each allocation site.

This is **best-effort defense-in-depth, not a guarantee** — Go's GC may have
already copied heap data. No caching of unwrapped KEKs or DEKs: they are unwrapped
per operation and wiped immediately, minimizing plaintext-key lifetime. Caching is
deliberately deferred (revisit only if profiling demands it).

## Testing

Integration-style tests against real Postgres via the store package's existing
testcontainers harness (the versioning logic cannot be faithfully faked), paired
with a test-unsealed `crypto.Keyring` built from an in-memory master key.
Table-driven throughout.

1. **Round-trip:** create project/env/config, `SetSecrets`, `GetSecret` returns
   exact plaintext; `RevealConfig` returns all keys; multi-key batch in one version.
2. **Batch semantics:** set-then-delete of a key in one call collapses to absent;
   mixed set + delete yields the right live set.
3. **Version ops:** several saves → `ListVersions` count/order; `DiffVersions`
   added/changed/removed; `GetSecretVersion` decrypts a historical value (proves
   per-`value_version` AAD); `KeyHistory` returns masked metadata only.
4. **Rollback reuses ciphertext:** set v1, change to v2, `Rollback` to v1 →
   `GetSecret` returns v1 plaintext and decrypts cleanly (AAD survives rollback,
   no re-encryption).
5. **Integrity binding (tamper):** swap a stored `secret_value`'s ciphertext with
   another key's (or bump its `value_version`) → `GetSecret` surfaces `ErrDecrypt`,
   never a silent wrong value, never a plaintext leak in the error.
6. **Sealed state:** with a sealed keyring, `CreateProject` / `SetSecrets` /
   `GetSecret` all return `ErrSealed`.
7. **Error mapping:** missing key → `ErrNotFound`; save to soft-deleted config →
   `ErrConflict`; invalid key/slug → `ErrValidation`.
8. **Leak test (CLAUDE.md-mandated):** capture logs + every returned error string
   across set/get/reveal and assert no secret value or key material appears.
   `internal/secrets` is the first place plaintext lives, so this is the
   enforcement point for "zero plaintext in logs/errors."
9. **`zeroize` unit test:** asserts the helper zeroes a buffer (the best-effort
   caveat is documented, not asserted).

The masked-vs-reveal distinction is also enforced structurally: `SecretMeta`
carries no value field, so masked reads cannot leak plaintext even by mistake.

## Verification gates

`go build`, `go vet`, `go test ./...` (crypto + store + secrets via
testcontainers), `gosec` (0 issues), `govulncheck` (0) must all pass, matching
the milestone-2 bar. Toolchain stays pinned at `go1.26.4`.
