# Phase 2 · Sub-project A — Transit Engine Design

**Status:** approved (brainstorming), pending implementation plan
**Package:** `internal/transit` (new) + `internal/crypto` (Ed25519 helpers) +
`internal/store` (TransitRepo, migration `000006`) + `internal/api`
(`/v1/transit/*`)
**Phase:** 2 (Transit + UI). This is the first Phase-2 sub-project — the backend
"encryption as a service" engine, with no UI dependency.

## Phase 2 decomposition (context)

Phase 2 spans four largely independent subsystems, each getting its own
spec → plan → implementation cycle:

- **A. Transit engine** (this spec) — depends only on already-shipped layers.
- **B. React SPA** — depends on the full REST API (incl. transit).
- **C. OIDC login** — auth/api; the SPA consumes it.
- **D. Usage metrics** — daily aggregates over audit events; feeds the SPA.

CLAUDE.md's phase order puts the transit engine first, and it's the only piece
with zero UI dependency.

## 1. Goal

A Vault-style transit secrets engine: Janus holds **named keys** and performs
encrypt / decrypt / sign / verify / rewrap on data it never stores, plus key
lifecycle (rotate, versioning, `min_decryption_version`, trim) and datakey
generation. Keys are instance-scoped and never leave the server in plaintext.

## 2. Architecture & placement

- **`internal/transit`** — a focused engine reusing `internal/crypto`
  (`Encrypt`/`Decrypt`, `GenerateKey`, `WrapKey`/`UnwrapKey`) and the unsealed
  `Keyring` master key. **New crypto helpers** in `internal/crypto` (stdlib
  `crypto/ed25519`, not hand-rolled primitives): `GenerateEd25519Key`, `Sign`,
  `Verify`, plus a `TransitKeyAAD(name string, version int) []byte` helper
  alongside the existing AAD helpers.
- **`store.TransitRepo`** — crypto-blind persistence of keys/versions (stores
  only wrapped material + public keys + metadata).
- **`internal/api`** — handlers under `/v1/transit/*`, behind `RequireAuth` +
  `RequireUnsealed` + RBAC.
- **Instance-scoped** global keys. **Sealed → 503**: every op needs the master
  key to unwrap version material, so all transit routes sit behind
  `RequireUnsealed`.

The engine stays HTTP-free and identity-free (mirrors `internal/secrets`); the
API layer owns authz + audit.

## 3. Key model

Two key types, each fixing its allowed operations:

- **`aes256-gcm`** → encrypt / decrypt / rewrap / datakey
- **`ed25519`** → sign / verify

A wrong-type operation (encrypt on an ed25519 key, sign on an aes key) →
`ErrWrongKeyType` → 400.

A key has **versions**:

- `latest_version` — used for new encrypt/sign and as the rewrap/datakey target.
- `min_decryption_version` — floor; decrypt/verify against a version below it is
  rejected (`ErrVersionTooOld`). Default 1.
- `deletion_allowed` — must be true before a key can be destroyed (Vault-style
  guard). Default false.

**Rotate** appends `v(latest+1)` with fresh material and bumps `latest_version`.
**Trim** permanently deletes versions below a supplied `min_available_version`,
which must not exceed `min_decryption_version` (you cannot trim a version you
still permit for decryption) — else `ErrValidation` → 400.

## 4. Storage (migration `000006`)

```sql
CREATE TABLE transit_keys (
  id                     uuid PRIMARY KEY,
  name                   text NOT NULL UNIQUE,
  key_type               text NOT NULL,          -- 'aes256-gcm' | 'ed25519'
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
  wrapped_material bytea NOT NULL,   -- version key material wrapped by the master key
  public_key       bytea,           -- ed25519 public key in clear (verify without unwrap)
  created_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (transit_key_id, version)
);
```

Each version's key material is wrapped by the **master key** under
`TransitKeyAAD(name, version)`, so a version row copied to a different key/version
fails to unwrap — the same anti-swap property project KEKs have. Ed25519 public
keys are stored in clear so `verify` needs no unwrap; the private seed is wrapped.

## 5. Envelope format & operations

- **Ciphertext:** `janus:v<N>:<base64(nonce‖ciphertext)>`. **Signature:**
  `janus:v<N>:<base64(sig)>`. The `v<N>` prefix identifies the key version on the
  way back in. A malformed envelope → `ErrBadCiphertext` → 400.
- **encrypt** — encrypt the plaintext directly with the `latest_version` key
  material via `crypto.Encrypt(material, plaintext, associatedData)`. Optional
  client `associated_data` (base64) is bound as AEAD AAD (must match on decrypt).
- **decrypt** — parse `v<N>`; reject if `N < min_decryption_version`; unwrap that
  version's material; `crypto.Decrypt`. Returns base64 plaintext. Any failure
  (bad version, wrong AAD, tampered bytes) → generic `ErrBadCiphertext`.
- **sign** (ed25519) — sign the input bytes with the `latest_version` private key
  → `janus:v<N>:<sig>`. **verify** — parse the version, verify with that version's
  public key → `{valid: bool}` (a bad signature is `valid:false`, not an error).
- **rewrap** — decrypt an old ciphertext (if `≥ min_decryption_version`) and
  re-encrypt under `latest_version` → new `janus:v<N>:…`. **Plaintext is never
  returned.** This rolls data forward after rotation without exposing it.
- **datakey** — generate a fresh random 256-bit DEK; return it **wrapped**
  (encrypted under the key's latest version) and, in the explicit plaintext mode,
  the raw DEK for client-side envelope encryption. Two endpoints
  (`/datakey/plaintext/{name}`, `/datakey/wrapped/{name}`) so plaintext exposure
  is deliberate, never accidental. Aes keys only.
- **Management:** rotate / trim / update-config (`min_decryption_version`,
  `deletion_allowed`) / delete / read-key-metadata / list-keys.

## 6. AuthZ, token scope, audit

- **Actions** (both instance-scoped): `transit:manage`
  (create/rotate/trim/update-config/delete) and `transit:use`
  (encrypt/decrypt/sign/verify/rewrap/datakey). Role mapping: instance
  **owner/admin** → manage + use; instance **developer** → use; **viewer** →
  read key metadata / list only.
- **New service-token scope `transit`:** mint `--scope transit --access
  use|manage [--key <name>]`. The authz engine maps a transit-scoped token to
  `transit:use`/`transit:manage` **only** — never secrets/config or other
  instance actions. An optional key restriction narrows it to a single key name;
  otherwise it covers all transit keys. This extends the existing token
  scope+access model (which today maps config/env scope to secret/config
  capabilities) with a transit branch; existing tokens are unaffected.
- **Audit:** management ops emit `transit.key.create|rotate|trim|config|delete`
  (fail-closed via the M7 `s.record` seam, recording the key **name** and never
  material); all denials are captured centrally by `s.authorize`. Data-plane ops
  (encrypt/decrypt/sign/verify/rewrap/datakey) are **not** individually audited —
  usage visibility is deferred to the Phase-2 usage-metrics sub-project. This
  matches how M7 already treats masked reads (no audit) and avoids serializing
  transit throughput on the audit advisory lock.

## 7. API surface

All under `/v1/transit/`, JSON, `Authorization: Bearer <token>` or session cookie,
behind `RequireAuth` + `RequireUnsealed`.

```
POST   /v1/transit/keys                {name, type}               → 201 metadata
GET    /v1/transit/keys                                           → {keys:[metadata]}
GET    /v1/transit/keys/{name}                                    → metadata
POST   /v1/transit/keys/{name}/rotate                             → metadata (new latest_version)
POST   /v1/transit/keys/{name}/config  {min_decryption_version?, deletion_allowed?}  → metadata
POST   /v1/transit/keys/{name}/trim    {min_available_version}    → metadata
DELETE /v1/transit/keys/{name}                                    → 204 (requires deletion_allowed)
POST   /v1/transit/encrypt/{name}      {plaintext, associated_data?}  → {ciphertext}
POST   /v1/transit/decrypt/{name}      {ciphertext, associated_data?} → {plaintext}
POST   /v1/transit/sign/{name}         {input}                    → {signature}
POST   /v1/transit/verify/{name}       {input, signature}         → {valid}
POST   /v1/transit/rewrap/{name}       {ciphertext}               → {ciphertext}
POST   /v1/transit/datakey/plaintext/{name}                       → {ciphertext, plaintext}
POST   /v1/transit/datakey/wrapped/{name}                         → {ciphertext}
```

`plaintext`, `input`, `associated_data` are base64 in request/response bodies.
Metadata responses (`{name, type, latest_version, min_decryption_version,
deletion_allowed, versions:[{version, created_at}], created_at}`) never include
key material or wrapped bytes.

## 8. Error handling

Reuse the `{"error":{code,message}}` envelope and extend `writeServiceError`:

| Sentinel | HTTP | Meaning |
|---|---|---|
| `ErrKeyNotFound` | 404 | no such transit key |
| `ErrKeyExists` | 409 | create with a name in use |
| `ErrWrongKeyType` | 400 | op not valid for the key type |
| `ErrVersionTooOld` | 400 | ciphertext version `< min_decryption_version` |
| `ErrBadCiphertext` | 400 | malformed envelope / failed AEAD (generic) |
| `ErrDeletionNotAllowed` | 409 | delete without `deletion_allowed` |
| `ErrSealed` | 503 | server sealed |

Decrypt/verify failures are **generic** — never reveal whether the cause was a
bad key, wrong version, or tampered ciphertext beyond the coarse code. No key
material, wrapped bytes, or plaintext appears in any error or log. Boundary
validation: base64 decode errors → 400; key names validated for charset/length
like slugs.

## 9. Testing

- **`internal/crypto`:** Ed25519 `GenerateEd25519Key`/`Sign`/`Verify` held to the
  package's 100% branch-coverage bar (tampered-signature, wrong-key,
  malformed-key cases). `TransitKeyAAD` injectivity test.
- **`internal/transit` (pure, table-driven):** encrypt→decrypt round-trip; rotate
  then decrypt an old-version ciphertext still works; `min_decryption_version`
  blocks an old ciphertext; rewrap upgrades version without exposing plaintext;
  wrong-key-type rejection; trim removes archived versions; datakey
  wrapped/plaintext; tamper (flip a ciphertext byte → `ErrBadCiphertext`);
  associated-data mismatch fails closed; sealed → `ErrSealed`.
- **`internal/store` (testcontainers):** TransitRepo create/get/list/update,
  version append, cascade delete, unique-name and unique-(key,version)
  constraints.
- **`internal/api` (e2e):** full lifecycle over HTTP (create → encrypt → rotate →
  rewrap → decrypt → trim → delete); RBAC matrix (viewer denied use; developer
  can use but not manage; a transit token restricted to its key is denied another
  key; a secrets-scoped token is denied transit); management ops audited and a
  `transit.key.rotate` present in the chain; **leak test** — no key material,
  wrapped bytes, or plaintext in captured logs or error bodies.
- Full gate sweep unchanged: `go build`/`go vet`/`go test ./...`, `gosec`
  (`-exclude-dir=internal/crypto/shamir`), `govulncheck` — all as build failures.

## 10. Non-goals (this sub-project)

- Key export / backup-restore (exfiltration surface — rejected in brainstorming).
- HMAC/hash helper endpoints (apps can do locally).
- Convergent/derived keys, key-derivation `context`, RSA/ECDSA or other key types.
- Project-scoped transit keys (instance-only for v1; revisit if a real
  multi-project isolation need appears).
- The React UI for transit (Phase-2 sub-project B) and the usage-metrics
  aggregates (sub-project D) — the audit decision here deliberately leaves
  data-plane usage visibility to D.
