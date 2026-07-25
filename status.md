# Janus — status & backlog

_Single tracker for the project (2026-07-20). Replaces the former `gaps.md`
(gap analysis) and `fe-improvements.md` (old React UI punch-list, retired by
the Nocturne → Atrium rewrites) — both removed. This file lists what's
**open**; for what's already built, see the summary below, `docs/roadmap.md`,
`docs/architecture.md`, and git/PR history for detail._

## Where it stands

One Go binary + one Postgres. Backend: envelope encryption (master key →
project KEKs → per-version DEKs), Shamir/KMS unseal, Postgres store with
two-level versioning + soft-delete, password + service-token + OIDC auth,
deny-by-default RBAC, a hash-chained audit log, a full `/v1/` REST API, and
the `janus` CLI (`run`, `secrets`, project/env/config/token control plane).
Feature engines: transit (encryption-as-a-service), scheduled static
rotation, one-way sync (GitHub Actions + Kubernetes), and dynamic Postgres
credentials with a lease manager. On top of the core model: config
inheritance + secret references, project-KEK and master-key rotation,
trash/restore, per-key value history, typed secrets, an env→env promotion
pipeline with four-eyes approval, cursor pagination + `Idempotency-Key`, and
release hygiene (Apache-2.0 license, hand-authored OpenAPI spec, goreleaser,
CHANGELOG, `GET /v1/sys/version`).

UI: a Svelte 5 SPA ("Atrium" — banknote-engraving / archival-ledger
aesthetic, `daylight`/`nightwatch` themes) embedded via `go:embed`, covering
the entire API surface — init/unseal/login, projects → envs → configs, the
secret editor (masked/audited reveal, dirty-buffer saves, per-key history,
locked keys, import/export), the promotion pipeline + approvals, the audit
ledger, tokens, scoped members + an RBAC matrix view, transit, an operations
console (rotation/sync/dynamic, incl. create flows and credential issuance),
an integrations hub (OIDC + CI federation), trash, and a settings hub
(master-key rotate/rekey, backup, seal control). It replaced an earlier
React SPA (through two redesigns, "Nocturne" then "Atrium" — see
[`ui-redo.md`](ui-redo.md) for that history).

All three CLAUDE.md build phases (Core, Transit + UI, Rotation + dynamic) are
complete. Upstream non-goals stay non-goals: no HA/Raft, no PKI/CA, no SSH
signing, no HSM, no multi-tenancy, no FIPS claims.

---

## In progress

_Nothing in flight._

## Open — backend / ops

- [x] ~~**Account lockout / progressive backoff** beyond the per-IP login rate
      limiter (10/min).~~ **SHIPPED 2026-07-22** — progressive temporary
      per-account lockout (5 failures → escalating `1m→5m→25m→1h` window,
      auto-expiring, reset on success; while locked, attempts don't extend the
      window → no DoS). Reveals the lock only to a correct password (`429
      account_locked` + `Retry-After`); wrong password stays byte-identical
      `invalid_credentials` (no enumeration). Admin unlock (`POST
      /v1/users/{id}/unlock`, `user:manage`) + Members "Locked" badge/Unlock;
      `JANUS_LOCKOUT_*` env; migration 000026. Adversarial review SHIP.
- [x] ~~**DB pool tuning** — `pgx` runs on defaults; shutdown grace fixed at
      10s.~~ **SHIPPED 2026-07-23** (ops-hardening bundle) — `JANUS_DB_MAX_CONNS`
      / `JANUS_DB_MIN_CONNS` / `JANUS_DB_MAX_CONN_LIFETIME` /
      `JANUS_DB_MAX_CONN_IDLE_TIME` (via `pgxpool.ParseConfig`+`NewWithConfig`,
      unset = pgx defaults) + configurable `JANUS_SHUTDOWN_GRACE` (default 10s,
      used for the main + aux-listener drains). No migration.
- [x] ~~**Prometheus `/metrics`** (request rates/latency, seal state, lease
      counts, rotation/sync failure gauges, audit head seq) + a
      `JANUS_LOG_LEVEL`/format env var.~~ **SHIPPED 2026-07-22** — hand-rolled
      zero-dep exposition, token-gated (`JANUS_METRICS_TOKEN`, off by default),
      HTTP metrics keyed by chi route pattern (bounded cardinality) + engine/DB/
      audit/runtime gauges. Plus an admin **health panel** (Settings, backed by
      `GET /v1/sys/status`) and `JANUS_LOG_LEVEL`/`JANUS_LOG_FORMAT`. Adversarial
      review SHIP. See [observability guide](docs/guides/observability.md).
- [x] ~~**Token `last_used` / user `last_login` not tracked**~~ **SHIPPED
      2026-07-23** — migration 000030 adds `service_tokens.last_used_at` +
      `users.last_login_at`. Token last-used updated on service-token auth,
      **throttled** (≤ once/60s, conditional UPDATE) and **non-fatal** (a failed
      update never fails the request); user last-login stamped in `createSession`
      (covers both password + OIDC login). `GET /v1/tokens` → `last_used_at`,
      `GET /v1/users` → `last_login_at`; Tokens screen "Last used" column +
      stale-token badge (never / 90d+), Members "Last login" column. Value-free.
- [x] ~~**docker-compose has no resource limits**, and no WAL-archiving/
      pg-backup guidance.~~ **SHIPPED 2026-07-23** (ops-hardening bundle) —
      `deploy.resources` limits+reservations on app + postgres; new
      [backup-and-restore guide](docs/guides/backup-and-restore.md)
      (`pg_dump`/`pg_restore` + WAL/PITR, distinguished from the sealed-material
      `janus backup`).
- [x] ~~**No `CONTRIBUTING.md`.**~~ **SHIPPED 2026-07-23** (ops-hardening
      bundle) — build/test/gate/migration/crypto/PR conventions.
- [x] ~~**Decision — OIDC login is not gated by app-level TOTP.**~~ **RESOLVED
      2026-07-23 — intended; documented.** OIDC delegates MFA to the IdP (the
      standard relying-party posture); Janus TOTP gates only the password path.
      Documented in [two-factor-auth guide](docs/guides/two-factor-auth.md#scope-password-logins-only-not-oidc)
      with the both-paths caveat and mitigations. An opt-in "require app 2FA even
      for OIDC" switch is a possible future add, not enforced today.
- [x] ~~**Decision needed — audit fail-closed policy for engine-authored action
      endpoints**~~ **RESOLVED 2026-07-23 — option (a): accept + document.** The
      engines' action endpoints (rotation/sync/dynamic) keep the fail-closed
      *denial* path; the *success* audit is the engine's best-effort write,
      because the external side effect can't be undone by a late audit failure
      and the `*_runs` tables are a second durable record. Applies uniformly
      across all three engines. Documented in the [operations audit-log
      section](docs/operations.md#audit-log) and `docs/architecture.md`.

## Open — product roadmap

**This section is the canonical tracker and mirrors the nine sections of
[`docs/roadmap.md`](docs/roadmap.md) one-to-one — every roadmap item appears
here (shipped ones struck through with a date). When you add, ship, or reword a
roadmap item, update BOTH files so they never drift.** Effort: **S** ≈ a
session, **M** ≈ a day or two, **L** ≈ a week-plus. Sections 1–5 are the
original (exhausted) roadmap; sections 6–9 are the post-1.0 roadmap added
2026-07-24 from a full-system review.

### Security hardening

| Feature | Why | Effort |
|---|---|---|
| ~~Native TLS listener (`JANUS_TLS_CERT/KEY`, optional ACME)~~ **SHIPPED 2026-07-23** — native HTTPS: static certs (`JANUS_TLS_CERT`/`JANUS_TLS_KEY`) or ACME/Let's Encrypt (`JANUS_TLS_ACME_DOMAINS`/`_EMAIL`/`_CACHE`, via `x/crypto/acme/autocert`), mutually exclusive + startup-validated, TLS 1.2 floor, optional `JANUS_TLS_REDIRECT_HTTP` HTTP→HTTPS 301; aux listeners drain on shutdown. No migration. See [production-deployment guide](docs/guides/production-deployment.md). | ~~M~~ |
| ~~TOTP second factor for password logins (+ recovery codes)~~ **SHIPPED 2026-07-21** — RFC 6238 TOTP + single-use recovery codes; self-service enroll/confirm/disable/regenerate (`/v1/auth/totp/*`), login gains `totp_code` (`401 totp_required` without it), `janus login` prompts + retries, Settings enroll UI. Secret master-key-wrapped (re-wrapped by master-key rotation), recovery codes HMAC-hashed + single-use, value-free audit. Migration 000025. **Note:** OIDC login is not gated by app-level TOTP (the IdP owns MFA) — see follow-ups. Passkeys/WebAuthn + per-account lockout as follow-ups. | ~~M~~ |
| ~~Session management — list active sessions, revoke one/all~~ **SHIPPED 2026-07-20** — `GET/DELETE /v1/auth/sessions` (self-service, IP/user-agent metadata, current-session marker), Settings → Active sessions UI, `janus session list/revoke`. Sessions now record client IP + user-agent (migration 000023). | An admin who suspects a stolen cookie has nothing to pull today. | ~~S~~ |
| ~~Account lockout / progressive backoff~~ **SHIPPED 2026-07-22** — see the "Open — backend / ops" entry above (migration 000026, `JANUS_LOCKOUT_*`, admin unlock). | Nothing locked an account out after repeated failed logins. | ~~S~~ |
| ~~Secret expiry / max-age policy per key or config, surfaced in-app~~ **SHIPPED 2026-07-23** — **advisory** max-age (never blocks reads/writes): config-level default + per-key override, effective policy = per-key else config-default else none, `stale` computed from the current value version's age. `config_secret_max_age` table (migration 000028, config-default under the `''` sentinel key), `GET/PUT /v1/configs/{cid}/max-age` + `PUT .../secrets/{key}/max-age`, `secret:write` to set / `secret:read` to list, value-free audit; masked-list gains `stale`+`max_age_seconds`; editor stale chip + set/clear controls + Overview in-tray count; `janus secrets max-age` CLI. | ~~M~~ |
| ~~Break-glass access — time-boxed role elevation with a mandatory reason, stamped into the audit chain~~ **SHIPPED 2026-07-23** — guarded self-service emergency elevation: activate only on a scope where you already hold a role, to a strictly-higher role (≤ owner), mandatory reason, TTL clamped to `JANUS_BREAKGLASS_MAX_TTL` (default 1h). Authz effective role = max(bound, active non-expired grant on the exact scope, re-checked against the engine clock). Loud `breakglass.activate/revoke/expire` audit (fail-closed) wired into notifications (`breakglass.activated`); self-revoke + boot-time expiry sweep; activate UI + active-grants list + Overview banner. Migration 000031. | ~~M~~ |
| ~~Per-token IP allowlists + usage anomaly notes (new IP)~~ **SHIPPED 2026-07-23** — optional per-token CIDR allowlist enforced in the API auth middleware (service-token auth only; out-of-list → 403; IPv4+IPv6; fails closed on an unparseable IP; client IP from `r.RemoteAddr` like the audit log, XFF untrusted). Value-free new-IP detection via `token_seen_ips` (best-effort `INSERT ON CONFLICT`, `token.new_ip` audit + Overview in-tray). Migration 000032. | ~~M~~ |
| ~~GCP KMS / Azure Key Vault auto-unseal~~ **SHIPPED 2026-07-23** — both providers on the provider-agnostic `KMSUnsealer` (parameterized with its seal type; AWS unchanged). `JANUS_SEAL_TYPE=gcpkms` (`JANUS_GCP_KMS_KEY`, ambient ADC) / `azurekv` (`JANUS_AZURE_KEYVAULT_URL`+`_KEY_NAME`[+`_KEY_VERSION`], ambient `DefaultAzureCredential`). New seal-type constants, no migration; `internal/crypto` held at 100% coverage with faked KMS APIs. | ~~M~~ |

### Secret lifecycle & editor

| Feature | Why | Effort |
|---|---|---|
| ~~Dotenv / properties import in the editor~~ **SHIPPED 2026-07-19** — Import… paste or pick a `.env`/`.properties` file, preview per-key (new/overwrite/invalid), stage into the dirty buffer, commit as one version. | The first thing a migrating user does is re-key an existing `.env` by hand. | ~~S~~ |
| ~~Value generator in the editor (random password / hex / base64, length picker)~~ **SHIPPED 2026-07-22** — client-side CSPRNG (unbiased rejection sampling), "Gen" popover on the editable value cell: password (symbols / exclude-ambiguous toggles) / hex / base64 + length; value flows through the normal dirty-buffer save, no endpoint/migration. | ~~S~~ |
| ~~Unused-secret detection — "not read in 90 days" chip from audit data~~ **SHIPPED 2026-07-23** — **advisory** (blocks nothing): per-key last-read = `MAX(occurred_at)` over `secret.reveal` audit events; masked list gains `last_read_at`+`unused`; editor "not read 90d+ / never read" chip + Overview in-tray count; threshold `JANUS_UNUSED_SECRET_DAYS` (default 90); migration 000029 (partial index on reveal events). Value-free. Bulk raw reads aren't per-key attributable (documented); inherited keys read as never-read on the leaf. | ~~M~~ |
| ~~Per-key read insights — last-read + 30-day sparkline in the editor row~~ **SHIPPED 2026-07-23** — value-free `GET /v1/configs/{cid}/read-insights` (per key: `last_read_at` + 30-int `daily` reveal counts) from `secret.reveal` audit events, reusing the 000029 partial index (no migration); editor row Reads panel with the `Sparkline` component. Rides `secret:read`, unaudited like the masked list. | ~~M~~ |
| ~~Cross-environment diff view — pick any two configs, key-level presence/drift (values masked)~~ **SHIPPED 2026-07-23** — `GET /v1/configs/{cid}/compare?against={cid}` returns **booleans only** (in_a/in_b/differs + per-side origin), never a value; requires `secret:read` on BOTH configs (each authorized independently, denial audited) + one value-free `config.compare` audit event; generalizes the promotion preview. New Compare screen + nav + palette entry. No migration. | ~~M~~ |
| ~~Secret annotations — owner + note metadata per key (never values)~~ **SHIPPED 2026-07-23** — `config_secret_annotations` (migration 000033, value-free); `PUT /v1/configs/{cid}/secrets/{key}/annotation`, `owner`/`note` on the masked list, editor affordance; `secret:write` to set / `secret:read` to view; value-free audit. Mirrors the max-age pattern. | ~~M~~ |
| ~~Require-approval-for-prod-edits toggle — direct saves to protected configs become a promotion-style request~~ **SHIPPED 2026-07-24** — per-config `require_approval` flag (`promotion:manage` to toggle); a save to a protected config files a **pending edit request** (`202`, not a commit) with the proposed changes **envelope-encrypted** (fresh DEK under the project KEK, domain-separated `ConfigEditRequestAAD`); a **different** user with `secret:write` approves (four-eyes, self-approval `403`, mark-on-success CAS → commit via `SetSecrets`) or rejects; requester cancels. Migration 000036. Value-free (key names only); crypto 100%. Editor Protect toggle + Approvals section. **Hardened 2026-07-24 (PR #148):** rollback + promote-apply now honor `require_approval` too (route the changeset through the edit-request flow instead of committing directly), and approval is claim-before-commit (no double-commit) — see Release & distribution below. | ~~M~~ |

### Integrations & delivery

| Feature | Why | Effort |
|---|---|---|
| ~~More sync providers: GitLab CI, Cloudflare Workers, Vercel/Netlify env, AWS SSM/Secrets Manager~~ **ALL SHIPPED 2026-07-23** — the sync engine now has **8 providers**: `github`, `k8s`, `gitlab`, `aws_ssm`, `cloudflare`, `aws_secrets`, and **`vercel` + `netlify`** (both net/http, upsert+prune, `api_token` cred; #130 also restored Cloudflare's REST decode fields). No migration. (GitLab CI/CD variables, GitHub Actions, K8s Secrets, Cloudflare Workers secrets, Vercel/Netlify env vars, AWS SSM Parameter Store + Secrets Manager.)  **CORRECTION (2026-07-25):** these were shipped in code but NOT actually usable — migration `000011` pinned `sync_targets.provider` to `CHECK (provider IN ('github','k8s'))` and no later migration widened it, so persisting a `gitlab`/`aws_ssm`/`cloudflare`/`aws_secrets`/`vercel`/`netlify` target failed the constraint. Found and fixed by migration `000041` (PR #175), with a store regression test creating a target for all eight. Same class of bug as the `rotation_policies.type` CHECK that `000037` fixed — a CREATE-time enum later features outgrew. | ~~M each~~ |
| ~~More CI federation issuers: GitLab, Buildkite, CircleCI OIDC~~ **SHIPPED 2026-07-23** — provider-aware required-claim rule (replaces the hardcoded GitHub `repository` requirement: GitHub→`repository`, GitLab→`project_path`, Buildkite→`organization_slug`, CircleCI→org/project claim; unknown issuer → any non-empty claim), issuer presets + URL validation, single-active-issuer model, `web/src/lib/federation.ts` preset dropdown. No migration. | ~~S each~~ |
| ~~Inbound one-shot importers: Doppler, Vault KV, AWS SM → project/config tree~~ **SHIPPED 2026-07-24** — CLI-first `janus import doppler|vault|aws-sm`: fetches from the source (creds from flags/env, never stored), maps → a Janus project/env/config (create-if-missing), writes as one batched config version via the existing authed client. **Default `--dry-run`** prints key names + counts only (never values); `--confirm` writes. Doppler/Vault via net/http, AWS-SM via the existing aws-sdk. No new server endpoint/migration/dep. Web wizard remains a possible follow-up. | ~~L~~ |
| ~~Notifications: webhook + Slack for rotation failures, sync errors, denials, pending approvals~~ **SHIPPED 2026-07-21** — audit-tailing dispatcher + delivery outbox; webhook + Slack channels; `notification:manage`, `/v1/notifications/channels`, `janus notifications` CLI, Notifications web screen; migration 000024. **SMTP email channel added 2026-07-23** (`type=smtp`, `net/smtp` STARTTLS/implicit/none, verify-by-default + per-channel `insecure_skip_verify`, write-only password, value-free body; migration 000027). | Failures must find humans, not just an in-app tray. | ~~M~~ |
| ~~Terraform provider (projects, configs, secrets-as-writes, tokens, bindings)~~ **SHIPPED 2026-07-24** — `terraform-provider-janus/` (own Go module, terraform-plugin-framework): resources `janus_project`/`environment`/`config`/`secret` (value `Sensitive`)/`service_token` (minted token = sensitive computed, secrets-in-state caveat documented) + `janus_secret`/`janus_config` data sources; full CRUD + import, 404→drift, `JANUS_ADDR`/`JANUS_TOKEN`. Hermetic httptest unit tests. Deferred: env-scoped tokens, batch-secret resource, Registry publish. | ~~L~~ |
| ~~Client SDKs (Go, TypeScript, Python) with in-process caching + lease renewal~~ **ALL SHIPPED 2026-07-24** — three standalone SDKs mirroring one API/cache model: **Go** (`sdk/go/`, zero deps), **TypeScript** (`sdk/ts/`, `janus-client`, fetch-based zero-dep, Node 18+), **Python** (`sdk/python/`, `janus_client`, stdlib-only, 3.9+). Each: `getSecret(s)` through a memory-only TTL cache, dynamic-lease issue/renew/revoke, typed errors/exceptions (401/403/404/503). Follow-ups: background auto-renew + `Run`-style helpers. | ~~L~~ |
| ~~More rotators: MySQL, Redis ACL, AWS IAM access keys, generic OAuth client-credential refresh~~ **ALL SHIPPED 2026-07-24** — the rotation engine now has **6 rotators**: `postgres`, `webhook`, `mysql` (bound-param password), `redis` (hand-rolled RESP `ACL SETUSER`, no dep), plus a new **generating-rotator** category (external system mints the credential): `oauth` (RFC 6749 client-credentials → stores the access token) + `aws_iam` (`CreateAccessKey` → stores `{access_key_id,secret_access_key}` JSON, prunes old keys, aws-sdk iam). **Migration 000037** relaxes the `rotation_policies.type` CHECK to admit all six (000010 only allowed postgres/webhook — a latent gap that had blocked mysql/redis too). Injection-safe, sanitized errors, value-free. | ~~M each~~ |

### Operations & observability

| Feature | Why | Effort |
|---|---|---|
| ~~Prometheus `/metrics` + health panel~~ **SHIPPED 2026-07-22** — see the "Open — backend / ops" entry above (`JANUS_METRICS_TOKEN`, `GET /v1/sys/status`, Settings → Health). | Self-hosting is a black box until it breaks. | ~~S~~ |
| ~~Scheduled encrypted backups to S3-compatible storage with retention + a restore-rehearsal command~~ **SHIPPED 2026-07-23** — `internal/backupsched` on `JANUS_BACKUP_TICK`: produces the sealed backup artifact, `PutObject`s to S3-compatible storage (custom endpoint for MinIO/R2/B2, static creds only), retention prune (keep N), `backup_runs` history (migration 000035). `janus backup rehearse` / `POST /v1/sys/backup/rehearse` verifies a backup restores WITHOUT clobbering live data. Sealed-artifact property preserved; value-free. `GET /v1/sys/status` gains a `backup` block. | ~~M~~ |
| ~~Audit shipping — stream JSONL to webhook/syslog/S3 for SIEM ingestion, with a high-water mark~~ **SHIPPED 2026-07-23** (webhook + syslog; S3 covered by scheduled backups) — `internal/auditship` tails the audit log past a durable high-water mark (migration 000034), ships JSONL to a webhook (optional HMAC-SHA256 sig) or RFC 5424 syslog (UDP/TCP), advances the mark only on success (at-least-once, no gaps). `JANUS_AUDIT_SHIP_*` env; `GET /v1/sys/status` gains an `audit_ship` block. Value-free. | ~~M~~ |
| ~~Health panel in Settings — DB latency, scheduler tick ages, failed-run counts~~ **SHIPPED 2026-07-22** (with Prometheus metrics — `GET /v1/sys/status` + Settings → Health). | ~~S~~ |
| ~~First-run onboarding checklist (create project → add secrets → mint token → `janus run`)~~ **SHIPPED 2026-07-23** — dashboard checklist on the Overview; steps auto-check from existing state (projects / any secret via 403-tolerant masked-list probe / `listTokens`), step 4 shows a copyable `janus login`→`setup`→`run` block; hides once set up, dismissible (localStorage). Frontend-only, no migration/endpoint. | ~~S~~ |

### UI polish

| Feature | Why | Effort |
|---|---|---|
| ~~Global key search in the command palette (search masked key names across configs)~~ **SHIPPED 2026-07-22** — `GET /v1/search/keys` (names-only, deny-by-default per-config `SecretRead` filter, no audit/no value, bounded) + palette "Secret keys" group with `?key=` editor deep-link. Adversarial review SHIP. | ~~S~~ |
| ~~Bulk row selection in the editor — multi-select → delete / promote / export~~ **SHIPPED 2026-07-23** — per-row checkboxes + select-all (filter-aware), bulk-action bar: Delete selected (stages into the dirty buffer), Reveal selected (audited per-key), Export selected (confirm-gated `.env` of the selection). Frontend-only, reuses existing audited-reveal/download flows. | ~~M~~ |
| ~~JSON/PEM awareness for file-type secrets — pretty-print, validate, syntax hint~~ **SHIPPED 2026-07-23** — client-side format sniff (content first, declared `type` as fallback) on the value being edited: JSON/PEM badge, well-formedness check (JSON parse error, PEM label/base64 faults) surfaced inline, one-click Pretty-print for valid JSON. Advisory only — never blocks a save; nothing leaves the browser. | ~~S~~ |
| ~~Shortcuts help modal (`?`) + `g`-prefixed nav chords~~ **SHIPPED 2026-07-23** — `?` opens a shortcuts modal (palette action too); `g` + letter jumps to any screen (`g p` Projects, `g a` Audit, …). Chords are suppressed while typing, with modifiers, or while a dialog is open; a pending-chord hint shows after `g`. | ~~S~~ |
| ~~Accessibility pass — focus traps in modals, ARIA on tables, reduced-motion audit~~ **SHIPPED 2026-07-24** — reusable `trapFocus` action (focus-in + `Tab` cycle + restore-on-close) on all 3 modal overlays with `role="dialog"`/`aria-modal`; `aria-label` + `<th scope="col">` across all 22 ledger tables; `ProjectBoard` drop columns `role="group"`; hardened `prefers-reduced-motion` rule. svelte-check now **0 errors / 0 warnings** (was 7). | ~~M~~ |
| Mobile/tablet layout for read-mostly screens (dashboard, audit, approvals) | Approving a promotion from a phone is a real workflow. | M |

### Trust & supply chain

| Feature | Why | Effort |
|---|---|---|
| ~~`SECURITY.md` — vulnerability disclosure policy~~ **SHIPPED 2026-07-25** — GitHub private vulnerability reporting as the primary channel (email fallback), response-target table, supported-versions, in/out-of-scope tied to the threat model, safe-harbor, and a "verify what you run" pointer. | ~~S~~ |
| ~~Signed releases — cosign keyless + SBOM (syft) + SLSA provenance~~ **SHIPPED 2026-07-25** — goreleaser gains Syft SBOMs (archives), Cosign **keyless** signature over `checksums.txt`, and Cosign signing of the multi-arch GHCR manifests by digest; the release workflow adds `attestations: write`, installs Cosign+Syft, and emits **SLSA build-provenance** (`actions/attest-build-provenance`) for binaries/archives **and** the image (by resolved digest) plus an image-SBOM attestation. Validated by `goreleaser check`; end-to-end exercise needs a real `v*` tag. | ~~S–M~~ |
| ~~Dependabot — automated dependency update PRs (Go, npm, actions)~~ **SHIPPED 2026-07-25** — `.github/dependabot.yml`: weekly grouped PRs across all 3 Go modules (root/`sdk/go`/`terraform-provider-janus`), both npm packages (`web`/`sdk/ts`), the Python SDK, and GitHub Actions. | ~~S~~ |
| ~~Threat-model document — what Janus defends against and explicitly what it does not~~ **SHIPPED 2026-07-25** — `docs/threat-model.md`: trust boundaries, assets, a 7-actor adversary table, defended properties→mechanisms, explicit **non-defenses** (host/DBA/owner/multi-tenancy/DoS), operator responsibilities, and crypto assumptions. `SECURITY.md` scope references it. | ~~S~~ |

### Product depth (post-1.0)

| Feature | Why | Effort |
|---|---|---|
| ~~`janus run --watch`~~ **DONE 2026-07-25 (PR #152, pending merge)** — polls the bound config's current version (value-free) and gracefully restarts the child on a bump (SIGTERM→5s grace→Kill, build-tagged per-OS; Windows Kill), re-fetching secrets + re-spawning with fresh env; `--watch-interval` (default 10s). No `--watch` = unchanged. | ~~M~~ |
| ~~`janus render`~~ **DONE 2026-07-25 (PR #152, pending merge)** — `--template <f> --out <f> [--watch] [--interval]`: Go `text/template` (missingkey=error), secrets as `{{ .KEY }}` + `secret "KEY"` func; atomic `0600` write (shared with `download --plain`) + plaintext-file notice; `--watch` re-renders on version bumps via the shared poll helper. | ~~M~~ |
| ~~Kubernetes service-account OIDC federation (cluster issuers in the existing trust bindings)~~ **SHIPPED 2026-07-25 (PR #174)** — federation trust became **multi-issuer** (migration 000042, existing row backfilled) so cluster workloads and CI can be trusted at once; previously the single-issuer config made them mutually exclusive. Verifier is selected by the token's `iss` then re-pinned after signature verification, and bindings are filtered by the **signing** issuer before any claim comparison. Nested claims flatten to dotted paths (`kubernetes.io.namespace`); genuine ambiguity (`{"a.b"}` vs `{"a":{"b"}}`) **rejects the token** rather than picking a precedence. `kubernetes` preset forces a binding to pin SA identity. | ~~M~~ |
| ~~Sync drift detection — scheduled verify pass reads targets back, flags tampering (in-tray + notification)~~ **SHIPPED 2026-07-25 (PR #175)** — optional `Verifier` interface with an honest capability level: **value drift** for `k8s`/`gitlab`/`aws_ssm`/`aws_secrets`/`vercel`/`netlify`; **names-only** for `github`/`cloudflare`, which are write-only by design and surface `values_compared: false` so a clean result is never mistaken for "values verified". Comparison is `hmac.Equal` over keyed HMACs — digests never persisted or logged. Migration 000041, `JANUS_SYNC_VERIFY_TICK` (default off), `sync.drift` notification, ops-console column. **Also fixed a latent bug it uncovered:** migration 000011 pinned `sync_targets.provider` to `CHECK (provider IN ('github','k8s'))` and nothing ever widened it, so **6 of the 8 providers could never be persisted** — see the corrected note in Integrations & delivery. | ~~M~~ |
| ~~WebAuthn/passkeys for UI login~~ **SHIPPED 2026-07-25 (PR #176)** — enrolment, single-step passkey sign-in, and credential management (migration 000040). `go-webauthn/webauthn` added under a **recorded CLAUDE.md exception** (approved 2026-07-25); only public credential material is stored, so master-key rotation has nothing to re-wrap. Challenges are single-use/expiring and the account comes from the **challenge**, never the client response; signature-counter regression is fatal (`webauthn_cloned`); `userVerification: required` on every ceremony (which is why a passkey login is not additionally TOTP-gated); RP ID/origins come from `JANUS_WEBAUTHN_*`, validated at boot, never from the request Host. Removing the last passkey cannot lock a user out. **Deferred:** passwordless/discoverable login (credentials enrol with `residentKey: preferred` so no re-enrolment is needed later). | ~~M–L~~ |
| WebAuthn passwordless / discoverable login — username-less sign-in with conditional UI (autofill) | Deferred from PR #176 rather than half-built; credentials already enrol with `residentKey: preferred`, so no re-enrolment is needed. | S–M |
| Terraform provider: environment-scoped tokens + a batch-secret resource | Deferred when the provider shipped; a per-secret resource is noisy for large configs. | M |
| SDK depth — background auto-renew for dynamic leases + `Run`-style helpers (Go/TS/Python) | Deferred when the SDKs shipped; today callers must renew leases themselves. | M |
| Web import wizard — the `janus import` sources (Doppler/Vault/AWS-SM) driven from the UI | CLI-first import shipped; a wizard was noted as the follow-up. | M |

### Operational longevity

| Feature | Why | Effort |
|---|---|---|
| ~~Audit retention with hash-chain checkpointing — signed checkpoints so shipped prefixes can be archived/pruned without breaking `audit/verify`~~ **DONE 2026-07-25 (PR #153, pending merge)** — migration `000039`; HMAC-SHA256 checkpoint MAC over length-prefixed `through_seq‖through_hash‖event_count`, key domain-separated from the master-key-wrapped token-HMAC key (`internal/crypto` untouched); owner-only `audit:manage` `POST/GET /v1/audit/checkpoint` + `POST /v1/audit/prune`; verify checks the checkpoint MAC then walks forward (forged → `checkpoint_mac_invalid`); prune fail-closed (valid checkpoint + auditship-HWM clamp + anchor-safe); audit-viewer checkpoint stamp + owner create button. Value-free; crypto gate unaffected. | ~~M–L~~ |
| ~~Secret value-version retention — optional owner-set "hard-destroy versions older than N days/versions"~~ **SHIPPED 2026-07-25 (PR #181)** — migration 000043, `secret:prune` (owner-only), `GET/PUT .../versions/retention` + `POST .../versions/prune`, `janus secrets retention|prune`, `JANUS_SECRET_RETAIN_MIN_VERSIONS`/`_MIN_DAYS`. **Prunes whole config versions, then GCs unreferenced `secret_values`** — deleting value rows directly would have cascaded away `config_version_entries` (000005) and silently stripped keys from old versions, making a rollback restore an incomplete config. The invariant *every retained config version is fully restorable* is re-asserted **inside the prune transaction** (live manifest-entry count before/after; a shortfall aborts). `dry_run` defaults **true**; CLI needs `--apply`. Conservative refusals: pending edit request blocks the whole config, pending-promotion source pinned, soft-deleted configs never pruned. **No scheduled sweep** — destroying secret history must be asked for by name. | ~~M~~ |
| ~~Grafana dashboard JSON + example alert rules in `docs/`~~ **SHIPPED 2026-07-25 (PR #180)** — `deploy/grafana/`: a 24-panel importable dashboard (`DS_PROMETHEUS` input, `$job`/`$instance` templating), 13 Prometheus-format alert rules, and a README covering token setup + `scrape_configs` with bearer auth + a `ServiceMonitor`. Verified rather than assumed: all 23 dashboard expressions evaluated through a live Prometheus scraping a real instance (23 OK, 0 empty), `promtool check/test rules` green, imported into Grafana 13.1.0. **Caught that `janus_audit_head_seq`, `janus_rotation_runs_failed` and `janus_sync_runs_failed` are gauges despite counter-ish names** — `rate()` on them is wrong, so the panels use `deriv()`/`offset` deltas. | ~~S~~ |

### Test depth

| Feature | Why | Effort |
|---|---|---|
| ~~Playwright smoke suite — browser E2E (init → unseal → login → create project → save secret → audited reveal) against the docker stack~~ **DONE 2026-07-25 (PR #151, pending merge)** — `web/tests/e2e/smoke.spec.ts` (8 steps: Shamir 5/3 init → unseal quorum → owner login → project/env/config → save → audited reveal → `secret.reveal` in ledger + chain-verified badge), `playwright.config.ts` (`JANUS_E2E_BASE_URL`, default `:8210`), opt-in `.github/workflows/e2e.yml`, additive `data-testid`s. Full run needs the live stack. | ~~M~~ |
| ~~Go fuzz tests — reference parser, `.env`/properties importers, PEM sniffing, RESP encoding, federation JWT claims~~ **SHIPPED 2026-07-25** — 7 `Fuzz*` targets asserting **invariants**, not just no-panic: `FuzzValidateKey` (path-traversal guard behind `download --format files`), `FuzzEncodeRESPNoInjection` (Redis command-injection round-trip), `FuzzParseSegments` (`${…}` reference parser — the most attacker-reachable, since any secret writer controls it), `FuzzParseEnvelope`/`FuzzEnvelopeRoundTrip` (transit ciphertext), `FuzzStringClaims`/`FuzzClaimsSatisfyFailClosed` (CI-federation claim matching). Found + hardened a latent fail-open in `claimsSatisfy` (absent claim satisfied an empty required value; not exploitable — `CreateFederationBinding` already rejects empty values and is the only write path — but the matcher no longer depends on that). `.env`/properties + PEM sniffing have **no Go parser** (client-side TS), so no Go target exists for them. | ~~S–M~~ |

### Release & distribution

_The binary + container ship automatically on a `v*` tag (goreleaser → GitHub
Release + GHCR). The language-package registries do **not** — each needs an
account/credential the maintainer must supply, so they are tracked separately._

- [x] ~~**v0.1.0 — first tagged release**~~ **SHIPPED 2026-07-24** — tag `v0.1.0`
      (`main 9490a7d`); goreleaser published the GitHub Release (6 multi-arch
      binaries + `checksums.txt`) and the multi-arch GHCR image
      `ghcr.io/steveokay/janus:0.1.0`. CHANGELOG consolidated (nothing was tagged
      before → all of `main` ships as 0.1.0).
- [x] ~~**Pre-release security hardening (PR #148)**~~ **SHIPPED 2026-07-24** —
      the consolidation security review's findings: closed two require-approval
      (four-eyes) bypasses where **rollback** and **promote-apply** committed
      directly to protected configs (both now route the changeset through the
      edit-request flow via `promote.Plan` / `secrets.RollbackChanges`); made
      edit-request approval **claim-before-commit** (`ClaimForApply` CAS → no
      double-commit); added **GitLab sync project-id validation** (request-URL
      injection guard); protected pending edit requests from **KEK-version
      retirement** during project-KEK rotation. gosec 0, full suite green.
- [x] ~~**Post-1.0 white-box audit remediation**~~ **DONE 2026-07-24** (branch
      `hardening/security-audit-fixes`, pending merge; findings kept in the
      local-only internal audit tracker, not published) — a 5-agent white-box
      audit of the whole system found **0 CRITICAL**. The single **HIGH** (H-1,
      promotion-request four-eyes) was re-examined and is **not** an exploitable
      bypass — `secret:promote` is only granted by the developer role, which also
      grants `secret:write`, and requester ≠ approver is enforced, so the approval
      is always a write-authorized two-party action; the invariant is documented
      at `promotion_request_handlers.go` rather than "fixed" with unreachable code.
      Fixed:
      - **M-1** — `memberPut` delegation cap now uses `BoundRole`, not
        `EffectiveRole`, so a break-glass-elevated user can no longer mint a
        **durable** binding at the elevated role that outlives the grant.
      - **M-2** — TOTP codes are no longer replayable: the last consumed step is
        persisted per user (`user_totp.last_step`, migration 000038) and any code
        at a step `<=` it is rejected.
      - **M-3** — a password change now `RevokeOtherSessions` + rotates the
        caller's own session cookie (stolen cookies die immediately).
      - **M-4 / L-2** — systemic SSRF closed with one shared `internal/nethard`
        hardened dialer: `net.Dialer.Control` re-checks the **resolved** IP on
        every dial (defeats DNS-rebinding), blocking link-local/cloud-metadata
        (169.254/fe80::/fd00:ec2::254) unconditionally + optional private-range
        block (`JANUS_OUTBOUND_BLOCK_PRIVATE`); `CheckRedirect` caps hops + scheme;
        bounded per-dial timeouts. Applied to **every** operator-config outbound
        caller — rotation (webhook/oauth/notify + Postgres/MySQL/Redis dials),
        notification (webhook/Slack/SMTP), sync (k8s/gitlab/cloudflare/vercel/
        netlify), and **OIDC discovery/JWKS** via `oidc.ClientContext` (I-4).
        Default policy permits loopback/RFC1918/ULA since self-hosted targets are
        legitimate.
      - **L-1** — uniform `X-Content-Type-Options`/framing/CSP middleware on all
        `/v1/` responses. **L-5** — `.dockerignore` added.
      - **L-3** (cross-IP account-lockout DoS), **L-4** (dev-compose password /
        `sslmode`), **L-6** (rotation writes bypass `require_approval`) — accepted
        tradeoffs, now explicitly documented.

      gosec 0; `internal/crypto` still 100%. INFO I-1/I-2/I-3 remain accepted
      defense-in-depth notes.
- [ ] **Publish the TypeScript SDK to npm** (`janus-client`, `sdk/ts/`) — needs
      an npm account + automation token. Not covered by the existing release
      workflow (binaries + GHCR only); add an npm-publish CI job (on an
      `sdk-ts-v*` tag or manual dispatch) running `npm publish` with
      `NPM_TOKEN`.
- [ ] **Publish the Python SDK to PyPI** (`janus_client`, `sdk/python/`) — needs
      a PyPI project + token (OIDC Trusted Publishing preferred). Add a build +
      publish job (`python -m build` → `pypa/gh-action-pypi-publish`).
- [ ] **Publish the Terraform provider to the Terraform Registry**
      (`terraform-provider-janus/`) — needs a **GPG signing key** and a Registry
      account linked to the repo, plus the provider's **own** goreleaser release
      workflow (the Registry ingests GPG-signed release archives from a `v*` tag
      in the provider module). Deferred provider work rides along: env-scoped
      tokens + a batch-secret resource.

### Suggested near-term slate

**The original roadmap (sections 1–5) is exhausted** and **v0.1.0 is released**
(see Release & distribution). The post-1.0 roadmap is the four new sections
above. Suggested first batch — **"Trust & Longevity"**, all parallel-friendly:

1. ~~**Trust & supply chain sweep**~~ **DONE 2026-07-25** — `SECURITY.md` +
   `docs/threat-model.md` + `.github/dependabot.yml` + cosign/SBOM/SLSA-provenance
   in goreleaser & the release workflow.
2. ~~**`janus run --watch` + `janus render`**~~ **DONE 2026-07-25 (PR #152).**
3. ~~**Audit chain checkpointing + retention**~~ **DONE 2026-07-25 (PR #153).**
4. ~~**Playwright smoke suite**~~ **DONE 2026-07-25 (PR #151).**

Then, as demand dictates: registry publishes (the three open boxes above, each
gated on a maintainer credential), k8s SA federation, sync drift detection,
passkeys, value-version retention, Grafana dashboard, fuzzing, mobile/tablet
layout (5.6), import web wizard, and SDK depth (background auto-renew +
`Run`-style helpers).

Both parked decisions are **resolved**. Still outstanding among the small
backend/ops items: DB pool tuning, docker-compose resource limits, and
`CONTRIBUTING.md` (folded into the ops-hardening bundle above).
