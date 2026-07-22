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

- [ ] **Account lockout / progressive backoff** beyond the per-IP login rate
      limiter (10/min). Admin disable exists but nothing locks an account out
      automatically after repeated failures.
- [ ] **DB pool tuning** — `pgx` runs on defaults (no max-conns/lifetime
      config); shutdown grace is fixed at 10s, not configurable.
- [ ] **Prometheus `/metrics`** (request rates/latency, seal state, lease
      counts, rotation/sync failure gauges, audit head seq) + a
      `JANUS_LOG_LEVEL`/format env var. "Reads 24h" is the only metric today.
- [ ] **Token `last_used` / user `last_login` not tracked** (only OIDC
      identities carry a `last_login_at`) — blocks a stale-token warning on
      Tokens and a "last login" column on Members.
- [ ] **docker-compose has no resource limits**, and no WAL-archiving/
      pg-backup guidance beyond the app-level `janus backup` logical dump.
- [ ] **No `CONTRIBUTING.md`.**
- [ ] **Decision — OIDC login is not gated by app-level TOTP.** OIDC calls
      `createSession` directly (the IdP is expected to own MFA). If a user
      enables TOTP for password login but the same account is also reachable
      via OIDC, the second factor can be sidestepped through the OIDC path.
      This is likely intended; confirm and either document it or gate OIDC
      too. (Surfaced by the TOTP security review, 2026-07-21.)
- [ ] **Decision needed — audit fail-closed policy for engine-authored action
      endpoints** (rotation `.../rotate`, sync `.../sync`, dynamic
      issue/renew/revoke). Today the *denial* path is fail-closed but the
      *success* audit event is the engine's best-effort write (a failure logs
      a warn; the mutation — already applied externally — still stands).
      Either (a) accept this as the documented trade-off for external side
      effects that can't be undone by a late audit failure, or (b) add a
      second fail-closed `s.record(...)` at the API layer after each such
      endpoint. Whichever is chosen must apply uniformly across all three
      engines, not piecemeal.

## Open — product roadmap

Carried forward from the sibling `janus-atrium` project's status doc (same
lineage, same backend) and from `docs/roadmap.md`. Effort: **S** ≈ a
session, **M** ≈ a day or two, **L** ≈ a week-plus.

### Security hardening

| Feature | Why | Effort |
|---|---|---|
| Native TLS listener (`JANUS_TLS_CERT/KEY`, optional ACME) | TLS is delegated to a reverse proxy today; small shops run without one. | M |
| ~~TOTP second factor for password logins (+ recovery codes)~~ **SHIPPED 2026-07-21** — RFC 6238 TOTP + single-use recovery codes; self-service enroll/confirm/disable/regenerate (`/v1/auth/totp/*`), login gains `totp_code` (`401 totp_required` without it), `janus login` prompts + retries, Settings enroll UI. Secret master-key-wrapped (re-wrapped by master-key rotation), recovery codes HMAC-hashed + single-use, value-free audit. Migration 000025. **Note:** OIDC login is not gated by app-level TOTP (the IdP owns MFA) — see follow-ups. Passkeys/WebAuthn + per-account lockout as follow-ups. | ~~M~~ |
| ~~Session management — list active sessions, revoke one/all~~ **SHIPPED 2026-07-20** — `GET/DELETE /v1/auth/sessions` (self-service, IP/user-agent metadata, current-session marker), Settings → Active sessions UI, `janus session list/revoke`. Sessions now record client IP + user-agent (migration 000023). | An admin who suspects a stolen cookie has nothing to pull today. | ~~S~~ |
| Secret expiry / max-age policy per key or config, surfaced in-app | Rotation exists but nothing nags about stale static secrets — the most common real-world failure. | M |
| Break-glass access — time-boxed role elevation with a mandatory reason, stamped into the audit chain | Incidents need a paved road that is loud, not shared root credentials. | M |
| Per-token IP allowlists + usage anomaly notes (new IP) | Cheap, high-signal containment for exfiltrated tokens; IPs are already in every audit event. | M |
| GCP KMS / Azure Key Vault auto-unseal | The `Unsealer` interface already exists; AWS-only is an adoption blocker off-AWS. | M |

### Secret lifecycle & editor

| Feature | Why | Effort |
|---|---|---|
| Value generator in the editor (random password / hex / base64, length picker) | Stops people inventing weak values. | S |
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
| ~~Notifications: webhook + Slack for rotation failures, sync errors, denials, pending approvals~~ **SHIPPED 2026-07-21** — audit-tailing dispatcher + delivery outbox; webhook + Slack channels; `notification:manage`, `/v1/notifications/channels`, `janus notifications` CLI, Notifications web screen; migration 000024. SMTP still a possible follow-up. | Failures must find humans, not just an in-app tray. | ~~M~~ |
| Terraform provider (projects, configs, secrets-as-writes, tokens, bindings) | Infra teams won't click UIs; declarative config is table stakes. | L |
| Client SDKs (Go, TypeScript, Python) with in-process caching + lease renewal | `janus run` covers processes; apps wanting native reads shouldn't hand-roll HTTP. | L |
| More rotators: MySQL, Redis ACL, AWS IAM access keys, generic OAuth client-credential refresh | Same crash-safe framework, new drivers. | M each |

### Operations & observability

| Feature | Why | Effort |
|---|---|---|
| Scheduled encrypted backups to S3-compatible storage with retention + a restore-rehearsal command | A backup button is not a backup strategy. | M |
| Audit shipping — stream JSONL to webhook/syslog/S3 for SIEM ingestion, with a high-water mark | Compliance teams want the ledger in *their* store; export-on-demand doesn't scale to that. | M |
| Health panel in Settings — DB latency, scheduler tick ages, failed-run counts, chain-verify age | The engines are invisible until they fail. | S |
| First-run onboarding checklist (create project → add secrets → mint token → `janus run`) | The empty state after init is a dead end for newcomers. | S |

### UI polish

| Feature | Why | Effort |
|---|---|---|
| Global key search in the command palette (search masked key names across configs) | "Where is STRIPE_KEY set?" is a daily question; names are metadata, safe to index. | S |
| Bulk row selection in the editor — multi-select → delete / promote / export | One-at-a-time actions don't survive 40-key configs. | M |
| JSON/PEM awareness for file-type secrets — pretty-print, validate, syntax hint | Format validation is the natural next step after multi-line editing. | S |
| Shortcuts help modal (`?`) + `g`-prefixed nav chords | The palette exists; discoverability doesn't. | S |
| Accessibility pass — focus traps in modals, ARIA on tables, reduced-motion audit | A deliberate pass would close the remaining gaps. | M |
| Mobile/tablet layout for read-mostly screens (dashboard, audit, approvals) | Approving a promotion from a phone is a real workflow. | M |

### Suggested near-term slate

If picking the next five, weighing leverage against effort:

1. Prometheus metrics + health panel — makes self-hosting operable.
2. TOTP second factor — the cheapest meaningful hardening now that session
   management has shipped.
3. Global key search — daily-use quality of life.
4. Account lockout / progressive backoff — closes the last auth-hardening gap.
5. SMTP notification channel + more sync providers — extend the shipped
   notifications/sync engines.
