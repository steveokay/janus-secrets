# Master-Key Rotation — Design

**Status:** Approved 2026-07-15
**Spec:** completes `gaps.md` §4.1 (second half) and the `internal/crypto/keyring.go:50` `TODO(rotation)` — the last unimplemented crypto-spec promise ("rotating the master key re-wraps all project KEKs — online operation").
**Predecessor:** project-KEK rotation (`docs/superpowers/specs/2026-07-14-project-kek-rotation-design.md`, merged PR #71/#72). This feature mirrors its store/API/CLI/audit patterns.

---

## 1. Goal

Rotate the master key (root KEK) online: mint fresh 256-bit master key material, re-wrap **everything the master key wraps** under the new key in a single atomic transaction, re-seal (new Shamir shares / new KMS-wrapped blob + fresh key-check value), and swap the in-memory master — all without ever decrypting a secret value and without taking the instance offline. Owner-only. Under Shamir unseal, gated additionally by a proof-of-possession rekey ceremony.

## 2. What the master key wraps (the complete re-wrap set)

The master key (256-bit, in server memory after unseal) directly wraps exactly these blobs. All are key material — never a secret value — and the set is small and bounded (dozens of rows), which is what makes an atomic single-transaction re-wrap viable.

| Wrapped material | Storage | AAD (unchanged across rotation) |
|---|---|---|
| Project KEKs (current) | `projects.wrapped_kek` (1 / project) | `janus:kek:project` ‖ projectID |
| Project KEKs (pending superseded versions) | `project_kek_versions.wrapped_kek` | `janus:kek:project` ‖ projectID |
| Auth token-HMAC key | `auth_config.wrapped_token_hmac_key` (single row, id=1) | `janus:auth:token-hmac` |
| Transit key material (all versions) | `transit_key_versions.wrapped_material` | existing transit AAD (binds key id + version) |
| Key-check value (KCV) | `seal_config.key_check_value` | **regenerated fresh under M2** (not re-wrapped) |
| Master key itself (KMS unseal only) | `seal_config.wrapped_master_key` | **re-encrypted under KMS** (Shamir: NULL) |

**Correctness note:** the spec line says "re-wraps all project KEKs", but the auth HMAC key and transit key material are *also* master-wrapped. Rotation MUST re-wrap them too, or auth/transit break after the swap. `project_kek_versions.wrapped_kek` (superseded KEK versions still awaiting a project-KEK rewrap) must be included — they too are wrapped under the master.

## 3. Approach: eager-atomic (rejecting lazy-versioned)

**Chosen — eager atomic:** generate M2, re-wrap the entire set (§2) under M2 in one DB transaction, write a fresh KCV + new seal metadata, `COMMIT`, then swap the in-memory master M1→M2 and zeroize M1. Afterward nothing is wrapped under M1 and the old shares are cryptographically dead.

**Rejected — lazy-versioned** (what `TODO(rotation)` hinted at: stamp `Ciphertext.KeyVersion`, re-wrap KEKs on access): would require *retaining* the old master (wrapped under the new one) so old-version KEKs stay openable, which weakens the security purpose of rotation, and adds a version table + `KeyVersion` population for a set small enough to re-wrap eagerly in well under a second. YAGNI. `Ciphertext.KeyVersion` stays `0`; the `TODO(rotation)` comment is removed and replaced with a one-line note that master rotation is eager-atomic.

**Concurrency / serialization:** the keyring holds its **write-lock** across the persist-transaction + in-memory swap, so no concurrent secret operation can observe a half-rotated state (some KEKs M2-wrapped in the DB while the in-memory master is still M1). Because the re-wrap set is small the transaction is sub-second; secret operations pause briefly, then resume. `SELECT … FOR UPDATE` on the re-wrapped rows serializes against a concurrent project-KEK rotation or transit-key create.

**Atomicity / crash safety:** everything is one transaction. Crash *before* commit → full rollback: still M1, old shares still valid, no partial state. The only lock-out window is between `COMMIT` and the operator saving the new shares (Shamir) — mitigated by (a) recommending an instance backup immediately before rotation (the backup/restore feature exists) and (b) the sub-second transaction. Documented in operator guidance, not engineered around (single-tenant, YAGNI).

## 4. Two flows (selected by unseal type)

The status endpoint returns `unseal_type` so CLI/UI pick the right flow.

### 4.1 Shamir — rekey ceremony (proof-of-possession)

Mirrors the existing unseal share-submission accumulator (`internal/crypto/shamir.go` `SubmitShare`/`submitted map`). Ceremony state is in-memory on the single node; a server restart mid-ceremony abandons it safely (nothing committed).

1. **init** (owner) — opens the single active ceremony, returns `{nonce, required, submitted:0}`. 503 if the keyring is sealed. 409 if a ceremony is already active.
2. **submit** (owner) — `{nonce, share}` fed one at a time; server accumulates (dedup by content, like unseal). Returns progress `{submitted, required}`.
3. On `submitted == required`: server reconstructs a candidate master from the submitted shares and **verifies it matches the current in-memory master via the KCV** (constant-time). This proves the initiator physically holds ≥threshold *current* shares, defeating a stolen owner-token. On mismatch → abort ceremony, `400`, no state change. On match → perform rotation (§3): generate M2, re-wrap all under M2, split M2 into **new shares of the same threshold/shape**, write seal_config (new KCV, bump `master_key_version`, set `master_key_rotated_at`), commit, swap, zeroize. Returns `{new_shares:[…], master_key_version}` **once**.
4. **cancel** (owner) — `DELETE` drops the accumulated shares and closes the ceremony.

The Shamir shape (threshold, shares) is **unchanged** — only key material rotates. Reconfiguring N-of-M is explicitly out of scope.

### 4.2 KMS auto-unseal — single call

`rotate` (owner) → generate M2, re-wrap all under M2, re-encrypt M2 under KMS → new `wrapped_master_key`, write seal_config (new KCV, bump version, rotated_at), commit, swap, zeroize. Returns `{master_key_version}`. No shares, no ceremony — there are no operator shares, so owner-token is the sole authority (consistent with the chosen authority model).

Calling the KMS `rotate` endpoint under Shamir → `400` ("rekey ceremony required"). Calling a Shamir rekey endpoint under KMS → `400`.

## 5. Components

Mirrors `internal/projectkeys` (service + closure pattern, crypto isolated from DB).

- **`internal/crypto/keyring.go`** — new `RotateMaster(newMaster []byte, rewrap func(unwrap func(ct Ciphertext, aad []byte) ([]byte, error), wrap func(plain, aad []byte) (Ciphertext, error)) error, persist func() error) error`. Under the write-lock: exposes M1-unwrap + M2-wrap closures to `rewrap` (which builds the new blobs), calls `persist` (the DB tx), and on success sets `k.master = M2` and zeroes M1. If `persist` errors, the in-memory master is untouched (still M1). No key material in errors. `SwapMaster`/unwrap helpers stay internal.
  - Remove the `TODO(rotation)` comment; `Encrypt` still leaves `KeyVersion == 0`.
- **`internal/crypto` unseal** — add `Reseal(newMaster []byte) (cfg *SealConfig, shares [][]byte, err error)` to the `Unsealer` interface. Shamir: split M2 into new shares (same threshold/shares), build KCV, return cfg (type/threshold/shares/kcv) + shares. KMS: `client.Encrypt(M2)`, build KCV, return cfg (type/kcv/wrapped_master_key) + nil shares. Single-share (1-of-1) special case preserved (share == master).
- **`internal/masterkeys/service.go`** — orchestrator. Holds keyring + repos (projects, project_kek_versions, auth, transit, sealconfig). `Rotate(ctx)` for KMS. Ceremony methods `RekeyInit/RekeySubmit/RekeyCancel/RekeyStatus` for Shamir, holding the in-memory ceremony accumulator + possession verification. Zeroizes all key material via `defer`.
- **`internal/store/masterkey.go`** — `RewrapAllUnderNewMaster(ctx, rewrapRow func(old []byte, aad []byte) (new []byte, err error), reseal func() (*crypto.SealConfig, error)) error`: one `withTx` that `SELECT … FOR UPDATE`s and `UPDATE`s every blob in §2 across the four tables, then writes `seal_config` (new KCV / wrapped_master_key, `master_key_version = master_key_version + 1`, `master_key_rotated_at = now()`). Provides a status read (`GetMasterKeyMeta`). AADs are reconstructed identically to the read path.
- **`internal/authz/actions.go`** — new action `SysMasterKey Action = "sys:master-key"`, added to `ownerActions` only (owner-only, like `KEKManage`).
- **`internal/api/masterkey_handlers.go`** — handlers + route registration (guarded by `if s.masterKeys != nil`). Owner-only via `s.authorize(... authz.SysMasterKey, authz.Resource{} ...)` (instance-scoped). Value-free audit.
- **`cmd/janus/masterkey_commands.go`** — `janus master-key` parent + subcommands.
- **`web/src/settings/…`** — Settings → Instance section additions.
- **migration `000019_master_key_rotation.up.sql`** — `ALTER TABLE seal_config ADD COLUMN master_key_version integer NOT NULL DEFAULT 1, ADD COLUMN master_key_rotated_at timestamptz;` (down: drop the two columns).

## 6. Crypto invariants (non-negotiable; enforced by tests)

- M2 from `crypto.GenerateKey` (32-byte CSPRNG). Re-wrap = unwrap-under-M1 → wrap-under-M2 with **byte-identical AAD** and **fresh random nonces** (nonce-difference asserted).
- **Value-free:** rotation touches only key blobs (project KEKs, superseded KEK versions, auth key, transit material) — never a DEK, never a secret value. AES-256-GCM, stdlib `crypto/*` + `x/crypto` only; no new primitives.
- Constant-time comparison for the KCV / possession check.
- Old master M1 zeroized after the swap; superseded Shamir shares cannot reconstruct M2 (fresh split of fresh material) — old shares are dead.
- One transaction → atomic; keyring write-lock spans commit + swap.
- Zero key material / shares / plaintext in logs, errors, or audit entries.

## 7. API surface

All under `/v1/sys/master-key`, owner-only, `Authorization: Bearer`. Sealed → `503`. Error envelope `{"error":{"code","message"}}`.

```
GET    /v1/sys/master-key
       → {unseal_type: "shamir"|"awskms", master_key_version, rotated_at|null,
          rekey_in_progress: bool, submitted: int, required: int}

POST   /v1/sys/master-key/rotate            (KMS only; 400 under shamir)
       → {master_key_version}

POST   /v1/sys/master-key/rekey/init        (shamir only; 400 under KMS; 409 if active)
       → {nonce, required, submitted: 0}

POST   /v1/sys/master-key/rekey/submit      (shamir; {nonce, share})
       → in-progress: {submitted, required, complete: false}
       → on completion: {complete: true, master_key_version, new_shares: [ "…", … ]}

DELETE /v1/sys/master-key/rekey             (shamir; cancel active ceremony)
       → 204
```

**Audit** (value-free, hash-chained): `sys.master-key.rotate`, `sys.master-key.rekey.init`, `sys.master-key.rekey.submit`, `sys.master-key.rekey.cancel`, `sys.master-key.rekey.complete` — resource `sys/master-key`, results success/denied/error. Never logs a share, key, or version material. `GET` status is a plain owner-gated read, no audit event (mirrors `handleKEKStatus`).

## 8. CLI

`janus master-key` parent (mirrors `janus project` from KEK rotation):

- `janus master-key status` → prints unseal type, current version, last-rotated, ceremony progress.
- `janus master-key rotate` → KMS single-shot; prints new version. (Errors clearly under Shamir, directing to `rekey`.)
- `janus master-key rekey` → Shamir. Accepts repeated `--share` flags or prompts interactively for ≥threshold current shares, drives init→submit, prints the **new shares once** on completion with a "store these now" warning. `--cancel` aborts an in-progress ceremony.

## 9. UI (Settings → Instance)

Extends the existing instance-seal section (frontend uses tokens only; solid modal surfaces per the just-shipped fix).

- **Status line:** master key version + "last rotated" (or "never").
- **Rotate master key** button → typed-confirm dialog (danger tone; names the consequence: new shares / re-seal).
  - **Shamir:** share-submission modal (paste ≥threshold current shares, progress indicator mirroring unseal), then **new shares shown once via `RevealOnce`**; recommends taking a backup first.
  - **KMS:** direct confirm → success toast with new version (no shares).
- Reuses `RevealOnce`, `ConfirmDialog`, `Sheet`/`Modal` primitives — no new visual system.

## 10. Testing

- **crypto/service round-trip:** write a secret under M1 → rotate → secret still readable under M2; transit encrypt/decrypt survives; an auth/session HMAC check still validates. Old shares fail the KCV (can't unseal); freshly minted shares pass.
- **never-decrypts-value:** corrupt a secret value's ciphertext → rotation still succeeds (proves values are never opened).
- **leak test:** sentinel plaintext never appears in captured logs during rotation.
- **ceremony:** possession check rejects wrong/insufficient shares; threshold accumulation + dedup; cancel clears state; only one active ceremony (409); KMS vs Shamir endpoint routing (400s).
- **atomicity:** injected failure mid-transaction → full rollback; still M1; old shares still unseal; nothing partially re-wrapped.
- **API e2e:** owner-only (admin → 403); sealed → 503; KMS single-call path with a fake `KMSClient`; Shamir full init→submit→complete.
- **CLI:** command structure + wire paths (stub server), shares-shown-once output.
- **web:** Settings rotate control + share modal + RevealOnce, dual-theme smoke.
- **gates:** `go test ./...`, `-race`, `gosec`, `govulncheck` (pinned `GOTOOLCHAIN=go1.26.5`), `npm test` + smoke, leak test, 100% branch coverage on new `internal/crypto` code including possession-mismatch and tamper cases.

## 11. Non-goals

- Reconfiguring the Shamir shape (threshold/shares) during rotation.
- Lazy/versioned re-wrap or retaining old master keys.
- Rekey ceremony for KMS (no operator shares exist).
- Scheduled/automatic rotation (manual, owner-initiated only).
- Any change to the DEK layer or project-KEK rotation (untouched).
