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
- [ ] **DB pool tuning** — `pgx` runs on defaults (no max-conns/lifetime
      config); shutdown grace is fixed at 10s, not configurable.
- [x] ~~**Prometheus `/metrics`** (request rates/latency, seal state, lease
      counts, rotation/sync failure gauges, audit head seq) + a
      `JANUS_LOG_LEVEL`/format env var.~~ **SHIPPED 2026-07-22** — hand-rolled
      zero-dep exposition, token-gated (`JANUS_METRICS_TOKEN`, off by default),
      HTTP metrics keyed by chi route pattern (bounded cardinality) + engine/DB/
      audit/runtime gauges. Plus an admin **health panel** (Settings, backed by
      `GET /v1/sys/status`) and `JANUS_LOG_LEVEL`/`JANUS_LOG_FORMAT`. Adversarial
      review SHIP. See [observability guide](docs/guides/observability.md).
- [ ] **Token `last_used` / user `last_login` not tracked** (only OIDC
      identities carry a `last_login_at`) — blocks a stale-token warning on
      Tokens and a "last login" column on Members.
- [ ] **docker-compose has no resource limits**, and no WAL-archiving/
      pg-backup guidance beyond the app-level `janus backup` logical dump.
- [ ] **No `CONTRIBUTING.md`.**
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

**This section is the canonical tracker and mirrors the five sections of
[`docs/roadmap.md`](docs/roadmap.md) one-to-one — every roadmap item appears
here (shipped ones struck through with a date). When you add, ship, or reword a
roadmap item, update BOTH files so they never drift.** Effort: **S** ≈ a
session, **M** ≈ a day or two, **L** ≈ a week-plus.

### Security hardening

| Feature | Why | Effort |
|---|---|---|
| ~~Native TLS listener (`JANUS_TLS_CERT/KEY`, optional ACME)~~ **SHIPPED 2026-07-23** — native HTTPS: static certs (`JANUS_TLS_CERT`/`JANUS_TLS_KEY`) or ACME/Let's Encrypt (`JANUS_TLS_ACME_DOMAINS`/`_EMAIL`/`_CACHE`, via `x/crypto/acme/autocert`), mutually exclusive + startup-validated, TLS 1.2 floor, optional `JANUS_TLS_REDIRECT_HTTP` HTTP→HTTPS 301; aux listeners drain on shutdown. No migration. See [production-deployment guide](docs/guides/production-deployment.md). | ~~M~~ |
| ~~TOTP second factor for password logins (+ recovery codes)~~ **SHIPPED 2026-07-21** — RFC 6238 TOTP + single-use recovery codes; self-service enroll/confirm/disable/regenerate (`/v1/auth/totp/*`), login gains `totp_code` (`401 totp_required` without it), `janus login` prompts + retries, Settings enroll UI. Secret master-key-wrapped (re-wrapped by master-key rotation), recovery codes HMAC-hashed + single-use, value-free audit. Migration 000025. **Note:** OIDC login is not gated by app-level TOTP (the IdP owns MFA) — see follow-ups. Passkeys/WebAuthn + per-account lockout as follow-ups. | ~~M~~ |
| ~~Session management — list active sessions, revoke one/all~~ **SHIPPED 2026-07-20** — `GET/DELETE /v1/auth/sessions` (self-service, IP/user-agent metadata, current-session marker), Settings → Active sessions UI, `janus session list/revoke`. Sessions now record client IP + user-agent (migration 000023). | An admin who suspects a stolen cookie has nothing to pull today. | ~~S~~ |
| ~~Account lockout / progressive backoff~~ **SHIPPED 2026-07-22** — see the "Open — backend / ops" entry above (migration 000026, `JANUS_LOCKOUT_*`, admin unlock). | Nothing locked an account out after repeated failed logins. | ~~S~~ |
| ~~Secret expiry / max-age policy per key or config, surfaced in-app~~ **SHIPPED 2026-07-23** — **advisory** max-age (never blocks reads/writes): config-level default + per-key override, effective policy = per-key else config-default else none, `stale` computed from the current value version's age. `config_secret_max_age` table (migration 000028, config-default under the `''` sentinel key), `GET/PUT /v1/configs/{cid}/max-age` + `PUT .../secrets/{key}/max-age`, `secret:write` to set / `secret:read` to list, value-free audit; masked-list gains `stale`+`max_age_seconds`; editor stale chip + set/clear controls + Overview in-tray count; `janus secrets max-age` CLI. | ~~M~~ |
| Break-glass access — time-boxed role elevation with a mandatory reason, stamped into the audit chain | Incidents need a paved road that is loud, not shared root credentials. | M |
| Per-token IP allowlists + usage anomaly notes (new IP) | Cheap, high-signal containment for exfiltrated tokens; IPs are already in every audit event. | M |
| GCP KMS / Azure Key Vault auto-unseal | The `Unsealer` interface already exists; AWS-only is an adoption blocker off-AWS. | M |

### Secret lifecycle & editor

| Feature | Why | Effort |
|---|---|---|
| ~~Dotenv / properties import in the editor~~ **SHIPPED 2026-07-19** — Import… paste or pick a `.env`/`.properties` file, preview per-key (new/overwrite/invalid), stage into the dirty buffer, commit as one version. | The first thing a migrating user does is re-key an existing `.env` by hand. | ~~S~~ |
| ~~Value generator in the editor (random password / hex / base64, length picker)~~ **SHIPPED 2026-07-22** — client-side CSPRNG (unbiased rejection sampling), "Gen" popover on the editable value cell: password (symbols / exclude-ambiguous toggles) / hex / base64 + length; value flows through the normal dirty-buffer save, no endpoint/migration. | ~~S~~ |
| Unused-secret detection — "not read in 90 days" chip from audit data | Dead secrets are silent risk; the data already exists in `audit_events`. | M |
| Per-key read insights — last-read + 30-day sparkline in the editor row | Turns "can I delete this?" from a guess into a lookup. | M |
| Cross-environment diff view — pick any two configs, key-level presence/drift (values masked) | Promote covers adjacent stages; "why does staging differ from prod" needs an arbitrary diff. | M |
| Secret annotations — owner + note metadata per key (never values) | "What is this and who do I ask" is unanswerable today. | M |
| Require-approval-for-prod-edits toggle — direct saves to protected configs become a promotion-style request | Extends the existing four-eyes approval machinery to close its biggest bypass (raw prod edits). | M |

### Integrations & delivery

| Feature | Why | Effort |
|---|---|---|
| More sync providers: GitLab CI variables, Cloudflare Workers secrets, Vercel/Netlify env, AWS SSM/Secrets Manager | The sync engine is provider-pluggable; each target is mostly an adapter + creds form. | M each |
| More CI federation issuers: GitLab, Buildkite, CircleCI OIDC | The trust-binding model generalizes; only issuer/claims mapping differs. | S each |
| Inbound one-shot importers: Doppler, Vault KV, AWS SM → project/config tree | Migration friction is the #1 adoption cost. | L |
| ~~Notifications: webhook + Slack for rotation failures, sync errors, denials, pending approvals~~ **SHIPPED 2026-07-21** — audit-tailing dispatcher + delivery outbox; webhook + Slack channels; `notification:manage`, `/v1/notifications/channels`, `janus notifications` CLI, Notifications web screen; migration 000024. **SMTP email channel added 2026-07-23** (`type=smtp`, `net/smtp` STARTTLS/implicit/none, verify-by-default + per-channel `insecure_skip_verify`, write-only password, value-free body; migration 000027). | Failures must find humans, not just an in-app tray. | ~~M~~ |
| Terraform provider (projects, configs, secrets-as-writes, tokens, bindings) | Infra teams won't click UIs; declarative config is table stakes. | L |
| Client SDKs (Go, TypeScript, Python) with in-process caching + lease renewal | `janus run` covers processes; apps wanting native reads shouldn't hand-roll HTTP. | L |
| More rotators: MySQL, Redis ACL, AWS IAM access keys, generic OAuth client-credential refresh | Same crash-safe framework, new drivers. | M each |

### Operations & observability

| Feature | Why | Effort |
|---|---|---|
| ~~Prometheus `/metrics` + health panel~~ **SHIPPED 2026-07-22** — see the "Open — backend / ops" entry above (`JANUS_METRICS_TOKEN`, `GET /v1/sys/status`, Settings → Health). | Self-hosting is a black box until it breaks. | ~~S~~ |
| Scheduled encrypted backups to S3-compatible storage with retention + a restore-rehearsal command | A backup button is not a backup strategy. | M |
| Audit shipping — stream JSONL to webhook/syslog/S3 for SIEM ingestion, with a high-water mark | Compliance teams want the ledger in *their* store; export-on-demand doesn't scale to that. | M |
| ~~Health panel in Settings — DB latency, scheduler tick ages, failed-run counts~~ **SHIPPED 2026-07-22** (with Prometheus metrics — `GET /v1/sys/status` + Settings → Health). | ~~S~~ |
| ~~First-run onboarding checklist (create project → add secrets → mint token → `janus run`)~~ **SHIPPED 2026-07-23** — dashboard checklist on the Overview; steps auto-check from existing state (projects / any secret via 403-tolerant masked-list probe / `listTokens`), step 4 shows a copyable `janus login`→`setup`→`run` block; hides once set up, dismissible (localStorage). Frontend-only, no migration/endpoint. | ~~S~~ |

### UI polish

| Feature | Why | Effort |
|---|---|---|
| ~~Global key search in the command palette (search masked key names across configs)~~ **SHIPPED 2026-07-22** — `GET /v1/search/keys` (names-only, deny-by-default per-config `SecretRead` filter, no audit/no value, bounded) + palette "Secret keys" group with `?key=` editor deep-link. Adversarial review SHIP. | ~~S~~ |
| Bulk row selection in the editor — multi-select → delete / promote / export | One-at-a-time actions don't survive 40-key configs. | M |
| ~~JSON/PEM awareness for file-type secrets — pretty-print, validate, syntax hint~~ **SHIPPED 2026-07-23** — client-side format sniff (content first, declared `type` as fallback) on the value being edited: JSON/PEM badge, well-formedness check (JSON parse error, PEM label/base64 faults) surfaced inline, one-click Pretty-print for valid JSON. Advisory only — never blocks a save; nothing leaves the browser. | ~~S~~ |
| ~~Shortcuts help modal (`?`) + `g`-prefixed nav chords~~ **SHIPPED 2026-07-23** — `?` opens a shortcuts modal (palette action too); `g` + letter jumps to any screen (`g p` Projects, `g a` Audit, …). Chords are suppressed while typing, with modifiers, or while a dialog is open; a pending-chord hint shows after `g`. | ~~S~~ |
| Accessibility pass — focus traps in modals, ARIA on tables, reduced-motion audit | A deliberate pass would close the remaining gaps. | M |
| Mobile/tablet layout for read-mostly screens (dashboard, audit, approvals) | Approving a promotion from a phone is a real workflow. | M |

### Suggested near-term slate

The previous slates (Prometheus + health panel, TOTP, global key search,
account lockout, SMTP notifications, JSON/PEM awareness, shortcuts help +
`g`-chords, **native TLS listener**, **secret max-age / expiry**, **first-run
onboarding checklist**) are **fully shipped** (2026-07-20 → 07-23). Next five,
weighing leverage against effort:

1. **More sync providers** (3.1, e.g. GitLab CI / AWS SSM) — extend the shipped
   provider-pluggable sync engine.
2. **Unused-secret detection** (2.2) — "not read in 90 days" chip from data
   already in `audit_events`; natural companion to the shipped max-age nags.
3. **Cross-environment diff view** (2.5) — arbitrary key-level config drift,
   values masked.
4. **GCP KMS / Azure Key Vault auto-unseal** (1.7) — the `Unsealer` interface
   already exists; AWS-only is an off-AWS adoption blocker.
5. **Token `last_used` / user `last_login` tracking** — unblocks a stale-token
   warning on Tokens + a "last login" column on Members (small backend + UI).

Both parked decisions are now **resolved** (see the backend/ops section). Still
outstanding: the small backend/ops items (DB pool tuning, token/user last-used
tracking, `CONTRIBUTING.md`).
