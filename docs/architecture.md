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
│  cmd/janus (server)          cmd/kh (CLI)                 │
├──────────────────────────────────────────────────────────┤
│  internal/api    HTTP handlers, middleware, routes  (TODO)│
├──────────────────────────────────────────────────────────┤
│  internal/auth   internal/authz   internal/audit    (TODO)│
├──────────────────────────────────────────────────────────┤
│  internal/store  Postgres repositories, migrations  (WIP) │
│                  crypto-blind: stores ciphertext only     │
├──────────────────────────────────────────────────────────┤
│  internal/crypto envelope encryption, keyring, unseal  ✅ │
└──────────────────────────────────────────────────────────┘
```

Key boundary: **`internal/crypto` is storage-blind and `internal/store` is
crypto-blind.** Crypto knows how to wrap/unwrap keys but not where anything is
stored; the store knows how to persist opaque ciphertext but never holds a key
or plaintext. Encryption orchestration — the layer that holds the unsealed
keyring and turns plaintext into stored ciphertext — sits *above* the store and
lands with a later milestone.

See [crypto.md](crypto.md) and [data-model.md](data-model.md) for the two
implemented/in-progress layers.

## Packages

| Package | Purpose | State |
|---------|---------|-------|
| `internal/crypto` | AES-256-GCM envelope encryption, key hierarchy, in-memory keyring, Shamir + AWS KMS unseal | ✅ implemented |
| `internal/crypto/shamir` | Vendored HashiCorp Vault Shamir (MPL-2.0) | ✅ vendored |
| `internal/store` | Postgres repositories, migrations, seal-config store | 🚧 in progress |
| `internal/auth` | Passwords, service tokens, OIDC, sessions | ⏳ planned |
| `internal/authz` | RBAC engine (viewer/developer/admin/owner) | ⏳ planned |
| `internal/audit` | Hash-chained append-only audit log | ⏳ planned |
| `internal/api` | `net/http` + chi, REST under `/v1/` | ⏳ planned |
| `cmd/janus` | Server entrypoint | 🚧 stub |
| `cmd/kh` | CLI (`kh run`, etc.) | ⏳ planned |

## Sealed vs. unsealed

The server boots **sealed**: the master key is absent from memory and every
operation that touches a secret returns HTTP 503. An operator unseals it (Shamir
shares or automatic cloud-KMS decrypt), which loads the master key into the
in-memory `Keyring`. Sealing again (or restart) wipes it. This is the single
most important safety property: at rest, nothing on disk can decrypt a secret on
its own.

## How a secret will flow through the system

Once the upper layers exist, a write will look like this (layers marked *TODO*
are not built yet):

1. **API** *(TODO)* authenticates the caller (session or service token) and
   checks RBAC.
2. The **encryption layer** *(TODO)* fetches the project's wrapped KEK from the
   store, unwraps it with the in-memory master key, generates a fresh DEK, and
   AES-256-GCM-encrypts the secret value. Each wrapped key is bound to its
   location with AAD.
3. The **store** persists the resulting `EncryptedValue` (wrapped DEK +
   ciphertext + nonce) as a new immutable *config version*, batching all edits
   from one save together.
4. The **audit log** *(TODO)* appends a hash-chained event — actor, action,
   resource path, result — recording the key name but never the value.

A read reverses steps 2–3 and, for a value reveal, also audits.

## Build phases

Phase 1 is being built strictly in order (see `../CLAUDE.md` for the full list):

> crypto + unseal ✅ → **store + migrations** 🚧 → CRUD with versioning → auth →
> RBAC → audit → REST API → CLI with `run`.

Phases 2 (transit engine + React UI + OIDC + usage metrics) and 3 (rotation +
dynamic Postgres credentials) follow. HA/Raft, PKI/CA, SSH signing, HSM,
multi-tenancy, and FIPS claims are explicit non-goals.
