# Design: Repo Scaffold + Crypto Layer (Milestone 1)

**Date:** 2026-07-02
**Status:** Approved
**Project:** Keyhaven (repo: `github.com/steveokay/janus-secrets`)

## Goal

First milestone of Phase 1: a repository where `make test` passes, CI is green,
and `internal/crypto` is complete, fully tested, and ready for the store layer
to build on. No HTTP server, no Postgres access, no CLI logic in this milestone.

## Decisions made during brainstorming

1. **Milestone scope:** repo scaffold + full crypto layer (follows the CLAUDE.md
   Phase 1 ordering).
2. **Module path:** `github.com/steveokay/janus-secrets`.
3. **Shamir source:** vendor HashiCorp Vault's `shamir` package (~200 lines,
   MPL-2.0) into `internal/crypto/shamir/` with license header intact. No new
   go.mod dependency for it; we do not hand-roll GF(2^8) arithmetic.
4. **Architecture:** pure, storage-blind crypto library (Approach A).
   `internal/crypto` knows nothing about Postgres; it exposes value types, pure
   operations, the `Unsealer` interface, and an in-memory `Keyring`. The store
   layer (next milestone) persists wrapped blobs it treats as opaque.

## Repo scaffold

- `go.mod` — `github.com/steveokay/janus-secrets`, latest stable Go.
- `cmd/keyhaven/main.go` — minimal entrypoint (prints version, exits) so the
  binary compiles in CI from day one. Real server wiring comes with the API
  milestone.
- `Makefile` — working `test`, `lint`, `build` targets; `dev` and `migrate`
  stubs that exit with "not yet implemented".
- `.github/workflows/ci.yml` — `go build`, `go vet`, `go test -race`,
  `govulncheck`, `gosec`. All findings fail the build. Enforces the coverage
  floor on `internal/crypto`.
- `.gitignore` for Go + editor artifacts.
- `docker-compose.yml` — Postgres 16 service only; app service added when
  there is an app to run.
- Other `internal/*` directories are NOT pre-created; each lands with its
  own milestone.

## `internal/crypto` package

### Layout

```
internal/crypto/
├── aead.go        # AES-256-GCM encrypt/decrypt primitives
├── keys.go        # key types, generation, wrap/unwrap
├── keyring.go     # in-memory post-unseal state machine
├── unseal.go      # Unsealer interface + seal-state types
├── shamir.go      # Shamir Unsealer implementation
├── shamir/        # vendored HashiCorp shamir (MPL-2.0 header intact)
└── kms.go         # AWS KMS Unsealer implementation
```

### Core types and operations

- `Ciphertext{Nonce, Data []byte, KeyVersion uint32}` — nonce stored alongside
  ciphertext; serializes to a single opaque blob for storage.
- `Encrypt(key, plaintext, aad) (Ciphertext, error)` / `Decrypt(...)` —
  AES-256-GCM, 12-byte nonces from `crypto/rand`, fail closed on any error.
- **AAD binding everywhere:** wrapping a project KEK binds the project ID into
  the AAD; wrapping a DEK binds project ID + secret path + version. A
  ciphertext moved to a different row fails to decrypt (prevents wrapped-key
  swap attacks).
- `WrapKey` / `UnwrapKey` — same AEAD, specialized for 32-byte key material.

### Keyring (the only stateful piece)

- States: `Sealed → Unsealed`, transitions guarded by a mutex. Every operation
  on a sealed keyring returns `ErrSealed` (the API layer later maps this
  to HTTP 503).
- Post-unseal it holds the master key and exposes: `WrapProjectKEK`,
  `UnwrapProjectKEK`, `NewDEK` (generate + wrap in one call to minimize
  plaintext DEK lifetime), `Seal()` (zeroize and return to sealed).
- Master-key zeroization on seal is best-effort (Go's GC may have copied the
  bytes); documented as such.

### Unsealer

```go
type Unsealer interface {
    Init(ctx)   // first boot: generate master key, return shares (or nothing)
    Unseal(ctx) // recover master key
}
```

- **Shamir:** `Init` generates the master key, splits k-of-n (default 3-of-5,
  configurable), returns shares exactly once. Share submission is specific to
  the Shamir implementation: its concrete type exposes
  `SubmitShare(share) (progress, error)` (e.g. 2/3 submitted), and `Unseal`
  succeeds once the threshold is reached — callers needing interactive
  submission hold the concrete `*ShamirUnsealer`, not the interface. After
  reconstruction the result is verified
  against a stored **key check value** (a known constant encrypted under the
  master key at init) so a wrong-but-valid reconstruction is rejected.
- **AWS KMS:** master key generated locally at init, encrypted via
  `kms:Encrypt`, wrapped blob persisted; `Unseal` calls `kms:Decrypt`. The AWS
  client hides behind a 2-method `KMSClient` interface so tests use a fake.
  `aws-sdk-go-v2` is the single new dependency (not a crypto library — KMS
  performs its crypto server-side).
- Persistence of the wrapped master key / KCV is abstracted behind a tiny
  `SealConfigStore` interface. A file-based implementation ships now (used by
  tests and single-binary bootstrap); a Postgres implementation arrives with
  the store milestone.

## Error handling

- Exported sentinels: `ErrSealed`, `ErrDecryptFailed`, `ErrInvalidShare`,
  `ErrNotEnoughShares`, `ErrKeyCheckFailed`, `ErrAlreadyUnsealed`.
- No key material, plaintext, or share bytes ever appear in an error string.
  GCM's underlying error is swallowed and returned as bare `ErrDecryptFailed`.
- Fail closed: short ciphertext, bad nonce length, AAD mismatch — all are
  `ErrDecryptFailed`, never a partial result.
- A unit test asserts every error path's message against a leak regex (seeds
  the project-wide grep-based leak test required by CLAUDE.md).

## Test plan

100% branch coverage on `internal/crypto`, enforced in CI. Table-driven
throughout.

- **AEAD:** round-trip; wrong key; wrong AAD; truncated/empty ciphertext;
  tamper cases — bit-flips in nonce, ciphertext body, and GCM tag each
  rejected.
- **Nonce uniqueness:** ~100k generated nonces with no collision; two
  encryptions of identical plaintext produce different ciphertexts.
- **Shamir:** k-of-n round-trips (2/3, 3/5, 5/5); wrong share →
  `ErrKeyCheckFailed`; duplicate shares; too few shares; share tampering.
- **KMS:** fake client covering success, decrypt failure, context
  cancellation.
- **Keyring:** sealed-state rejection of every operation; double-unseal;
  seal-then-unseal cycle; concurrent access under `-race`.

## Out of scope for this milestone

- HTTP endpoints (including unseal endpoints), Postgres store, migrations,
  auth, audit, CLI behavior beyond a version print.
- Key rotation orchestration (lazy DEK re-wrap, master-key rotation) — the
  primitives here enable it; the orchestration lands with the store layer.
