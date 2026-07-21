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
│                  (sys + auth + token + user + member +    │
│                   project/env/config/secret + versions +  │
│                   trash + transit + rotation + sync +      │
│                   dynamic + promote + audit + metrics)    │
├──────────────────────────────────────────────────────────┤
│  internal/auth ✅  internal/authz ✅  internal/audit ✅    │
│  internal/resolve ✅ (inheritance + references)            │
│  internal/promote ✅  internal/masterkeys ✅               │
│  internal/projectkeys ✅  internal/transit ✅               │
│  internal/rotation ✅  internal/secretsync ✅  internal/dynamic ✅ │
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
server. `internal/masterkeys` and `internal/projectkeys` sit alongside
`internal/secrets`, orchestrating rotation of the master key and per-project
KEKs respectively — both re-wrap key material only and never decrypt a secret
value. `internal/resolve` composes read-time config inheritance and secret
references over `internal/secrets`, with `internal/api` owning authz/audit for
each resolved read (see [references.md](references.md)).

See [crypto.md](crypto.md), [data-model.md](data-model.md), and
[operations.md](operations.md) for the implemented layers.

## Packages

| Package | Purpose | State |
|---------|---------|-------|
| `internal/crypto` | AES-256-GCM envelope encryption, key hierarchy, in-memory keyring, Shamir + AWS KMS unseal | ✅ implemented |
| `internal/crypto/shamir` | Vendored HashiCorp Vault Shamir (MPL-2.0) | ✅ vendored |
| `internal/store` | Postgres repositories, migrations, seal-config store, two-level versioning, trash, idempotency | ✅ implemented |
| `internal/secrets` | Encryption orchestration: project KEKs, version-bound DEK AAD, masked vs. reveal reads, version ops, key validation | ✅ implemented |
| `internal/resolve` | Read-time config inheritance + secret-reference resolution (pure, composes over `internal/secrets`) | ✅ implemented |
| `internal/masterkeys` | Master-key rotation: KMS single-call rotate, Shamir interactive rekey ceremony, re-wraps all master-wrapped material in one tx | ✅ implemented |
| `internal/projectkeys` | Per-project KEK rotation + resumable DEK rewrap sweep, version-aware reads | ✅ implemented |
| `internal/promote` | Env-to-env secret promotion pipeline: locked keys, per-key selection, four-eyes approval workflow (`promotion_requests`) | ✅ implemented |
| `internal/transit` | Named-key encrypt/decrypt/sign/verify/rewrap-as-a-service, key versioning, `min_decryption_version` | ✅ implemented |
| `internal/rotation` | Scheduled static secret rotation (Postgres password + webhook rotators), run history | ✅ implemented |
| `internal/secretsync` | One-way sync of resolved secrets to GitHub Actions + Kubernetes Secrets, run history | ✅ implemented |
| `internal/dynamic` | Dynamic Postgres credentials: lease manager, TTL/renewal/revocation, crash-safe issue, orphan sweep | ✅ implemented |
| `internal/api` | HTTP server: chi router, `/v1/sys/*` seal lifecycle, `/v1/auth/*`, `/v1/tokens`, `/v1/users`, `/v1/trash`, `.../members`, `/v1/projects` (+ KEK rotate/rewrap, pipeline, locked-keys, promote) + env/config CRUD + lifecycle, `/v1/configs/{cid}/secrets` masked-list/reveal/write/delete/history, `/v1/configs/{cid}/versions` list/diff/rollback, `/v1/transit/*`, `/v1/rotation/*`, `/v1/sync/*`, `/v1/dynamic/*`, `/v1/audit/*` (verify/export/events/histogram), `/v1/metrics/reads-24h`, cursor pagination + `Idempotency-Key` middleware, sealed-state + auth + authz middleware, `Boot` composition | ✅ implemented |
| `internal/auth` | Argon2id passwords, opaque Postgres sessions, scoped service tokens, OIDC login + CI federation, `Principal` | ✅ implemented |
| `internal/authz` | Pure deny-by-default RBAC engine (viewer/developer/admin/owner; instance/project/env scopes; `Can`, `EffectiveRole`, grant/revoke) | ✅ implemented |
| `internal/audit` | Hash-chained append-only audit log: canonical SHA-256 chain, advisory-lock append, `Verify`, filtered export, bucketed histogram; fail-closed per-handler recording | ✅ implemented |
| `web/` | Svelte 5 (runes) + TypeScript + Vite SPA (Atrium design, hand-written CSS tokens), embedded via `go:embed` | ✅ implemented |
| `cmd/janus` | Single binary: server + operator CLI (`server`, `init`, `unseal`, `seal-status`, `seal`, `migrate`) + secrets/control-plane CLI (`login`, `setup`, `run`, `secrets …`, `project`/`env`/`config`/`token` CRUD, `promote`, `pipeline`, `master-key`, `dynamic`, `sync`, `rotation`, `backup`, `restore`, `whoami`, `completion`) | ✅ implemented |

## Sealed vs. unsealed

The server boots **sealed**: the master key is absent from memory and every
operation that touches a secret returns HTTP 503. An operator unseals it (Shamir
shares or automatic cloud-KMS decrypt), which loads the master key into the
in-memory `Keyring`. Sealing again (or restart) wipes it. This is the single
most important safety property: at rest, nothing on disk can decrypt a secret on
its own.

## How a secret flows through the system

A write today (every layer below is built ✅):

1. **API** (✅) authenticates the caller (session cookie or service token → a
   `Principal`) and the handler checks RBAC via `internal/authz`
   (deny-by-default) before calling the encryption layer. `PUT
   /v1/configs/{cid}/secrets` (batch) and `PUT /v1/configs/{cid}/secrets/{key}`
   (per-key) are the write routes; each commits one new config version.
2. The **encryption layer** (`internal/secrets`, ✅) fetches the project's
   wrapped KEK from the store, unwraps it with the in-memory master key,
   generates a fresh DEK per value, and AES-256-GCM-encrypts the secret. Each
   wrapped key's AAD binds it to its exact slot — project, config/key path,
   and value version (the store assigns the version inside its transaction and
   calls back, so the binding cannot drift).
3. The **store** (✅) persists the resulting `EncryptedValue` (wrapped DEK +
   ciphertext + nonce) as a new immutable *config version*, batching all edits
   from one save together.
4. The **audit log** (✅) appends a hash-chained event — actor, action,
   resource path, result — recording the key name but never the value. The API
   records it fail-closed after the mutation commits.

A read reverses steps 2–3: `GET /v1/configs/{cid}/secrets` returns masked
metadata (no audit) with each key's inheritance `origin`, while a reveal
(`?reveal=true`, or `GET .../secrets/{key}`) resolves inheritance + references,
decrypts, and audits `secret.reveal` (plus one reveal per distinct config
dereferenced via a reference — see [references.md](references.md)). `?raw=true`
returns the config's own stored values verbatim, unresolved. Rollback (`POST
/v1/configs/{cid}/rollback`) repoints at existing ciphertext without
re-encrypting. Deleting a secret writes a tombstone entry (per-key history is
preserved); deleting a project/environment/config soft-deletes it — visible via
`GET /v1/trash` and reversible via `.../restore` — with a separate explicit hard
destroy. See
[operations.md](operations.md) for how the server boots, seals, and unseals
around all of this.

## Build phases

All three phases in `../CLAUDE.md` are complete:

> **Phase 1 — Core:** crypto + unseal ✅ → store + migrations + versioning ✅ →
> CRUD service + encryption orchestration ✅ → server bootstrap (sys API +
> `janus` CLI) ✅ → auth ✅ → RBAC ✅ → audit ✅ → REST API ✅ → CLI with `run` ✅.
>
> **Phase 2 — Transit + UI:** transit engine ✅ → SPA ✅ (originally React/
> Nocturne, rewritten to Svelte 5/Atrium in PR #95, 2026-07-19 — see
> [web.md](web.md)) → OIDC login + CI federation ✅ → usage metrics ✅.
>
> **Phase 3 — Rotation + dynamic:** static rotation (Postgres + webhook) ✅ →
> sync integrations (GitHub Actions + Kubernetes) ✅ → dynamic Postgres
> credentials + lease manager ✅.

Since the original three phases, further depth work has shipped on top: config
inheritance + secret references (`internal/resolve`), per-project KEK rotation
and master-key rotation, trash/restore, per-key value history, typed secrets,
an env-to-env promotion pipeline with a four-eyes approval workflow, cursor
pagination, an `Idempotency-Key` middleware, and an audit event histogram. See
`../status.md` for what remains open and git/PR history for the full detail.
HA/Raft, PKI/CA, SSH signing, HSM, multi-tenancy, and FIPS claims remain
explicit non-goals.
