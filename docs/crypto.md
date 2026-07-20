# Cryptography

**Package:** `internal/crypto`. **Status:** implemented, held to 100% statement
coverage in CI. This document explains how envelope encryption, key wrapping,
the in-memory keyring, and unseal actually work.

The package is a pure, **storage-blind** library: value types and pure
functions for the primitives, one stateful component (the `Keyring`), and an
`Unsealer` interface with two implementations. It never touches Postgres or the
filesystem except through the tiny `SealConfigStore` interface.

## Primitives

All symmetric encryption is **AES-256-GCM** (`KeySize = 32`, `NonceSize = 12`).

```go
Encrypt(key, plaintext, aad []byte) (Ciphertext, error)
Decrypt(key []byte, ct Ciphertext, aad []byte) ([]byte, error)
```

A `Ciphertext` bundles the GCM output with its nonce and the version of the key
that produced it:

```go
type Ciphertext struct {
    KeyVersion uint32
    Nonce      []byte
    Data       []byte // includes the GCM auth tag
}
```

- **Nonces are random per encryption** and never reused; encrypting identical
  plaintext twice yields different ciphertext (tested over 100k iterations for
  uniqueness).
- **Decrypt fails closed.** Wrong key, wrong AAD, tampered nonce/body/tag,
  truncated or malformed input — all return the single sentinel
  `ErrDecryptFailed` with no detail. A wrong key size returns
  `ErrInvalidKeySize`.
- `Marshal()` / `ParseCiphertext()` serialize a ciphertext as
  `formatVersion(1) | keyVersion(4, big-endian) | nonce(12) | data`. Malformed
  blobs parse to `ErrDecryptFailed`.

## The key hierarchy (envelope encryption)

Three levels, so no single stored value can decrypt a secret on its own:

```
Master key (root KEK)  — 256-bit, in memory only after unseal, never persisted plaintext
      │  wraps
      ▼
Project KEK            — one per project, stored wrapped in Postgres
      │  wraps
      ▼
DEK                    — one per secret value version, AES-256-GCM encrypts the value
```

```go
GenerateKey() ([]byte, error)                              // 32 random bytes
WrapKey(wrappingKey, keyMaterial, aad []byte) (Ciphertext, error)
UnwrapKey(wrappingKey []byte, ct Ciphertext, aad []byte) ([]byte, error)
```

`WrapKey` rejects non-32-byte material; `UnwrapKey` additionally verifies the
*unwrapped* result is key-sized and zeroizes it on mismatch, so a decryption
that authenticates but yields the wrong length is still rejected.

## AAD binding — defeating wrapped-key-swap

Every wrapped key is bound to *where it lives* using GCM's additional
authenticated data. A ciphertext copied onto another row fails to unwrap because
the AAD no longer matches.

```go
ProjectKEKAAD(projectID string) []byte
DEKAAD(projectID, secretPath string, version uint64) []byte
```

The AAD encoding is **length-prefixed** (`appendField` writes a `uint64` length
before each field), so it is injective even when project IDs or secret paths
contain the delimiter characters — `DEKAAD("p1", "a:b", 1)` can never collide
with `DEKAAD("p1:a", "b", 1)`. The KEK and DEK families use distinct prefixes so
they can never overlap. This is tested explicitly (`TestAADInjective`).

## The keyring

The `Keyring` is the only stateful piece: it holds the master key in memory
after unseal and is safe for concurrent use (`sync.RWMutex`).

```go
k := NewKeyring()                       // starts sealed
k.Unseal(master)                        // installs master key (copies the slice)
k.Sealed()                              // true iff no master key
k.WrapProjectKEK(kek, projectID)        // wrap a project KEK under master
k.UnwrapProjectKEK(ct, projectID)       // unwrap it
k.NewDEK(projectKEK, aad)               // generate + wrap a DEK in one call
k.Seal()                                // zeroize master, return to sealed
```

- Every operation on a **sealed** keyring returns `ErrSealed` (the API layer
  maps this to HTTP 503). `Unseal` twice returns `ErrAlreadyUnsealed`.
- `Unseal` **copies** the caller's slice, so the caller can (and should) zero
  its own copy afterward without breaking the keyring.
- `NewDEK` generates and wraps in one call to minimize the plaintext DEK's
  lifetime, zeroizing it if wrapping fails.
- `Seal` best-effort zeroizes the master key. (Go's GC may have copied bytes;
  this narrows the window, it does not guarantee erasure — an inherent Go
  limitation, documented in the code.)

## Unseal

The server starts sealed. An `Unsealer` recovers and **verifies** the master key
at startup:

```go
type Unsealer interface {
    Init(ctx) (*InitResult, error)   // first boot: generate master key + persist seal metadata
    Unseal(ctx) ([]byte, error)      // later boots: recover + verify the master key
}
```

Non-secret seal metadata is persisted via `SealConfigStore` as a `SealConfig`
(seal type, Shamir threshold/shares, key-check value, and — for KMS — the
wrapped master key). A `FileSealConfigStore` exists for bootstrap and tests; the
Postgres-backed store lands with the store milestone.

### Key check value (KCV)

At `Init`, a known constant (`"janus-key-check-v1"`) is encrypted under the
master key and stored as the KCV. On unseal, the recovered key must decrypt the
KCV to that exact constant, else `ErrKeyCheckFailed`. This rejects a
**wrong-but-well-formed** master key — for example a Shamir reconstruction from
a wrong share — *before* it is ever used to touch real data. The final compare
is constant-time (defense-in-depth; the constant is public).

### Shamir (interactive, k-of-n)

The master key is split with Shamir's Secret Sharing (default **3-of-5**,
configurable) using a vendored copy of HashiCorp Vault's implementation.

```go
u := NewShamirUnsealer(store, shares, threshold)
res, _ := u.Init(ctx)                 // returns res.Shares — shown to the operator ONCE
// ... later, at startup ...
p, _ := u.SubmitShare(ctx, share)     // p.Received / p.Required progress
master, _ := u.Unseal(ctx)            // once threshold shares are in
u.Reset()                             // discard submitted shares (e.g. after a bad share)
```

Operators submit shares one at a time until the threshold is met; duplicate or
malformed shares are rejected (`ErrDuplicateShare` / `ErrInvalidShare`), and
`Reset()` recovers from a poisoned submission without restarting.

### AWS KMS (automatic)

The master key is wrapped by a cloud KMS key and recovered automatically at
startup with a single decrypt call — no operator interaction.

```go
client := NewAWSKMSClient(api, keyID) // adapter over aws-sdk-go-v2 KMS
u := NewKMSUnsealer(store, client)
res, _ := u.Init(ctx)                 // wraps a fresh master key via KMS, stores it
master, _ := u.Unseal(ctx)            // KMS-decrypts the stored wrapped master key
```

KMS is used purely as an *encrypt/decrypt service* (crypto happens server-side
at AWS); it is the only third-party crypto-adjacent dependency, and it sits
behind a fakeable `KMSClient` interface so tests need no real AWS.

> **Init locking asymmetry (documented in code):** the two unsealers differ in
> how they guard first-boot initialization; see comments in `unseal.go` /
> `kms.go`. This is intentional and noted so future readers don't "fix" it.

## Key rotation

Rotation re-wraps key material; it never decrypts a secret value. Two
independent operations, each in its own package above `internal/crypto`:

### Project KEK rotation (`internal/projectkeys`)

- **Rotate** installs a fresh project KEK as a new version (`projects.kek_version`
  incremented); the superseded wrapped KEK is preserved in
  `project_kek_versions`. Existing DEKs stay wrapped under the old KEK — reads
  stay correct because each DEK row carries its own `dek_key_version`.
- **Rewrap** is a resumable, batched sweep (`rewrapBatchSize = 200`, keyset-
  paginated so a crash loses at most one in-flight batch) that walks every DEK
  still wrapped under a superseded KEK version, unwraps it under its old KEK,
  and re-wraps it under the latest KEK. It touches only 32-byte DEKs — the
  sweep's row type carries no ciphertext/nonce, so a secret value is
  structurally unreachable from this code path. Superseded KEK versions with
  no remaining DEK references are retired (deleted) at the end of a sweep.
- Both are owner-only (`kek:manage`), exposed as `POST
  /v1/projects/{pid}/kek/rotate`, `POST /v1/projects/{pid}/kek/rewrap`, `GET
  /v1/projects/{pid}/kek` (status), and `janus project rotate-kek / rewrap /
  kek-status <project-id>`.

### Master-key rotation (`internal/masterkeys`)

Rotating the master key re-wraps **every** master-wrapped blob — project KEKs
(current + superseded versions), the auth token-HMAC key, OIDC client secrets,
and transit key material — under a freshly generated master, in one DB
transaction, then swaps the in-memory master and re-seals. `Keyring.RotateMaster`
enforces atomicity: it takes an `unwrap`/`wrap` pair and a `persist` closure: the
in-memory master is only swapped if **both** the rewrap and the persist commit
succeed; on either failure the old master is retained unchanged (never a
half-rotated keyring).

- **KMS-unsealed instances** rotate in a single call (`Rotate`) — no operator
  interaction, mirroring `Unsealer.Reseal`'s single KMS round-trip.
- **Shamir-sealed instances** require an interactive rekey ceremony (`RekeyInit`
  / `RekeySubmit` / `RekeyCancel`): operators submit proof-of-possession shares
  of the *current* master (same threshold as unseal) before a new share set is
  minted and shown once, analogous to `Init`. `Rotate` on a Shamir seal returns
  `ErrShamirCeremonyRequired` rather than silently falling back.
- Owner-only, exposed under `/v1/sys/master-key/*` and the `janus master-key`
  CLI; `seal_config.master_key_version` / `master_key_rotated_at` (migration
  `000019`) track rotation state for the "chain verified"-style status surface.

## Error & logging discipline

- No key material, plaintext, or share bytes ever appear in an error message.
  Every error is one of the exported sentinels (`ErrSealed`, `ErrDecryptFailed`,
  `ErrKeyCheckFailed`, `ErrInvalidShare`, …) or an OS/SDK error that cannot
  contain key bytes. A dedicated test (`TestNoSecretsInErrorMessages`) asserts
  this over captured output.
- Constant-time comparison for the key-check value.
- 100% statement coverage is enforced in CI, including the nonce-uniqueness,
  tamper (modified-ciphertext), and secret-leak cases. To make otherwise
  unreachable error branches testable, the package uses two injection points —
  `randReader` and `aeadForKey` — that tests override and restore.
