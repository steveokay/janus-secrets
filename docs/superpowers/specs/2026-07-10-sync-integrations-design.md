# Sync Integrations — Design

> **Phase 3, sub-project 2 of 3.**
> 1. Static rotation framework — ✅ shipped (PR #52).
> 2. **Sync integrations** ← *this spec*.
> 3. Dynamic Postgres credentials + lease manager.

**Status:** approved 2026-07-10.

## 1. Overview & boundaries

A new engine at `internal/secretsync` (package `secretsync`), a sibling to `internal/rotation`, structured the same way: a `Service` holding a store repository + keyring + the secrets resolver + audit recorder, with an in-process scheduler.

> **Package naming:** the package is `secretsync`, not `sync`, to avoid shadowing the Go stdlib `sync` package. CLAUDE.md's repository-layout table lumped "sync integrations" under `internal/rotation/`; this design instead gives sync its own package for separation of concerns (rotation and sync are unrelated mechanics). This is a deliberate, approved deviation from the layout note.

A **sync target** binds one Janus config to one external destination and **one-way** replicates the config's **resolved** secrets — references expanded, exactly the key/value map `janus run` would inject — to that destination. Replication is declarative "desired state": the target is made to match the config's current key set.

Two providers ship in this slice:

- **github** — GitHub Actions secrets (repo-level, optional environment-level).
- **k8s** — Kubernetes `Secret` objects.

Reconcile is **scheduled + on-demand**. Deliverables: the engine + scheduler, REST API under `/v1/sync/`, `janus sync` CLI, RBAC (`sync:manage`), migration `000011`. **No web UI** — deferred.

**Explicitly out of scope** (this slice): web UI for sync; org-level / Dependabot / Codespaces GitHub secrets; other sync destinations (Vault, AWS Secrets Manager, etc.); two-way sync; per-key name mapping or filtering (all resolved keys are pushed). No third-party provider SDKs and no new crypto dependency (both providers use raw REST over `net/http`; GitHub sealed-box encryption uses `golang.org/x/crypto/nacl/box`, already a dependency).

## 2. Provider interface & mechanics

Providers implement one method; the framework computes the desired map, the managed-key set (from the manifest), and whether to prune, then delegates transport to the provider:

```go
// Provider applies the desired key/value map to one external destination.
// managedKeys is the set Janus pushed on the previous successful sync (drives
// prune of keys removed from Janus). It returns the keys it actually applied
// (github may skip name-invalid keys) and any skipped keys with reasons.
type Provider interface {
    Apply(ctx context.Context, creds Creds, addr Addr, desired map[string]string,
        managedKeys []string, prune bool) (ApplyResult, error)
    Name() string // "github" | "k8s"
}

type ApplyResult struct {
    Applied []string          // keys successfully written to the target
    Skipped map[string]string // key -> value-free reason (e.g. "invalid github secret name")
}
```

### github provider
- **Addr:** `{owner, repo, environment?}`. With `environment` set, targets that environment's secrets; otherwise repo secrets.
- **Creds:** a fine-grained (or classic) PAT with Actions-secrets write permission.
- **Apply:**
  1. `GET /repos/{owner}/{repo}/actions/secrets/public-key` (or `/environments/{env}/secrets/public-key`) → `{key, key_id}`.
  2. For each desired key: encrypt the value with `nacl/box.SealAnonymous` under the repo public key (this IS GitHub's libsodium sealed-box format), base64 the ciphertext.
  3. `PUT /repos/{owner}/{repo}/actions/secrets/{NAME}` with `{encrypted_value, key_id}`.
  4. **Prune:** `GET .../actions/secrets` lists secret *names* (values are write-only, but names are readable); for each name in `managedKeys` that is not in `desired`, `DELETE .../actions/secrets/{NAME}`.
- **Key-name constraint:** GitHub secret names must match `^[A-Za-z_][A-Za-z0-9_]*$`, are case-insensitive/stored uppercase, and cannot start with `GITHUB_`. Janus keys that do not conform are **skipped and reported** in `ApplyResult.Skipped` (never silently transformed — transformation risks collisions). A target that skips keys still counts as a successful sync but surfaces the skips.

### k8s provider
- **Addr:** `{namespace, secret_name}`.
- **Creds:** `{api_url, ca_cert, token}` — the API-server base URL, the cluster CA certificate (PEM, used to verify TLS), and a service-account bearer token with `create/patch` on Secrets in the namespace.
- **Apply:** server-side apply — `PATCH {api_url}/api/v1/namespaces/{ns}/secrets/{name}?fieldManager=janus&force=true` with `Content-Type: application/apply-patch+yaml` and a body describing a `type: Opaque` Secret whose `data` map is `base64(value)` per desired key.
  - Server-side apply gives **per-key field ownership**: Janus owns exactly the `data.<key>` entries it applies. Dropping a previously-applied key prunes exactly that key and leaves externally-managed keys in the same Secret untouched. When `prune` is false, the provider merges instead (applies desired keys without removing previously-managed-but-now-absent ones — achieved by also re-applying the still-managed keys; simpler: when `prune=false`, union desired with the prior managed set is not possible without values, so `prune=false` for k8s means "use a non-pruning merge patch instead of SSA" — see §4 note).
- TLS to the API server is verified against the configured `ca_cert` (never `InsecureSkipVerify`).

## 3. Data model

Migration `000011_sync` (current head after 3.1 is `000010`).

`sync_targets`:

| Column | Notes |
| --- | --- |
| `id` | text, `store.NewID` |
| `project_id` | FK → `projects` (KEK + RBAC scope) |
| `config_id` | FK → `configs` (source config) |
| `provider` | `github` \| `k8s` |
| `prune` | bool, default true |
| `interval_seconds` | bigint, > 0 |
| `next_sync_at` | timestamptz |
| `status` | `active` \| `failed` \| `paused` |
| `failure_count` | int |
| `last_error` | text, nullable, sanitized (never a secret) |
| `last_synced_at` | timestamptz, nullable |
| `synced_config_version` | int, nullable (config version at last successful sync) |
| `creds_ct`/`creds_nonce`/`creds_wrapped_dek`/`creds_dek_kek_version` | envelope-encrypted creds blob (mirrors rotation) |
| `addr` | jsonb — non-secret target coordinates (`{owner,repo,environment?}` or `{namespace,secret_name}`) |
| `managed_keys` | text[] — keys Janus pushed on the last successful sync (drives prune) |
| `synced_fingerprint` | bytea, nullable — HMAC of the last-synced resolved map (change detection) |
| `created_by`/`created_at`/`updated_at` | standard |

Uniqueness: one target per `(config_id, provider, addr)` — enforced with a unique index over `(config_id, provider, md5(addr::text))` (addr is jsonb; hash it for the index).

The **creds blob** is JSON, encrypted (see §6): github `{"pat":"..."}`; k8s `{"api_url":"...","ca_cert":"...","token":"..."}`.

History lives in `audit_events`. No separate runs table.

## 4. Reconcile logic

Per target, per scheduler cycle (or on-demand / manual `sync-now`):

1. **Unseal-gated:** resolving the config and decrypting creds requires the project KEK; sealed ⇒ the tick skips the target.
2. **Resolve** the config's current secrets to a `map[string]string` with references expanded (reuse the existing `resolve` machinery / the resolved-read path the CLI uses).
3. **Change detection:** compute `fingerprint = HMAC(masterDerivedKey, canonical(desiredMap))`. If `fingerprint == synced_fingerprint` and this is not a forced/manual sync → **skip** (no external calls; respects provider rate limits). Otherwise proceed.
4. **Apply:** decrypt creds, call `Provider.Apply(creds, addr, desired, managedKeys=synced managed_keys, prune)`.
5. **Commit on success:** set `managed_keys = ApplyResult.Applied`, `synced_fingerprint = fingerprint`, `synced_config_version = <current config version>`, `last_synced_at = now`, reset `failure_count = 0`, `status = 'active'`, `last_error = NULL`, `next_sync_at = now + interval`. Log skipped keys (value-free).

**Change-detection caveat:** the fingerprint covers the *resolved* map, so it also changes when a referenced secret in another config changes — reference drift is caught. It is only recomputed each cycle from a fresh resolve, so the skip in step 3 is safe (it never skips a real change). A manual `sync-now` forces past the skip.

**`prune=false` for k8s:** server-side apply inherently prunes Janus-owned fields. To honor `prune=false` (additive/upsert-only), the k8s provider uses a strategic-merge PATCH (`application/merge-patch+json` / `application/strategic-merge-patch+json`) that adds/updates the desired `data` keys without removing previously-managed-but-absent ones. github honors `prune=false` by simply not issuing DELETEs.

## 5. Scheduler & failure handling

An in-process goroutine started at server boot, tied to the shutdown `context`, ticking every `JANUS_SYNC_TICK` (default `60s`; `0` disables). Identical shape to the rotation scheduler.

Each tick: if `keyring.Sealed()`, skip. Else `ClaimDue`: `SELECT ... WHERE status='active' AND next_sync_at <= now ORDER BY next_sync_at LIMIT <batch>`, reconcile each.

Every reconcile attempt writes a `sync.reconcile` audit event (`success`/`failure`, target id + config path + provider, **never values/creds**). On failure: `failure_count++`, sanitized `last_error`, `next_sync_at = now + backoff(failure_count)` (exp backoff, base 1m, cap 1h). After `failure_count >= 5`, `status='failed'` (scheduler ignores until manual sync-now or `PATCH status=active`). Sealed is **not** counted as a failure (the tick skips before attempting). A manual `sync-now` on a `failed` target reactivates it and marks it due (mirrors rotation's `PrepareRotateNow`), so a crash mid-apply is recoverable.

## 6. Crypto & security

- The creds blob is encrypted exactly like rotation config / secret values: `keyring.NewDEK(projectKEK, crypto.SyncCredsAAD(targetID))` mints + wraps a per-blob DEK, then AES-256-GCM over the JSON, storing ciphertext + nonce + wrapped DEK. New AAD helper `crypto.SyncCredsAAD(targetID)`. **No new crypto primitives; stdlib + `x/crypto` only.**
- GitHub sealed boxes use `golang.org/x/crypto/nacl/box.SealAnonymous` — already an x/crypto dependency, no addition.
- The change-detection fingerprint is `HMAC-SHA256` keyed by a key derived from the master key (a new `keyring.SyncFingerprint(data []byte) []byte`, available only while unsealed), so the DB never stores a reversible hash of secret values.
- **Zero plaintext leakage.** PATs, k8s tokens, CA certs, and secret values never appear in logs, error strings, `last_error`, audit entries, or masked API responses. A grep-based leak test asserts this over captured logs.
- Outbound TLS is always verified: github against public CAs; k8s against the target's configured `ca_cert`. `InsecureSkipVerify` is never used.
- All target inputs validated at the API boundary; SQL via parameterized queries only; the addr jsonb is validated per-provider before storage.

## 7. REST API, CLI, RBAC

All routes require **`sync:manage`** on the target's project (project-scoped; added to the admin/owner action bundle). Reads are **masked**: status/provider/addr/`managed_keys` names + skip reports, but never creds (PAT/token/CA) or secret values.

- `POST /v1/sync/targets` — create. Body: `config_id`, `provider`, `prune`, `interval_seconds`, provider `addr`, provider `creds`.
- `GET /v1/sync/targets?project_id=…` — list (masked).
- `GET /v1/sync/targets/{id}` — get (masked).
- `PATCH /v1/sync/targets/{id}` — update interval / prune / status / creds / addr.
- `DELETE /v1/sync/targets/{id}`.
- `POST /v1/sync/targets/{id}/sync` — sync now (forces past change-detection; clears `failed`).

Errors use the project `{"error":{"code","message"}}` envelope; new code `sync_not_found`; reuse `validation`/`conflict`/`forbidden`/`sealed`.

CLI: `janus sync create | list | get | update | delete | sync`, built on the existing `apiclient`.

## 8. Testing

- **Unit (table-driven):** fingerprint change-detection (unchanged → skip, changed → sync); github key-name validation/skip; backoff schedule + `failed` threshold; sealed-box round-trip (encrypt with `SealAnonymous` under a test keypair, decrypt with `box.OpenAnonymous` to assert the value); creds-blob encrypt/decrypt round-trip + AAD binding.
- **Integration (`httptest` provider fakes):**
  - github: fake server for `public-key` / `PUT secrets` / `list secrets` / `DELETE`; assert the PUT carries a valid sealed-box `encrypted_value` + `key_id`, that a key removed from the config is DELETEd on prune, that a non-conforming key is skipped and reported, and that `prune=false` issues no DELETE.
  - k8s: fake server for the SSA `PATCH .../secrets/{name}`; assert `Content-Type: application/apply-patch+yaml`, `fieldManager=janus`, base64 `data`, and that `prune=false` uses a merge patch. Assert TLS CA wiring via a fake server with a self-signed cert the target `ca_cert` trusts.
  - testcontainers Postgres for the store repo tests.
- **e2e (real stack):** create a target via the API against a fake provider server, run one reconcile, assert the external fake received the resolved secrets and an audit event was written; masked GET hides creds.
- **Leak test:** no PAT / k8s token / CA / secret value appears in captured logs across a sync run.
- Gates unchanged: `go build`, `go vet`, `go test`, `gosec`, `govulncheck` all green.

## 9. Files (anticipated)

- `internal/crypto/` — add `SyncCredsAAD` helper + a `Keyring.SyncFingerprint` method (small additions).
- `internal/secretsync/` — `secretsync.go` (Service + New + envelope creds seal/open), `provider.go` (Provider interface + Creds/Addr/ApplyResult types), `github_provider.go`, `k8s_provider.go`, `reconcile.go` (resolve → fingerprint → apply → commit; backoff), `scheduler.go` (RunDue/RunScheduler), `crud.go` (engine CRUD + SyncNow + masked views), `errors.go`, plus tests.
- `internal/store/` — `sync.go` (repository + `SyncTarget` model), migration `migrations/000011_sync.{up,down}.sql`, add `sync_targets` to `backupTables`.
- `internal/api/` — `sync_handlers.go`, route group + `sync:manage` in the RBAC set + error code + Server field + `New`/`Boot` wiring + scheduler start.
- `cmd/janus/` — `sync_commands.go` (CLI), `JANUS_SYNC_TICK` parsing in `server.go`.
- `docs/ops/sync.md` — operator runbook (provider credential setup, least privilege, prune semantics, key-name constraints); `docs/operations.md` — CLI + env-var rows.
