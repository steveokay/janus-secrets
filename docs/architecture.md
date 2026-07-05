# Architecture overview

Janus is a single-tenant, self-hosted secrets manager: **one Go binary + one
Postgres**. There is no clustering, no external message bus, and no separate
metrics stack. State lives in Postgres; the only in-memory secret is the master
key, and only after unseal.

## Layering

The codebase is built bottom-up in strict layers. Each layer depends only on
those below it and is testable in isolation.

```
┌──────────────────────────────────────────────────────────┐
│  cmd/janus (single binary: server + secrets CLI)         │
├──────────────────────────────────────────────────────────┤
│  internal/api    HTTP handlers, middleware, routes     ✅ │
│                  (sys + auth + token + user + member)     │
├──────────────────────────────────────────────────────────┤
│  internal/auth ✅  internal/authz ✅  internal/audit ✅    │
├──────────────────────────────────────────────────────────┤
│  internal/store  Postgres repositories, migrations    ✅  │
│                  crypto-blind: stores ciphertext only     │
├──────────────────────────────────────────────────────────┤
│  internal/crypto envelope encryption, keyring, unseal  ✅ │
└──────────────────────────────────────────────────────────┘
```

Key boundary: **`internal/crypto` is storage-blind and `internal/store` is
crypto-blind.** Crypto knows how to wrap/unwrap keys but not where anything is
stored; the store knows how to persist opaque ciphertext but never holds a key
or plaintext. Encryption orchestration — the layer that holds the unsealed
keyring and turns plaintext into stored ciphertext — sits *above* the store in
`internal/secrets`, and `internal/api` composes everything into the runnable
server.

See [crypto.md](crypto.md), [data-model.md](data-model.md), and
[operations.md](operations.md) for the implemented layers.

## Packages

| Package | Purpose | State |
|---------|---------|-------|
| `internal/crypto` | AES-256-GCM envelope encryption, key hierarchy, in-memory keyring, Shamir + AWS KMS unseal | ✅ implemented |
| `internal/crypto/shamir` | Vendored HashiCorp Vault Shamir (MPL-2.0) | ✅ vendored |
| `internal/store` | Postgres repositories, migrations, seal-config store, two-level versioning | ✅ core CRUD (inheritance/references deferred) |
| `internal/secrets` | Encryption orchestration: project KEKs, version-bound DEK AAD, masked vs. reveal reads, version ops | ✅ implemented |
| `internal/api` | HTTP server: chi router, `/v1/sys/*` seal lifecycle, `/v1/auth/*`, `/v1/tokens`, `/v1/users`, `.../members`, sealed-state + auth + authz middleware, `Boot` composition | ✅ sys/auth/authz APIs (secret routes land with the API milestone) |
| `internal/auth` | Argon2id passwords, opaque Postgres sessions, scoped service tokens, `Principal` | ✅ implemented (OIDC/federation Phase 2) |
| `internal/authz` | Pure deny-by-default RBAC engine (viewer/developer/admin/owner; instance/project/env scopes; `Can`, `EffectiveRole`, grant/revoke) | ✅ implemented |
| `internal/audit` | Hash-chained append-only audit log: canonical SHA-256 chain, advisory-lock append, `Verify`, filtered export; fail-closed per-handler recording | ✅ implemented |
| `cmd/janus` | Single binary: server + operator CLI (`server`, `init`, `unseal`, `seal-status`, `seal`, `migrate`) — secrets CLI (`janus run`, etc.) planned | ✅ implemented (secrets CLI ⏳ planned) |

## Sealed vs. unsealed

The server boots **sealed**: the master key is absent from memory and every
operation that touches a secret returns HTTP 503. An operator unseals it (Shamir
shares or automatic cloud-KMS decrypt), which loads the master key into the
in-memory `Keyring`. Sealing again (or restart) wipes it. This is the single
most important safety property: at rest, nothing on disk can decrypt a secret on
its own.

## How a secret flows through the system

A write today (layers marked *TODO* are not built yet):

1. **API** authenticates the caller (session cookie or service token → a
   `Principal`) and the handler checks RBAC via `internal/authz`
   (deny-by-default). *(The auth + authz machinery is built ✅; the
   secret-writing route itself lands with the REST API milestone.)*
2. The **encryption layer** (`internal/secrets`, ✅) fetches the project's
   wrapped KEK from the store, unwraps it with the in-memory master key,
   generates a fresh DEK per value, and AES-256-GCM-encrypts the secret. Each
   wrapped key's AAD binds it to its exact slot — project, config/key path,
   and value version (the store assigns the version inside its transaction and
   calls back, so the binding cannot drift).
3. The **store** (✅) persists the resulting `EncryptedValue` (wrapped DEK +
   ciphertext + nonce) as a new immutable *config version*, batching all edits
   from one save together.
4. The **audit log** *(TODO)* appends a hash-chained event — actor, action,
   resource path, result — recording the key name but never the value.

A read reverses steps 2–3 and, for a value reveal, also audits. Rollback
repoints at existing ciphertext without re-encrypting. See
[operations.md](operations.md) for how the server boots, seals, and unseals
around all of this.

## Build phases

Phase 1 is being built strictly in order (see `../CLAUDE.md` for the full list):

> crypto + unseal ✅ → store + migrations + versioning ✅ → CRUD service +
> encryption orchestration ✅ → server bootstrap (sys API + `janus` CLI) ✅ →
> auth ✅ → RBAC ✅ → audit ✅ → **REST API** → CLI with `run`.

Phases 2 (transit engine + React UI + OIDC + usage metrics) and 3 (rotation +
dynamic Postgres credentials) follow. HA/Raft, PKI/CA, SSH signing, HSM,
multi-tenancy, and FIPS claims are explicit non-goals.
