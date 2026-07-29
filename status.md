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
ledger, tokens, scoped members (effective role + direct/via-group source),
groups, transit, an operations
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
      Documented in [two-factor-auth guide](docs/guides/two-factor-auth.md#scope-password-logins-only-not-oidc-decided-2026-07-23)
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
| ~~Secret annotations — owner + note metadata per key (never values)~~ **SHIPPED 2026-07-23**, **REVISED 2026-07-28** — `config_secret_annotations` (migration 000033, value-free); `PUT /v1/configs/{cid}/secrets/{key}/annotation`, `secret:write` to set / `secret:read` to view; value-free audit. **Ownership moved to the PROJECT (migration 000049):** per-key `owner` was the wrong grain — a service has an owner, its keys almost never do, so the field was repeated or empty — and it read confusingly beside group-owned projects. `owner` is now an advisory label on the project (`PATCH /v1/projects/{pid}`, `project:update`), never an authorization input; the per-key **note** stays. Sending `owner` to the key endpoint is a 400, not a silent drop. Upgrade preserves operator data: a single agreed owner is promoted to the project, disagreeing ones fold into each key's note. | ~~M~~ |
| ~~Require-approval-for-prod-edits toggle — direct saves to protected configs become a promotion-style request~~ **SHIPPED 2026-07-24** — per-config `require_approval` flag (`promotion:manage` to toggle); a save to a protected config files a **pending edit request** (`202`, not a commit) with the proposed changes **envelope-encrypted** (fresh DEK under the project KEK, domain-separated `ConfigEditRequestAAD`); a **different** user with `secret:write` approves (four-eyes, self-approval `403`, mark-on-success CAS → commit via `SetSecrets`) or rejects; requester cancels. Migration 000036. Value-free (key names only); crypto 100%. Editor Protect toggle + Approvals section. **Hardened 2026-07-24 (PR #148):** rollback + promote-apply now honor `require_approval` too (route the changeset through the edit-request flow instead of committing directly), and approval is claim-before-commit (no double-commit) — see Release & distribution below. | ~~M~~ |

### Integrations & delivery

| Feature | Why | Effort |
|---|---|---|
| ~~More sync providers: GitLab CI, Cloudflare Workers, Vercel/Netlify env, AWS SSM/Secrets Manager~~ **ALL SHIPPED 2026-07-23** — the sync engine now has **8 providers**: `github`, `k8s`, `gitlab`, `aws_ssm`, `cloudflare`, `aws_secrets`, and **`vercel` + `netlify`** (both net/http, upsert+prune, `api_token` cred; #130 also restored Cloudflare's REST decode fields). No migration. (GitLab CI/CD variables, GitHub Actions, K8s Secrets, Cloudflare Workers secrets, Vercel/Netlify env vars, AWS SSM Parameter Store + Secrets Manager.)  **CORRECTION (2026-07-25):** these were shipped in code but NOT actually usable — migration `000011` pinned `sync_targets.provider` to `CHECK (provider IN ('github','k8s'))` and no later migration widened it, so persisting a `gitlab`/`aws_ssm`/`cloudflare`/`aws_secrets`/`vercel`/`netlify` target failed the constraint. Found and fixed by migration `000041` (PR #175), with a store regression test creating a target for all eight. Same class of bug as the `rotation_policies.type` CHECK that `000037` fixed — a CREATE-time enum later features outgrew. | ~~M each~~ |
| ~~More CI federation issuers: GitLab, Buildkite, CircleCI OIDC~~ **SHIPPED 2026-07-23** — provider-aware required-claim rule (replaces the hardcoded GitHub `repository` requirement: GitHub→`repository`, GitLab→`project_path`, Buildkite→`organization_slug`, CircleCI→org/project claim; unknown issuer → any non-empty claim), issuer presets + URL validation, single-active-issuer model, `web/src/lib/federation.ts` preset dropdown. No migration. | ~~S each~~ |
| ~~Inbound one-shot importers: Doppler, Vault KV, AWS SM → project/config tree~~ **SHIPPED 2026-07-24** — CLI-first `janus import doppler|vault|aws-sm`: fetches from the source (creds from flags/env, never stored), maps → a Janus project/env/config (create-if-missing), writes as one batched config version via the existing authed client. **Default `--dry-run`** prints key names + counts only (never values); `--confirm` writes. Doppler/Vault via net/http, AWS-SM via the existing aws-sdk. No new server endpoint/migration/dep. **Web wizard shipped 2026-07-26 (PR #187)** — see Product depth; it is paste-based, so the browser never holds a source credential. | ~~L~~ |
| ~~Notifications: webhook + Slack for rotation failures, sync errors, denials, pending approvals~~ **SHIPPED 2026-07-21** — audit-tailing dispatcher + delivery outbox; webhook + Slack channels; `notification:manage`, `/v1/notifications/channels`, `janus notifications` CLI, Notifications web screen; migration 000024. **SMTP email channel added 2026-07-23** (`type=smtp`, `net/smtp` STARTTLS/implicit/none, verify-by-default + per-channel `insecure_skip_verify`, write-only password, value-free body; migration 000027). | Failures must find humans, not just an in-app tray. | ~~M~~ |
| ~~Terraform provider (projects, configs, secrets-as-writes, tokens, bindings)~~ **SHIPPED 2026-07-24** — `terraform-provider-janus/` (own Go module, terraform-plugin-framework): resources `janus_project`/`environment`/`config`/`secret` (value `Sensitive`)/`service_token` (minted token = sensitive computed, secrets-in-state caveat documented) + `janus_secret`/`janus_config` data sources; full CRUD + import, 404→drift, `JANUS_ADDR`/`JANUS_TOKEN`. Hermetic httptest unit tests. **Env-scoped tokens + the `janus_secrets` batch resource shipped 2026-07-26 (PR #186)** — see Product depth. Still deferred: the Terraform Registry publish (needs a GPG key + Registry account — see Release & distribution). | ~~L~~ |
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
| ~~**Mobile/tablet layout** for read-mostly screens (dashboard, audit, approvals)~~ **SHIPPED 2026-07-26 (PR #192)** — the SPA had **no shell breakpoint at all**: on a 390px phone the 236px cover took 60% of the screen and the remainder was **clipped, not scrolled** (`.desk` is `overflow: hidden`), so headline, folio bar and every ledger column were cut off with no way to reach them. The load-bearing fix is one line — `.desk { min-width: 0 }`. A grid item defaults to `min-width: auto` ("never narrower than my content"), and **ten screens already declared `.table-wrap { overflow-x: auto }`** whose scroll simply never engaged because the track never had to shrink. Below 1024px the cover becomes an off-canvas drawer (scrim, Escape, close-on-navigate, `aria-expanded`/`-controls`, `visibility: hidden` while closed so it is not a keyboard trap); 1024px keeps the sidebar for tablet **landscape**. `trapFocus` gained an optional `enabled` param because the cover is always mounted and only sometimes modal. One global `.page-head { flex-wrap: wrap }` fixes all **14** screens that declare it identically. Caught en route: the Audit result filter's `denied` segment was pushed off-screen and **unreachable** on a phone, and the Overview rosette's `right: -40px` bleed had been putting a horizontal scrollbar on **every width, desktop included**. Ships `web/tests/e2e/shots.mjs`, a read-only harness that screenshots every screen at phone/tablet/laptop in both themes and measures the **content area** — measuring only the document reported everything green, which is exactly how this shipped unnoticed. | ~~M~~ |

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
| ~~`janus run --watch`~~ **SHIPPED 2026-07-25 (PR #152)** — polls the bound config's current version (value-free) and gracefully restarts the child on a bump (SIGTERM→5s grace→Kill, build-tagged per-OS; Windows Kill), re-fetching secrets + re-spawning with fresh env; `--watch-interval` (default 10s). No `--watch` = unchanged. | ~~M~~ |
| ~~`janus render`~~ **SHIPPED 2026-07-25 (PR #152)** — `--template <f> --out <f> [--watch] [--interval]`: Go `text/template` (missingkey=error), secrets as `{{ .KEY }}` + `secret "KEY"` func; atomic `0600` write (shared with `download --plain`) + plaintext-file notice; `--watch` re-renders on version bumps via the shared poll helper. | ~~M~~ |
| ~~Kubernetes service-account OIDC federation (cluster issuers in the existing trust bindings)~~ **SHIPPED 2026-07-25 (PR #174)** — federation trust became **multi-issuer** (migration 000042, existing row backfilled) so cluster workloads and CI can be trusted at once; previously the single-issuer config made them mutually exclusive. Verifier is selected by the token's `iss` then re-pinned after signature verification, and bindings are filtered by the **signing** issuer before any claim comparison. Nested claims flatten to dotted paths (`kubernetes.io.namespace`); genuine ambiguity (`{"a.b"}` vs `{"a":{"b"}}`) **rejects the token** rather than picking a precedence. `kubernetes` preset forces a binding to pin SA identity. | ~~M~~ |
| ~~Sync drift detection — scheduled verify pass reads targets back, flags tampering (in-tray + notification)~~ **SHIPPED 2026-07-25 (PR #175)** — optional `Verifier` interface with an honest capability level: **value drift** for `k8s`/`gitlab`/`aws_ssm`/`aws_secrets`/`vercel`/`netlify`; **names-only** for `github`/`cloudflare`, which are write-only by design and surface `values_compared: false` so a clean result is never mistaken for "values verified". Comparison is `hmac.Equal` over keyed HMACs — digests never persisted or logged. Migration 000041, `JANUS_SYNC_VERIFY_TICK` (default off), `sync.drift` notification, ops-console column. **Also fixed a latent bug it uncovered:** migration 000011 pinned `sync_targets.provider` to `CHECK (provider IN ('github','k8s'))` and nothing ever widened it, so **6 of the 8 providers could never be persisted** — see the corrected note in Integrations & delivery. | ~~M~~ |
| ~~WebAuthn/passkeys for UI login~~ **SHIPPED 2026-07-25 (PR #176)** — enrolment, single-step passkey sign-in, and credential management (migration 000040). `go-webauthn/webauthn` added under a **recorded CLAUDE.md exception** (approved 2026-07-25); only public credential material is stored, so master-key rotation has nothing to re-wrap. Challenges are single-use/expiring and the account comes from the **challenge**, never the client response; signature-counter regression is fatal (`webauthn_cloned`); `userVerification: required` on every ceremony (which is why a passkey login is not additionally TOTP-gated); RP ID/origins come from `JANUS_WEBAUTHN_*`, validated at boot, never from the request Host. Removing the last passkey cannot lock a user out. **Deferred:** passwordless/discoverable login (credentials enrol with `residentKey: preferred` so no re-enrolment is needed later). | ~~M–L~~ |
| ~~WebAuthn passwordless / discoverable login — username-less sign-in with conditional UI (autofill)~~ **SHIPPED 2026-07-26 (PR #184)** — `POST /v1/auth/webauthn/login/discoverable/{begin,finish}`, migration 000044. Identity is resolved **from the stored credential, never the client-supplied `userHandle`**: look up by `rawID` → take the credential row's owner → require the presented handle to equal that owner's (constant-time) → hand the library only that owner's credential set. So A's credential presented with B's handle is rejected (credential substitution), pinned by `TestWebAuthnDiscoverableIdentityBinding`. Separate `login_discoverable` challenge pool; every failure returns a byte-identical 401 (no enumeration oracle — there is no email at begin). New enrolments use `residentKey: required` + `credProps`, with a **Passwordless** column in Settings so a user is never left guessing. Conditional UI (autofill) shipped, feature-detected and abortable. Browser-verified: 12/12 Playwright. | ~~S–M~~ |
| ~~Terraform provider: environment-scoped tokens + a batch-secret resource~~ **SHIPPED 2026-07-26 (PR #186)** — `janus_service_token` gains `scope_kind` (`config` default \| `environment`) alongside the existing `scope` UUID, validated by a hand-rolled `stringOneOf` so a typo fails at **`plan`**, locally, instead of round-tripping to a server 400 (and no new dependency — the provider still needs only terraform-plugin-framework). The new **`janus_secrets`** resource writes a whole key→value map as **ONE config version** rather than one per key, matching how the editor and API already model a save: a 40-key config is one version bump, not 40. Keys absent from the map are never touched, so `janus_secrets` and per-key `janus_secret` can coexist on one config. **Drift detection is deliberately metadata-only** — the masked list endpoint is value-free, so the provider *cannot* compare stored plaintext and doesn't pretend to; it tracks each key's server-side `value_version` in a separate non-sensitive `value_versions` map, and a moved counter means an out-of-band write. That companion map exists for a second reason: because `secrets` is `Sensitive` **in full**, plan output masks key *names* as well as values, so without it an operator could not see which keys a plan touches. | ~~M~~ |
| ~~SDK depth — background auto-renew for dynamic leases + `Run`-style helpers (Go/TS/Python)~~ **SHIPPED 2026-07-26 (PR #183)** — one policy across all three: renew at 2/3 of remaining TTL ±10% jitter, retries at half that so they converge on expiry instead of hot-looping. **Opt-in** (never a background task behind the caller's back), cleanly stoppable, zero new dependencies, hermetic tests with injected clocks (36 Go `-race`, 37 TS, 38 Python). `RunWithDynamic` / `withDynamic` / `dynamic_lease` revoke on success, error and panic, without masking the caller's own error. Key find: `internal/dynamic/lease.go` **clamps** a renewal to `MaxExpiresAt` and returns 200, so hitting the server-side ceiling looks like success — the loop detects it two ways (`expires_at >= max_expires_at`, or an expiry that stops advancing) and terminates instead of spinning. | ~~M~~ |
| ~~Web import wizard — the `janus import` sources (Doppler/Vault/AWS-SM) driven from the UI~~ **SHIPPED 2026-07-26 (PR #187)** — a format picker on the editor's existing Import flow: `env` (unchanged), **Doppler** (flat JSON), **Vault KV v2** (unwraps the `.data.data` envelope the CLI prints), **AWS Secrets Manager** (parses the JSON inside `SecretString`). Deliberately **paste-based**: unlike the CLI importer, the browser never holds a Doppler/Vault/AWS credential and makes **no outbound call of its own** — so the wizard adds no CORS, SSRF or third-party-credential surface to the server. Each format shows the exact source CLI command that produces the paste, so the operator pipes it themselves. Parsed entries land in the existing per-key preview (new/overwrite/invalid) and commit as one config version through the unchanged dirty-buffer path; parsing is pure client-side and unit-tested (`web/tests/unit/import-formats.test.ts`). | ~~M~~ |

### Operational longevity

| Feature | Why | Effort |
|---|---|---|
| ~~Audit retention with hash-chain checkpointing — signed checkpoints so shipped prefixes can be archived/pruned without breaking `audit/verify`~~ **SHIPPED 2026-07-25 (PR #153)** — migration `000039`; HMAC-SHA256 checkpoint MAC over length-prefixed `through_seq‖through_hash‖event_count`, key domain-separated from the master-key-wrapped token-HMAC key (`internal/crypto` untouched); owner-only `audit:manage` `POST/GET /v1/audit/checkpoint` + `POST /v1/audit/prune`; verify checks the checkpoint MAC then walks forward (forged → `checkpoint_mac_invalid`); prune fail-closed (valid checkpoint + auditship-HWM clamp + anchor-safe); audit-viewer checkpoint stamp + owner create button. Value-free; crypto gate unaffected. | ~~M–L~~ |
| ~~Secret value-version retention — optional owner-set "hard-destroy versions older than N days/versions"~~ **SHIPPED 2026-07-25 (PR #181)** — migration 000043, `secret:prune` (owner-only), `GET/PUT .../versions/retention` + `POST .../versions/prune`, `janus secrets retention|prune`, `JANUS_SECRET_RETAIN_MIN_VERSIONS`/`_MIN_DAYS`. **Prunes whole config versions, then GCs unreferenced `secret_values`** — deleting value rows directly would have cascaded away `config_version_entries` (000005) and silently stripped keys from old versions, making a rollback restore an incomplete config. The invariant *every retained config version is fully restorable* is re-asserted **inside the prune transaction** (live manifest-entry count before/after; a shortfall aborts). `dry_run` defaults **true**; CLI needs `--apply`. Conservative refusals: pending edit request blocks the whole config, pending-promotion source pinned, soft-deleted configs never pruned. **No scheduled sweep** — destroying secret history must be asked for by name. | ~~M~~ |
| ~~Grafana dashboard JSON + example alert rules in `docs/`~~ **SHIPPED 2026-07-25 (PR #180)** — `deploy/grafana/`: a 24-panel importable dashboard (`DS_PROMETHEUS` input, `$job`/`$instance` templating), 13 Prometheus-format alert rules, and a README covering token setup + `scrape_configs` with bearer auth + a `ServiceMonitor`. Verified rather than assumed: all 23 dashboard expressions evaluated through a live Prometheus scraping a real instance (23 OK, 0 empty), `promtool check/test rules` green, imported into Grafana 13.1.0. **Caught that `janus_audit_head_seq`, `janus_rotation_runs_failed` and `janus_sync_runs_failed` are gauges despite counter-ish names** — `rate()` on them is wrong, so the panels use `deriv()`/`offset` deltas. | ~~S~~ |

### Test depth

| Feature | Why | Effort |
|---|---|---|
| ~~Playwright smoke suite — browser E2E (init → unseal → login → create project → save secret → audited reveal) against the docker stack~~ **SHIPPED 2026-07-25 (PR #151)** — `web/tests/e2e/smoke.spec.ts` (8 steps: Shamir 5/3 init → unseal quorum → owner login → project/env/config → save → audited reveal → `secret.reveal` in ledger + chain-verified badge), `playwright.config.ts` (`JANUS_E2E_BASE_URL`, default `:8210`), opt-in `.github/workflows/e2e.yml`, additive `data-testid`s. Full run needs the live stack. | ~~M~~ |
| ~~Go fuzz tests — reference parser, `.env`/properties importers, PEM sniffing, RESP encoding, federation JWT claims~~ **SHIPPED 2026-07-25** — **8** `Fuzz*` targets asserting **invariants**, not just no-panic: `FuzzValidateKey` (path-traversal guard behind `download --format files`), `FuzzEncodeRESPNoInjection` (Redis command-injection round-trip), `FuzzParseSegments` (`${…}` reference parser — the most attacker-reachable, since any secret writer controls it), `FuzzParseEnvelope`/`FuzzEnvelopeRoundTrip` (transit ciphertext), and `FuzzStringClaims`/`FuzzClaimsSatisfyFailClosed`/`FuzzFlattenClaims` (CI-federation claim matching — the third added with multi-issuer federation in PR #174, pinning that nested-claim flattening never invents an ambiguous dotted path). Found + hardened a latent fail-open in `claimsSatisfy` (absent claim satisfied an empty required value; not exploitable — `CreateFederationBinding` already rejects empty values and is the only write path — but the matcher no longer depends on that). `.env`/properties + PEM sniffing have **no Go parser** (client-side TS), so no Go target exists for them. | ~~S–M~~ |
| ~~CI coverage for the shipped-but-untested modules~~ **SHIPPED 2026-07-26** — `go test ./...` in the `test` job covers the **root module only**: `./...` does not descend into nested modules, so `sdk/go` and `terraform-provider-janus` (each with its own `go.mod`) were never built, vetted or tested in CI, and neither were `sdk/ts` or `sdk/python`. Dependabot watches **7** directories; CI verified **2** of them — so a bump to any SDK or to the Terraform provider could merge green with nothing running its tests, and #186 added provider resources whose tests had only ever run locally. Adds a `go-modules` matrix (build + vet + `test -race`) plus `sdk-ts` (typecheck + tests) and `sdk-python` jobs. Python runs on **3.9**, the floor `requires-python` advertises, since testing on 3.14 would let a 3.10-only construct through and silently break the compatibility the package promises. All four were green when wired up — this locks that in rather than fixing a break. | ~~S~~ |

### Release & distribution

_The binary + container ship automatically on a `v*` tag (goreleaser → GitHub
Release + GHCR). The language-package registries do **not** — each needs an
account/credential the maintainer must supply, so they are tracked separately._

- [x] ~~**v0.2.0 — the post-1.0 release**~~ **TAGGED 2026-07-26** — everything
      built after v0.1.0 in one release: passkeys (incl. passwordless), Kubernetes
      service-account federation over a multi-issuer trust set, sync drift
      detection, value-version retention, audit checkpointing, the Grafana
      dashboard, SDK lease auto-renew, the Terraform batch resource + env-scoped
      tokens, the web import wizard, the mobile/tablet layout, and this week's
      fixes (Trash destroy, the passkey-enrolment logout, expired-session
      reporting). **Also repoints `ghcr.io/steveokay/janus:latest`**, which had
      been hijacked by the `v0.1.1-rc1` prerelease: `latest` carries
      `skip_push: auto`, so a stable tag reclaims it automatically — no manual
      registry surgery, and the `-rc` regression cannot recur.
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
- [x] ~~**Post-1.0 white-box audit remediation**~~ **SHIPPED 2026-07-25** (PR #149,
      merged to `main`; findings kept in the
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
      in the provider module). The provider itself is now feature-complete for
      this milestone — the previously-deferred env-scoped tokens and batch-secret
      resource shipped 2026-07-26 (PR #186), so **only the credential-gated
      publish remains**.

### Post-v0.2.0 — work generated by using the product

**These did not come from the roadmap.** They came from two bugs the maintainer
hit in normal use and one incident during development, which is the intended
source of new work now that both roadmaps are exhausted.

Shipped 2026-07-26/27:

- [x] ~~**Owner disaster recovery**~~ **SHIPPED (PR #198)** — `janus admin
      reset-password` closes a gap where losing the last owner password meant
      destroying every secret in the instance (the documented remedy was
      `docker compose down -v`). Local console only, no HTTP surface; requires
      the seal material — Shamir quorum or cloud KMS — as an authority control
      rather than a technical need. Sessions revoked before the credential is
      replaced; a failed audit append rolls the credential back. See
      [disaster-recovery guide](docs/guides/disaster-recovery.md).
- [x] ~~**`janus doctor`**~~ **SHIPPED (PR #200)** — 19 preflight checks for
      configuration that parses, passes boot validation, and is still wrong.
      Motivated by a real incident: passkey origins naming a port the server
      does not serve, which fails the ceremony with no server-side error and
      reads like a product bug. Reuses the server's own config parse so it
      cannot drift, and a test walks the repo for `os.Getenv("JANUS_…")`
      literals so the unknown-variable check cannot fall behind. See
      [troubleshooting guide](docs/guides/troubleshooting.md).
- [x] ~~**E2E coverage of the destructive + security screens**~~ **SHIPPED
      (PR #199)** — 26 tests over Trash, tokens, members, four-eyes approvals
      and break-glass. The prior suite covered the happy path; everything it
      missed was irreversible or a security control, which is exactly where the
      Trash destroy bug (#191) lived. Verified to fail against the pre-fix
      handlers.
- [x] ~~**`janus master-key rekey` could not read a piped quorum**~~ **FIXED
      (PR #198)** — the share reader built a fresh `bufio.Reader` per share, so
      bufio's read-ahead swallowed shares 2..n. `unseal` takes one share per
      invocation and never hit it.

### Open — RBAC at organisation scale

_The target shape: an organisation with many product teams, each owning some
projects, unable to see each other's secrets — with instance owner/admin able to
see everything. Janus already does the visibility half; what it lacks is
everything that makes that arrangement manageable, delegable and auditable for
more than a handful of people. Nothing here is a security hole — the server
denies correctly today — they are the gaps between "correct" and "usable by an
org"._


Raised 2026-07-27 after walking the model with a real no-binding account. **The
visibility half already works**: `handleProjectList` filters per item on
`project:read`, so a fresh account sees `projects: 0` — a project is invisible
unless you are bound to it (same for `/v1/trash` and `/v1/tokens`). What is
missing is everything that makes that manageable for more than a handful of
people.

**Recommended order (decided 2026-07-27).** These are not independent — the
first one is what makes the other two worth doing. **(1) shipped 2026-07-27**;
(2) and (3) remain:

1. **Groups.** Everything else is downstream. It is the only item that fixes
   binding sprawl, makes offboarding a single action in the IdP, and makes
   "Team Payments owns these projects" expressible rather than approximated by
   dozens of per-user rows.
2. **Delegated project creation.** Without it an org must choose between teams
   self-serving and teams being isolated, and it will choose self-serve.
3. **Scoped audit read.** So a team can audit itself without being handed every
   other team's trail.

Those three are what make Janus genuinely multi-team. The rest of this section
is real but can wait: UI permission gating is cosmetic, `secret:read`
granularity is a larger design conversation, and the union/no-deny edge is
better handled by defaulting prod configs to `require_approval` than by new
authorization machinery.

- [x] ~~**(1) Group-based role bindings, driven by the IdP.**~~ **SHIPPED
      2026-07-27** — migration 000045 (`groups`, `group_members`,
      `group_role_bindings`, plus `oidc_providers.groups_claim`). A binding may
      target a **group** instead of a user.

      **Two kinds, never both.** `oidc` groups carry a `claim_value` and their
      membership is a snapshot refreshed from the IdP's group claim at each
      login — an admin can never hand-add a member. `local` groups have an
      explicit admin-managed list, which is what an instance without an IdP
      uses and what covers password logins. The distinction is enforced in the
      schema, not by a handler: `group_members` carries a denormalised
      `group_kind` and a composite FK to `groups(id, kind)`, so a hand-added
      member of an IdP-fed group is **unrepresentable**. That is what lets us
      state and hold *access granted via an IdP group is fully described by the
      IdP* — an access review run against Entra is therefore complete for those
      bindings. A hybrid group was considered and rejected on three edge cases:
      cross-authority collision (an identity team creating a same-named group
      injects members into a locally-bound one), incident-born permanent grants
      (an IdP outage tempts a local add that then outlives the incident
      forever, because a login sync only clears rows it owns — break-glass
      already covers temporary access, TTL-clamped and expiring), and the
      review returning a clean result that is not true. Group names are unique
      across kinds, and `name` is separate from `claim_value` because Entra
      emits GUIDs.

      **The engine stayed pure.** Group bindings arrive through an optional
      second source, `WithGroups`, mirroring `WithGrants`, as ordinary
      `store.RoleBinding` values stamped with `ViaGroupID`. `userAllows`,
      `bindingApplies`, `BoundRole` and `EffectiveRole` gained **no new
      concepts** — they see one longer slice, so group bindings union with
      direct ones exactly as two direct bindings do, with no precedence tier.
      It fails **closed**: a group-store error denies rather than silently
      resolving against direct bindings alone.

      **A group binding can never be owner** (`CHECK` + a 400 at the API).
      Owner rotates the master key, prunes the audit chain and hard-destroys
      secret history — the destroy-the-evidence tier — and group-deriving it
      would hand that to whoever administers the IdP, who can add themselves
      silently and whose membership list Janus cannot authoritatively
      enumerate. A consequence worth naming: every instance owner therefore
      stays a direct binding, so `CountInstanceOwners` and the never-lock-out
      guard needed **no change at all**, and an IdP outage can never strand the
      instance without an owner. Group-derived roles *do* count toward
      `BoundRole`, since a group binding is durable — M-1 is untouched because
      break-glass still arrives via `GrantStore`, never a binding source.

      **Two authorities, deliberately separate.** The catalog (which groups
      exist, who is in a local one, the claim mapping) is instance-scoped
      `group:manage`; *binding* a group at a scope is `member:manage` at that
      scope under the identical `BoundRole` delegation cap `memberPut` applies
      — without it, groups would be a way around M-1. So a project admin can
      grant a group access to their project but cannot put themselves into a
      group bound elsewhere, and therefore cannot reach another project.

      **The claim resolver distinguishes "no groups" from "cannot tell".** An
      absent claim clears the snapshot (fails closed — access is lost, never
      gained). But Entra replaces `groups` with a `_claim_names` Graph pointer
      once a user exceeds ~200 groups, and reading *that* as empty would clear
      every membership and look exactly like a legitimate removal from all of
      them — so it is treated as **unknown**, the snapshot is retained, and a
      `group.sync` event records `status=overage`. Non-string elements, an
      ambiguous dotted path (a literal `a.b` claim *and* a nested `{"a":{"b"}}`,
      the same fail-closed rule CI federation uses), and >512 values all reject
      rather than partially parse. A delimited string is never split. Sync
      failure fails the login: completing one against a snapshot we just failed
      to update is precisely the silent-stale case groups exist to remove.

      Ships `/groups` (catalog + local membership + where a group reaches), a
      Groups section on Members, `janus group` (10 subcommands, owner refused
      locally), 16 OpenAPI paths, and a [guide](docs/guides/groups.md). The e2e
      suite was verified to fail against an engine without `WithGroups`.
- [x] ~~**(2) Delegated project creation — today self-service forces org-wide
      visibility.**~~ **SHIPPED 2026-07-27** — migration `000047`
      (`groups.can_create_projects`). Both options in the original note turned
      out to be the same one: a group may be marked as able to create projects,
      and a member creating one binds it to the **group at admin** (the team can
      work immediately) and the **creator at owner** (so it always has someone
      who can administer and delete it — a group binding can never be owner).
      No instance-wide read is granted at any point, so "teams self-serve" and
      "teams cannot see each other" stop being mutually exclusive.

      It is a narrow **capability**, deliberately not a new rung on the role
      ladder: roles are cumulative bundles, so *any* role granting
      `project:create` at instance scope also grants `project:read` there —
      precisely the leak being closed. It is therefore checked alongside the
      engine rather than inside it, which is defensible because creation is the
      one operation with no existing resource to authorize against (which is
      why it sat at instance scope to begin with). `internal/authz` stays a pure
      decision function over roles.

      Naming a group you are not a member of returns the same `403` as having no
      capability, so it is not a probe for which groups exist. Members of
      several creating groups must name the owner rather than have Janus guess.
      `janus group delegate-creation`, a **delegate…** control on Groups, and an
      **Owning team** picker on project creation.
- [x] ~~**(3) Audit read is instance-wide or nothing.**~~ **SHIPPED
      2026-07-27** — migration `000048`. `audit:read` is now honoured at
      **project** scope, so a team lead reviews their own trail instead of being
      handed every event in the organisation — which mattered because audit rows
      carry resource paths and key names, so the all-or-nothing read leaked the
      shape of every other team's secrets. **This was the last place the
      isolation story leaked.**

      Filtering could not be derived from the resource string: it is free-form
      (`configs/<cid>/secrets`, `project/<pid>/members/<uid>`, `groups/<gid>`,
      `auth/oidc`, …) and a prefix/`LIKE` scheme would silently mis-scope — for
      an audit view the worst failure, because it would *look* complete. So the
      scope is recorded on the event at write time.

      **The column is deliberately outside the chain hash.** `computeHash`
      covers a fixed field list under the `janus:audit:v1` tag; a hashed field
      would invalidate every existing event and break `audit/verify` on upgrade.
      The consequence is stated rather than hidden: `project_id` is an **index,
      not evidence** — direct database access could re-point an event to hide it
      from a *scoped* view, though the instance-wide view stays complete and the
      chain still detects tampering with the event itself. Recorded in
      `docs/threat-model.md` as an explicit non-defense.

      Scope is captured at **authorization** time (`s.can` notes the resource's
      project) rather than threaded through 144 `record()` call sites — an
      event's scope *is* the scope its operation was authorized against. A
      request that authorizes against two different projects is marked ambiguous
      and recorded with **no** scope, so a cross-project action is never
      mis-attributed to one side and hidden from the other.

      `NULL` = instance-only, covering instance-level actions and everything
      written before the upgrade, so a team's scoped history starts there rather
      than being backfilled with guesses. `verify`/`checkpoint`/`prune` stay
      instance-only, since a subset of a hash chain cannot be verified. An
      environment-scoped binding confers nothing (project-level only), and such
      a caller is denied outright rather than shown a partial trail. The filter
      is applied **in the query**; a pagination test walks every page at
      `limit=3` over interleaved two-project writes and asserts the exact count,
      because a post-filter would silently truncate. `GET /v1/audit/events`
      returns `scoped`/`scope_projects` and the viewer shows a **Scoped view**
      stamp, so a partial trail is never presented as the whole ledger.

- [x] ~~**Offboarding has no single answer to "what can this person reach?"**~~
      **SHIPPED 2026-07-29 (PR #225)** — and it closed the users × scopes grid
      and the *"who can write prod?"* view in the same stroke, since all three
      were the same missing thing: a cross-scope answer. `GET /v1/access/matrix`
      (people × scopes, each cell the effective role **and every binding that
      produced it**), `GET /v1/access/users/{uid}` (one person's bindings,
      direct vs group, their reach, live break-glass grants), and
      `POST /v1/access/users/{uid}/revoke-all`, behind a new `/access` screen.

      **One rule, not two.** Cells are computed in memory by
      `authz.ApplicableBindings` / `RoleFromBindings`, which share
      `bindingApplies` with the decision path — `BoundRole` now delegates to
      `RoleFromBindings`. A second implementation of "which bindings apply"
      would have drifted into a review that disagrees with enforcement, which is
      the one thing an access review must never do.

      **Five queries regardless of instance size**, copying `audit_authz.go`'s
      shape: one `AllowedProjects` load decides what is reviewable, then
      `ListForProjects` / `ListForScopes` / `DerivedForScopes` fetch in bulk.
      The delegation cap had the same fan-out problem, so `authz.BoundRoles`
      answers many resources on one load.

      **Revoke-all is deliberately narrow and says so.** It removes *direct*
      bindings at scopes where the caller holds `member:manage` **and** whose
      role is ≤ the caller's `BoundRole` there (the M-1 cap — which makes it
      stricter than `memberDelete`, whose lack of a cap is now the odd one out).
      It does **not** remove group-derived access, revoke active break-glass, or
      disable the account; each is reported structurally and `complete` is false
      while any remain. It refuses the **whole** request with a 409 rather than
      removing the last instance owner. Each revocation writes an ordinary
      `member.revoke` event at its exact scope plus one `member.revoke_all`
      summary, fail-closed via `s.record` (`s.authorize` records denials only).

      **Deny-by-default holds:** the scope set *is* the authorization boundary
      and the SQL filter is built from it, so a caller who cannot read
      instance-scoped bindings does not see them at all — `instance_visible:
      false` says so, and `?project=` refuses an unreviewable id identically to
      a nonexistent one.

      **Not verified:** the screen has never been rendered in a browser in
      either theme (svelte-check only), so column widths under many scopes are
      unproven, and it uses a component-local `--ochre` override because there
      is no `.stamp.warn` primitive. The truncation paths (200 projects / 1000
      envs / 5000 bindings / 500 users / 20000 cells) have never been watched to
      fire.
- [x] ~~**Permissions are not exposed to the UI, so nothing can be gated.**~~
      **FIXED 2026-07-28** — `GET /v1/auth/me` now carries `permissions`, and
      the shell renders from it. It stays a **hint**: the server authorizes
      every request independently, so a hidden screen is still reachable by URL
      and behaves exactly as before.

      The load-bearing decision is the **instance/anywhere split**
      (`authz.Effective`). A project viewer holds `transit:read` *inside their
      project*, but the transit endpoints authorize at instance scope — so a
      single flat "permissions" list would have shown Transit and 403'd, which
      is the bug rather than the fix. `instance` gates instance-scoped screens
      (Transit, Groups, Notifications); `anywhere` gates project-shaped ones
      (Projects, Audit, Members, Approvals, Trash).

      Two collapses avoided: `Effective` resolves bindings **once** and reuses
      the same predicates `Can` does (`userAllows`/`tokenAllows`/`grantsAllow`,
      the last extracted so both callers share one copy) — a second rule set
      would drift into nav items that 403. And the rail, the command palette and
      the `g`-chords now render from **one** table (`web/src/lib/nav.ts`); they
      were three lists, and gating only the rail would have left every hidden
      screen a `ctrl`+`K` away. `nav.ts` is deliberately store-free so the gates
      are unit-testable without a Svelte runtime. An older server that sends no
      `permissions` shows everything — degrading to show-nothing would strand a
      signed-in owner on an empty rail. Active break-glass grants are included.
- [x] ~~**A binding cannot narrow another one.**~~ **HALF-FIXED 2026-07-27 —
      the safety net is now real.** Permissions are the union of all applicable
      bindings and there are no deny rules, so *"developer on the project, but
      read-only on prod"* stays unexpressible, and that remains deliberate:
      allow-union plus deny is where RBAC stops being reasonable about.

      The recorded answer was "default prod configs to `require_approval` so
      such a write becomes a four-eyes request rather than a commit (the
      machinery exists and is E2E-tested)". **That default did not exist.**
      `require_approval` was per config, `DEFAULT false`, and `ConfigRepo.Create`
      never set it — so every config created in a production environment started
      unprotected, and the justification for having no deny rules rested on a
      control nobody had switched on. Groups made it sharper: one project-scope
      binding now grants production write to a whole team at once rather than to
      one person at a time.

      Fixed by making protection a property of the **environment** (migration
      `000046`), with the effective value as the union
      `config.require_approval OR environment.require_approval` — the same shape
      as role bindings, so the engine keeps one rule. A config may add
      protection its environment does not require but can never remove what the
      environment does, so production four-eyes survives a new config and cannot
      be switched off one config at a time. All five write paths (batch, per-key
      set, delete, rollback, promote-apply) resolve it through one shared check
      that fails **closed**. `janus env protect <slug>`, a Protect control per
      environment column on the project board, and an editor banner that says
      when protection is inherited rather than the config's own.

      **The cross-scope half shipped 2026-07-29 (PR #225):** `/access` shows
      people × scopes with every binding that produced each cell, so the union
      is visible rather than implicit. That is what makes "no deny rules" an
      inspectable choice instead of an act of faith.
- [ ] **`secret:read` is all-or-nothing.** A viewer at project scope reads every
      value in every environment, prod included; there is no granularity below a
      config. Reveals are audited per key and unused keys are flagged, so the
      posture is **detection, not prevention** — defensible for a single-tenant
      self-hosted tool, but it should be a decision rather than an accident.

**Open — raised by building groups (2026-07-27):**

_These came out of the groups build itself, not from a roadmap. Each is a real
gap; none is a security hole._

- [x] ~~**Members reported group-derived access as no access.**~~ **FIXED
      2026-07-27** — and the item as originally written was wrong on a fact
      worth recording: it claimed "the Members matrix still plots users ×
      scopes". **There is no RBAC matrix.** The users × scopes grid
      (`useRbacMatrix`, PR #92) was React/Nocturne-era and was removed by
      `18f122c`, the Atrium rewrite; the summary at the top of this file still
      advertised it, which is exactly the tracker drift that comes of checking
      PR titles instead of code.

      The real defect was narrower and worse than "incomplete": `Members.svelte`
      built its role column from `listScopedMembers`, i.e. direct
      `role_bindings` only, so a user holding developer at that scope **through
      a group** rendered as *"no {scope} binding"* — wrong information on the
      screen whose entire job is to say who has access.

      Fixed by resolving it **server-side**, which is not an optimisation but a
      requirement: listing a group's members is instance `group:manage`, while
      reading a scope's members is `member:read` there, so a project admin —
      the person most likely to ask "who can act on my project?" — was the one
      person who could not work it out client-side. `GET
      /v1/{scope}/group-members` now also returns `derived_members` (one
      `{user_id, role, via_group_id, via_group_name}` per granting pair) and a
      `derived_truncated` flag, reported rather than silent so a partial answer
      never reads as complete. Members shows the **effective** role (the union)
      plus a **Source** column — `direct`, `via <group>`, or both — and the
      dropdown/Remove still act on the direct binding only, with derived rows
      linking to `/groups` where the grant actually lives.

      **The grid was rebuilt 2026-07-29 (PR #225)** — not restored: the React
      one was deleted by the Atrium rewrite. It is server-side this time, which
      avoids the 403-tolerant client fan-out the React version needed, and it
      answers "who can write prod?" across every scope at once.
- [ ] **An OIDC group's member list only covers users who have signed in.**
      Membership is a login-time snapshot, so a person who has been in the IdP
      group for months but has never logged into Janus is invisible in
      `/groups`. Nothing is mis-granted — they get the access on first sign-in —
      but "who is in this group?" reads as a complete answer when it is a
      partial one. The UI says so in prose; it should say so structurally
      (e.g. mark the count as "seen at sign-in"), and the offboarding view
      below has the same caveat.
- [x] ~~**Entra group overage leaves membership stale without bound.**~~
      **FIXED 2026-07-28** — migration `000050` + `JANUS_OIDC_GROUP_MAX_AGE`.
      Of the two honest fixes, chose the **configurable maximum snapshot age**
      over a Graph fetch: Graph would need new credentials, add an outbound call
      to the login path, and be Entra-specific in a deliberately generic OIDC
      implementation. Past the configured age, `oidc`-derived bindings stop
      applying until the user's next **authoritative** sync — and an overage
      login deliberately does NOT refresh the clock, or the bound would never
      fire for the exact case it exists for.

      **Local membership never expires.** It is admin-managed, has no freshness
      concept, and expiring it would break every instance with no IdP at all —
      the edge case that would have made a naive implementation worse than the
      problem.

      Off by default, because enabling it silently on upgrade would revoke
      group-derived access from anyone who had not logged in recently; a
      `janus doctor` check warns when group sync is configured without it, so
      it is discoverable rather than buried in a guide. A missing sync timestamp
      is treated as stale (fail closed), and the migration backfills existing
      members so nothing is revoked retroactively. Verified by disabling the SQL
      clause and watching both tests go red.
- [x] ~~**The Groups nav item is visible to accounts that cannot use it.**~~
      **FIXED 2026-07-28** by the `/v1/auth/me` permissions item above, which
      was its root cause. Groups is gated on **instance** `group:manage`, so a
      project admin — whose role bundle contains `group:manage` but only within
      their project — correctly does not see it.
- [x] ~~**No group support in the Terraform provider or the SDKs.**~~
      **SHIPPED 2026-07-29 (PR #223)** — `janus_group`, `janus_group_member` and
      `janus_group_binding`, plus catalog operations (list/get/create/delete,
      local membership, the project-creation capability, `myGroups`) in all
      three SDKs. No new dependency in any module.

      **Bindings are deliberately Terraform-only, not in the SDKs.** Three
      reasons, in order of weight: the SDKs document a config- or
      environment-scoped read token, which can never hold `member:manage`, so
      every binding call from a normally-configured client would 403; a binding
      needs the scope triple and the `BoundRole` delegation cap modelled to be
      usable, which in an SDK degrades to three mutually exclusive optional args
      and an undiagnosable 403; and a binding is *durable access*, the class of
      change that should be planned and diffed. The cost is stated in each guide
      (a JML script that also binds must shell out to `janus group bind`), and a
      test in the TS and Python suites asserts no binding-shaped method exists —
      so reversing this is a decision, not a drive-by.

      **Both invariants land at plan time.** `owner` gets its *own* diagnostic
      (master-key rotation / audit prune / never-lock-out) rather than a generic
      "invalid value", because owner is a valid role — just never for a group;
      a test asserts **zero** API calls on that path. The two-kinds rule cannot
      come from configuration alone (kind is not an attribute of
      `janus_group_member`), so `ModifyPlan` looks it up whenever `group_id` is
      concrete, and a `Create` pre-flight catches the same-apply case with no
      write. A lookup failure is deliberately silent — `plan` must not break on
      an unreachable server.

      Note `janus_group` exposes **no member count and no member data source**,
      and the SDK field is named `members_seen`: an `oidc` group's list only
      covers users seen at sign-in, so a count would read as membership and is
      not. **Not verified:** nothing was applied against a live Janus, and
      `terraform plan`/`apply` were never run (plan-time logic is driven through
      the validator entry points directly).

**Found by actually deploying the Helm chart (2026-07-28):**

The chart shipped in PR #165 and had **never been deployed**, nor covered by any
CI. Deploying it to a local minikube cluster surfaced three defects, all now
fixed:

- [x] ~~The API Service also selected the bundled Postgres pod.~~ It matched on
      `name`+`instance` only, which the evaluation Postgres also carries — so
      `kubectl port-forward svc/janus`, the command `NOTES.txt` gives for the
      **unseal step**, picked the database and failed. API traffic was never
      mis-routed (Postgres joined as a port-less endpoint, having no `http`
      port) but sat one label from receiving it. Fixed by pinning
      `component: server` on the Service selector and the pod template, leaving
      the Deployment's immutable `.spec.selector` alone so `helm upgrade` still
      works — verified against the live cluster.
- [x] ~~An invalid or incomplete seal config rendered silently.~~ `seal.type`
      is now validated, and a cloud-KMS type without its key fails at template
      time. The chart's own defaults (`awskms` + empty `keyArn`) previously
      rendered a pod that could never unseal.
- [x] ~~The chart had no CI at all.~~ Added a job that lints, renders every seal
      mode, asserts invalid configs fail, and asserts the API Service cannot
      match the Postgres pod.

Verified end-to-end on minikube: 3-of-5 Shamir init → `/v1/sys/live` 200 while
`/v1/sys/ready` 503 (sealed, exactly as documented) → quorum unseal → Ready 1/1,
schema at the expected migration. Two things worth knowing that are **not**
defects: `helm install --wait` cannot succeed with Shamir (the pod is
intentionally NotReady until unsealed), and a fresh install crash-loops a few
times before Postgres accepts connections, since the chart has no wait-for-DB
init container.

**Found by exercising the Kubernetes integrations on a real cluster (2026-07-28):**

Both k8s-native integrations were tested end-to-end against minikube for the
first time. Unit tests covered them (httptest fakes, synthetic tokens); nothing
had ever run against a live API server.

- [x] ~~**The chart's SSRF hardening silently breaks both in-cluster
      integrations.**~~ **DOCUMENTED 2026-07-28** — the chart sets
      `JANUS_OUTBOUND_BLOCK_PRIVATE=true`, which also blocks
      `kubernetes.default.svc` (a ClusterIP in a private range). The **k8s sync
      provider** then fails with a sanitized `apply failed` that does not say
      why, and **SA federation** fails with a generic 401 because the JWKS fetch
      never leaves the pod. Diagnosed by elimination: the provider's exact SSA
      request, RBAC, and TLS-against-the-cluster-CA all succeed from a pod with
      the same credentials. With `env.outboundBlockPrivate=false` the sync
      **works end-to-end** — a real `Secret` appeared with the values
      round-tripped from Janus. Now called out in `values.yaml` and the
      Kubernetes guide.
- [x] ~~**A blunt on/off SSRF toggle is the wrong shape for k8s.**~~ **SHIPPED
      2026-07-29** — `JANUS_OUTBOUND_ALLOW` (chart: `env.outboundAllow`), a
      comma-separated list of IPs/CIDRs exempt from
      `JANUS_OUTBOUND_BLOCK_PRIVATE`. The operator keeps the control on and
      names the API server's ClusterIP, instead of choosing between SSRF
      hardening and the cluster-native integrations. No migration, no API
      surface — it is process configuration.

      **The allowlist exempts the private-space tightening and nothing else.**
      Enforcement consults it strictly *below* the unconditional link-local /
      cloud-metadata check in `checkIP`, so no entry can reach
      `169.254.169.254`; parsing additionally rejects any entry lying entirely
      inside a blocked range, so such an entry fails at boot rather than sitting
      in the config looking effective. `TestAllowlistCannotUnblockAlwaysBlocked`
      pins this by driving `checkIP` directly with those addresses allowlisted
      *and* `0.0.0.0/0` + `::/0` — proving enforcement does not rely on parsing
      having caught them. It was verified non-vacuous: moving the allowlist
      check above the unconditional block turns all 8 ranges red.

      **Entries are addresses, never hostnames.** The guard's whole value is
      that it validates the *resolved* IP at connect time, which is what defeats
      DNS rebinding; allowlisting a name would mean trusting DNS for that name
      and would reopen precisely that attack. The IPv4-in-IPv6 spelling is
      rejected on input (a prefix's bit count is ambiguous under it) while
      enforcement unmaps before matching, so a dial that resolves to the mapped
      form still matches a plain IPv4 entry.

      **A malformed value refuses to start**, naming the entry, and one bad
      entry discards the whole list rather than leaving a partially-applied one.
      Both directions fail closed, but this one is predictable — and since the
      allowlist fails closed, tolerating a typo would present as an integration
      that mysteriously cannot connect, which is the failure mode the variable
      exists to remove. An allowlist set *without* the tightening is inert;
      boot and `janus doctor` both warn rather than pretending it does
      something. CI (and `make helm-test`) assert the chart omits the variable
      when empty and renders it when set.
- [x] ~~**The outbound policy could only be changed by restarting the server.**~~
      **SHIPPED 2026-07-29** — Settings → **Outbound policy** (owner only) plus
      `GET`/`PUT`/`DELETE /v1/sys/outbound-policy`, migration `000051`. A stored
      policy supersedes the environment and survives restarts; both the screen
      and the API report **which source is in force**, so a manifest that
      disagrees with the instance is visible rather than silent.

      **The plumbing was the work, not the screen.** Each engine built one
      `http.Client` at construction whose dialer closed over a policy *value*,
      so a runtime change could never reach it. Policy now resolves through a
      single process-wide `nethard.Source` read on every dial —
      deliberately **one** source, because a per-engine copy would leave
      rotation obeying the new policy while sync still obeyed the old, a
      split-brain invisible until something failed to connect.
      `TestSourceIsLive` drives a dialer built once and asserts both tightening
      and loosening land on the next dial.

      **It is a deliberate, bounded weakening** — recorded in
      `docs/threat-model.md` rather than glossed. The guard bounds what a
      mis-configured integration can make the server dial, and integration
      config is already admin-gated, so an in-app policy sits under an authority
      it partly constrains. Four bounds: the metadata/link-local ranges are
      unexemptable **in enforcement** (checkIP consults the allowlist below
      them), so no stored value can reach them; the capability is **owner-only**
      (`sys:egress`), beside master-key rotation and audit prune, explicitly not
      admin, since admin is the tier that configures the integrations this
      guards; `allow_proxy` is **not storable** and is rejected with a 400
      rather than silently dropped, because it is the one setting that blinds
      the guard outright; and `JANUS_OUTBOUND_POLICY_LOCKED=true` pins the
      policy to the environment (writes 409) for deployments that need the
      control strictly outside the app. Worth naming: the DEFAULT policy already
      permits private space, so this only widens instances that had hardened
      past the default.

      Boot failure is fatal, not a warning: starting on the environment's policy
      while an override exists would be a **silent** egress change, the one
      outcome this control must never produce.
- [x] ~~**Federation cannot verify an issuer whose cert is signed by the cluster
      CA.**~~ **SHIPPED 2026-07-29 (PR #224, migration `000052`)** — trusted
      issuers carry an optional per-issuer `ca_cert` (PEM) used for that issuer's
      discovery + JWKS TLS. The asymmetry that motivated it is now closed: sync
      accepted a `ca_cert`, federation did not.

      **A bundle REPLACES the system roots for that issuer**, rather than adding
      to them. It matches what the sync provider already does; it is the stricter
      reading, since an issuer is one host with one legitimate signer, so a
      mis-issuance by any public root cannot impersonate it; and additive trust
      would be strictly weaker in exactly the case the feature exists for
      (private cluster CA *plus* every public CA) for no benefit. An operator who
      wants public roots leaves it empty. There is no `InsecureSkipVerify` path
      at any layer, and a test asserts it is never set.

      **The verifier cache was the real hazard.** A cached `fedVerifier` owns the
      HTTP client its JWKS `RemoteKeySet` was built with, so a corrected bundle
      would have been masked until restart. The cache-hit check now compares
      issuer + audience + CA, on top of the existing `invalidateFederationVerifier()`
      on every mutation path — the comparison is the second line of defence for a
      row changed by a restore, a second instance, or a hand-edit.

      The shared `s.oidcHTTP` client is left untouched (it serves every other
      issuer and human OIDC login); a bundle produces a *per-issuer* client built
      through `nethard.SafeHTTPClient` with the live process policy, so the SSRF
      guard is fully intact. A custom CA changes which certificate is acceptable,
      never which address may be dialled — operators with
      `JANUS_OUTBOUND_BLOCK_PRIVATE=true` still need the ClusterIP allowlisted.

      **Not verified: never exercised against a real cluster.** The end-to-end
      proof is an `httptest.NewTLSServer` mock IdP trusted only via the supplied
      bundle (fails with system roots, fails with a valid-but-wrong CA, succeeds
      with its own). Same trust relationship, but it is not minikube — and a k8s
      feature looking right and then failing on contact with a real cluster is
      exactly how this item was found in the first place. The migration has also
      only been applied by the test harness, and the down migration never run.

**Verified working end-to-end on the cluster:** the k8s sync provider (real
`Secret` created via server-side apply, values correct, run history recorded),
and all Kubernetes Go tests (SA-token federation claim matching including the
7 required-claim subtests; sync provider SSA/prune/CA-rejection/non-2xx).

**Open — found by the new E2E coverage, not yet fixed:**

- [x] ~~**The secret editor loses the protected (four-eyes) flag on a deep
      link.**~~ **FIXED 2026-07-27 (PR #202)** — the flag was read from the
      registry cache without waiting for hydration, and a deep link is a cold
      load, so it defaulted to "unprotected": no banner, no review panel, and a
      Save button reading "Save as vN" on a config where saving files an
      approval request. The server was never wrong, so nothing was exploitable —
      the UI misreported a security control. The client had no way to read a
      config authoritatively at all (only `deleteConfig`), so `api.getConfig`
      was added and the editor now reads the flag from the server, falling back
      to the cache only when that read fails. Pinned by an E2E test that
      deep-links with a cold browser context and asserts the banner, the toggle
      and the Save button's own text; verified to fail against the cache read
      while the dossier path still passes.
- [x] ~~**The login screen throttles password logins.**~~ **ALREADY FIXED —
      confirmed against the code 2026-07-28.** `/v1/auth/oidc/status` and
      `/webauthn/status` now sit on a separate `probeLimiter` (120/min, burst
      40), not `loginLimiter`; `internal/api/server.go:379-395` narrates the old
      behaviour in the past tense. Capability probes are not credential
      attempts, and rate-limiting a page view as if it were a password guess
      punished the honest user while doing nothing to an attacker, who skips the
      page and posts straight to `/login`.
- [x] ~~**Deleting a config redirects to `/projects`, not the dossier.**~~
      **ALREADY FIXED — confirmed 2026-07-28.** `SecretEditor.svelte:652`
      captures `parentProjectId` **before** the delete, precisely because `ctx`
      is `$derived` from the registry and the re-hydration drops the config.
- [x] ~~**Duplicate/wrong `aria-label`** on the protected-config edits table.~~
      **ALREADY FIXED — confirmed 2026-07-28.** `Approvals.svelte:257` reads
      `"Protected-config edit requests"`; the promotion table above it keeps
      `"Pending promotion approvals"`.

Also noted, not yet fixed: `docker-compose.yml` points its passkey-origin
comment at the **TOTP** guide rather than [passkeys.md](docs/guides/passkeys.md),
and environment-variable documentation is split between
[production-deployment.md](docs/guides/production-deployment.md) and
[operations.md](docs/operations.md) — nothing is undocumented, but an operator
reading only the deployment guide will not find `JANUS_SYNC_VERIFY_TICK`,
`JANUS_NOTIFY_TICK`, `JANUS_BREAKGLASS_MAX_TTL` or the retention floors.

### What's actually left

**Both roadmaps are now exhausted.** Sections 1–5 (the original) finished before
v0.1.0; sections 6–9 (post-1.0, added 2026-07-24) closed out over 2026-07-25/26.
The "Trust & Longevity" batch — trust & supply-chain sweep, `run --watch` +
`render` (#152), audit checkpointing + retention (#153), Playwright smoke (#151)
— all shipped, as did everything that followed it: k8s SA federation (#174),
sync drift detection (#175), passkeys (#176) + discoverable login (#184),
value-version retention (#181), Grafana (#180), fuzzing, SDK depth (#183), the
Terraform batch resource + env-scoped tokens (#186), and the web import wizard
(#187).

The mobile/tablet layout (5.6) shipped 2026-07-26 (PR #192), which was the last
open **build** item. **Every remaining item is blocked on a maintainer
credential, not on code:**

| # | Item | Blocked on |
|---|---|---|
| 1 | Publish the TypeScript SDK to npm | npm account + automation token |
| 2 | Publish the Python SDK to PyPI | PyPI project + Trusted Publishing |
| 3 | Publish the Terraform provider to the Registry | GPG signing key + Registry account |

Each is a short CI job once the credential exists; everything they would publish
is written, tested and — as of 2026-07-26 — covered by CI.

Nothing on the *roadmap* is outstanding. Open engineering work now lives in the
section above, sourced from real usage rather than invented to fill a list —
which is how it should stay.

**The RBAC-at-organisation-scale and Kubernetes batches closed out 2026-07-29**
(PRs #223 · #224 · #225, migration `000052`): group resources in the Terraform
provider and the SDKs, per-issuer federation `ca_cert`, and the cross-scope
access review — which closed the offboarding view, the users × scopes grid and
the *"who can write prod?"* question together, since all three were the same
missing cross-scope answer. **Exactly two engineering items remain**, and both
are deliberate rather than pending:

- **`secret:read` is all-or-nothing** — a recorded decision (detection over
  prevention, defensible for a single-tenant self-hosted tool), not a gap
  awaiting work.
- **An OIDC group's member list covers only users who have signed in** — the
  one genuine open item. #223 was careful not to make it worse (no member count,
  no member data source, the SDK field is named `members_seen`), but the Groups
  screen still states it in prose where it should state it structurally.

Two things shipped this batch are **unverified against reality** and should be
treated as such until someone runs them: the federation `ca_cert` has never been
exercised against a real cluster (only an `httptest` TLS server with the same
trust relationship), and the `/access` screen has never been rendered in a
browser in either theme. A Kubernetes feature that looked right and then failed
on contact with a real cluster is precisely how the `ca_cert` item was found.

Unrelated to the roadmap, one operational chore is also waiting on the
maintainer: `gh auth refresh -h github.com -s write:packages`, so
`ghcr.io/steveokay/janus:latest` can be repointed off the `v0.1.1-rc1` digest
and the rc artifacts deleted.

Both parked decisions are **resolved** (OIDC/TOTP scope, engine audit
fail-closed policy), and the small backend/ops list at the top is fully struck
through.
