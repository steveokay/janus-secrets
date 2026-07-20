# Transit: encryption as a service

The transit engine lets applications encrypt, decrypt, sign, verify, rewrap, and
mint data keys against **named keys that Janus holds but whose material never
leaves the server in plaintext** — the Vault "transit secrets engine" model. The
app sends data, Janus returns a result; Janus stores keys, the app stores
ciphertext. This is distinct from the secret store (project → environment →
config → secret): transit keys are **instance-scoped**, have no hierarchy, and
Janus never persists the plaintext or ciphertext you pass through them.

Package `internal/transit` is a pure, HTTP-free, identity-free engine (mirroring
`internal/secrets`): it reuses `internal/crypto` (AEAD encrypt/decrypt, key
wrap/unwrap, the unsealed master key) and a crypto-blind `store.TransitRepo`
(wrapped material + public keys + metadata only). The API layer owns
authorization and audit.

## Key model

Two key types, each fixing its allowed operations:

| Type | Operations | Material |
|---|---|---|
| `aes256-gcm` | encrypt / decrypt / rewrap / datakey | 256-bit AES key per version |
| `ed25519` | sign / verify | Ed25519 keypair per version (private seed wrapped, public key stored in clear) |

A wrong-type operation — encrypt on an `ed25519` key, sign on an `aes256-gcm`
key — is rejected with `ErrWrongKeyType` (400). Ed25519 public keys are stored
unwrapped so `verify` needs no unseal-time unwrap; the private seed is wrapped
like any other version material.

### Versions

A key has one or more **versions**, and three pieces of version policy:

- **`latest_version`** — the target for new `encrypt`/`sign`, and for
  `rewrap`/`datakey`. Starts at 1.
- **`min_decryption_version`** — the floor for `decrypt`/`verify`. A ciphertext
  or signature whose version is below this is rejected with `ErrVersionTooOld`
  (400). Default 1.
- **`deletion_allowed`** — a key cannot be deleted until this is set true
  (a Vault-style guard against accidental destruction). Default false.

**Rotate** appends `v(latest+1)` with fresh material and bumps `latest_version`;
old versions stay so previously-encrypted data still decrypts. **Trim**
permanently deletes every version below a supplied `min_available_version`, which
must not exceed `min_decryption_version` (you cannot trim a version you still
permit for decryption) — otherwise `ErrValidation` (400).

## Envelope format

Both ciphertext and signature are returned as:

```
janus:v<N>:<base64(payload)>
```

where `<N>` is the key version used, so the decrypt/verify path can select the
right version on the way back in. For ciphertext the payload is
`nonce ‖ ciphertext` (AES-256-GCM, random nonce per operation); for a signature
it is the raw Ed25519 signature bytes. A malformed envelope → `ErrBadCiphertext`
(400).

## At-rest wrapping (anti-swap)

Each version's key material is wrapped by the **master key** under
`TransitKeyAAD(name, version)` — a domain-tagged, length-prefixed, injective
binding of the key name and version. A version row copied to a different key or
version therefore fails to unwrap, the same wrapped-key-swap defense the project
KEKs and DEKs have. The store only ever holds wrapped material (and, for
ed25519, the clear public key); it never sees a plaintext key.

## Operations

All bodies are JSON; binary fields (`plaintext`, `input`, `associated_data`, and
the returned `plaintext`) are **base64**. Ciphertext and signature travel as the
`janus:v<N>:…` string.

- **encrypt** — encrypts with the `latest_version` material. Optional
  `associated_data` (base64) is bound as AEAD AAD and must be supplied identically
  to decrypt.
- **decrypt** — parses `v<N>`; rejects `N < min_decryption_version`; unwraps that
  version and decrypts. Any failure (bad version, wrong AAD, tampered bytes) is
  reported as a single generic `ErrBadCiphertext` — the cause is never
  distinguished to the caller.
- **sign** (ed25519) — signs the input with the `latest_version` private key →
  `janus:v<N>:<sig>`.
- **verify** (ed25519) — parses the version, rejects a version
  `< min_decryption_version` (`ErrVersionTooOld` — raising the floor retires old
  signing versions), then verifies with that version's public key. A bad
  signature is `{"valid": false}` — **not** an error.
- **rewrap** — decrypts an old ciphertext (if `≥ min_decryption_version`) and
  re-encrypts it under `latest_version`. **The plaintext is never returned** —
  this rolls data forward after a rotation without exposing it. If the original
  was encrypted with `associated_data`, the same value must be supplied so the
  AEAD tag validates and the binding is preserved on the new ciphertext.
- **datakey** — generates a fresh random 256-bit data key (DEK) and returns it
  **wrapped** under the key's latest version. Two explicit endpoints so plaintext
  exposure is always deliberate: `/datakey/wrapped/{name}` returns only the
  wrapped DEK; `/datakey/plaintext/{name}` returns the wrapped DEK **and** the raw
  DEK (base64) for client-side envelope encryption. `aes256-gcm` keys only.
- **Management** — create, rotate, update-config (`min_decryption_version`,
  `deletion_allowed`), trim, delete, read key metadata, list keys.

## API surface

All routes are under `/v1/transit/`, JSON, `Authorization: Bearer <token>` or a
session cookie, behind `RequireAuth` + `RequireUnsealed` (every op needs the
master key to unwrap version material, so all transit routes answer `503` while
the server is sealed).

| Route | Body | Result | Requires |
|---|---|---|---|
| `POST /v1/transit/keys` | `{name, type}` | `201` metadata | `transit:manage` |
| `GET /v1/transit/keys` | | `{keys:[metadata]}` | `transit:read` |
| `GET /v1/transit/keys/{name}` | | metadata | `transit:read` |
| `POST /v1/transit/keys/{name}/rotate` | | metadata (new `latest_version`) | `transit:manage` |
| `POST /v1/transit/keys/{name}/config` | `{min_decryption_version?, deletion_allowed?}` | metadata | `transit:manage` |
| `POST /v1/transit/keys/{name}/trim` | `{min_available_version}` | metadata | `transit:manage` |
| `DELETE /v1/transit/keys/{name}` | | `204` (requires `deletion_allowed`) | `transit:manage` |
| `POST /v1/transit/encrypt/{name}` | `{plaintext, associated_data?}` | `{ciphertext}` | `transit:use` |
| `POST /v1/transit/decrypt/{name}` | `{ciphertext, associated_data?}` | `{plaintext}` | `transit:use` |
| `POST /v1/transit/sign/{name}` | `{input}` | `{signature}` | `transit:use` |
| `POST /v1/transit/verify/{name}` | `{input, signature}` | `{valid}` | `transit:use` |
| `POST /v1/transit/rewrap/{name}` | `{ciphertext, associated_data?}` | `{ciphertext}` | `transit:use` |
| `POST /v1/transit/datakey/plaintext/{name}` | | `{ciphertext, plaintext}` | `transit:use` |
| `POST /v1/transit/datakey/wrapped/{name}` | | `{ciphertext}` | `transit:use` |

Metadata responses — `{name, type, latest_version, min_decryption_version,
deletion_allowed, versions:[{version, created_at}], created_at}` — never include
key material or wrapped bytes.

## Authorization

Three instance-scoped actions map onto the existing role matrix:

| Action | Grants | Role |
|---|---|---|
| `transit:read` | read key metadata, list keys | **viewer** and up |
| `transit:use` | encrypt / decrypt / sign / verify / rewrap / datakey | **developer** and up |
| `transit:manage` | create / rotate / trim / update-config / delete | **admin** / **owner** |

### Transit-scoped service tokens

A new service-token scope lets an application call transit without reaching
secrets or any other instance action. Mint with `scope.kind = "transit"` and
`access` = `use` or `manage`:

```jsonc
POST /v1/tokens
{ "name": "billing-app", "scope": { "kind": "transit", "id": "" }, "access": "use" }
```

- An **empty** `scope.id` covers **all** transit keys (persisted as a NULL
  `scope_id`).
- A **present** `scope.id` is a transit key **name** and restricts the token to
  that single key — the engine looks keys up by name, and enforcement compares the
  token's scope against the `/{name}` route parameter, so the restriction is
  keyed by name, not id.

A transit token maps **only** to `transit:use` / `transit:manage`; it can never
perform secret, config, or instance actions. Conversely a config- or
environment-scoped (secrets) token has **no** transit capability. Existing tokens
are unaffected.

## Audit

**Management** operations emit fail-closed audit events recording the key
**name**, never material: `transit.key.create`, `transit.key.rotate`,
`transit.key.config`, `transit.key.trim`, `transit.key.delete`. Every denied
(`403`) transit authorization is captured centrally like any other denial.

**Data-plane** operations (encrypt / decrypt / sign / verify / rewrap / datakey)
are **not** individually audited — this matches how the audit design already
treats masked reads, keeps high-frequency transit throughput off the audit
advisory lock, and defers per-operation usage visibility to the Phase-2
usage-metrics sub-project. No key material, wrapped bytes, or plaintext appears in
any audit row, log line, or error body (enforced by a leak test).

## Errors

Transit reuses the project-wide `{"error":{code,message}}` envelope:

| Sentinel | HTTP | Meaning |
|---|---|---|
| `ErrKeyNotFound` | 404 | no such transit key |
| `ErrKeyExists` | 409 | create with a name already in use |
| `ErrWrongKeyType` | 400 | operation not valid for the key type |
| `ErrVersionTooOld` | 400 | ciphertext/signature version `< min_decryption_version` |
| `ErrBadCiphertext` | 400 | malformed envelope or failed AEAD (generic) |
| `ErrDeletionNotAllowed` | 409 | delete without `deletion_allowed` |
| `ErrSealed` | 503 | server sealed |

Decrypt/verify failures are deliberately **generic** — the coarse code never
reveals whether the cause was a bad key, a wrong version, or tampered ciphertext.
Base64 decode failures and invalid key names are `400 validation`.

## Example flow

```sh
# Create an AES key (admin / transit:manage).
curl -XPOST $ADDR/v1/transit/keys -H "$AUTH" \
  -d '{"name":"app","type":"aes256-gcm"}'

# Encrypt (base64 the plaintext first).
curl -XPOST $ADDR/v1/transit/encrypt/app -H "$AUTH" \
  -d '{"plaintext":"'"$(printf 'hello' | base64)"'"}'
# → {"ciphertext":"janus:v1:...."}

# Rotate, then roll old ciphertext forward without exposing plaintext.
curl -XPOST $ADDR/v1/transit/keys/app/rotate -H "$AUTH"
curl -XPOST $ADDR/v1/transit/rewrap/app -H "$AUTH" \
  -d '{"ciphertext":"janus:v1:...."}'
# → {"ciphertext":"janus:v2:...."}

# Decrypt (returns base64 plaintext).
curl -XPOST $ADDR/v1/transit/decrypt/app -H "$AUTH" \
  -d '{"ciphertext":"janus:v2:...."}'
# → {"plaintext":"aGVsbG8="}
```

## Non-goals (this sub-project)

Key export / backup-restore (an exfiltration surface), HMAC/hash helper
endpoints (apps can do those locally), convergent/derived keys and key-derivation
`context`, RSA/ECDSA or other key types, and project-scoped transit keys
(instance-only for v1) are out of scope. The transit web UI (Phase-2
sub-project B) and per-operation usage metrics (sub-project D) are separate
Phase-2 work; the management-only audit policy here deliberately leaves
data-plane usage visibility to D.
