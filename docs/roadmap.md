# Janus — Atrium status & feature roadmap

_2026-07-19. The first half of this file is where the system stands; the second
half is the forward roadmap — features I'd add, ranked by value against effort.
Effort: **S** ≈ a session, **M** ≈ a day or two, **L** ≈ a week-plus._

## Where it stands

Full stack in one repo: the upstream Janus backend (synced through PR #94,
filename-style keys) + the Atrium SPA (Svelte 5, "Security Printing" design,
Daylight/Nightwatch themes) embedded in the single binary. The UI covers the
entire API surface: init/unseal/login ceremony, projects → envs → configs,
secret editor (masked/audited reveal, dirty-buffer saves, multi-line values,
per-key history, locked keys), promotion pipeline + approvals, audit ledger,
tokens, scoped members, transit, operations (rotation/sync/dynamic incl.
create flows and credential issuance), integrations (OIDC + CI federation),
trash, settings (master-key rotate/rekey, backup, passphrase), command
palette, and in-app modal dialogs.

Upstream non-goals stay non-goals here: no HA/Raft, no PKI/CA, no SSH signing,
no HSM, no multi-tenancy, no FIPS claims.

---

## Proposed features

### 1. Security hardening

| Feature | Why | Effort |
|---|---|---|
| ~~**Native TLS listener** (`JANUS_TLS_CERT/KEY`, optional ACME)~~ **SHIPPED 2026-07-23** — static certs or ACME/Let's Encrypt (mutually exclusive, startup-validated), TLS 1.2 floor, optional HTTP→HTTPS redirect; `x/crypto/acme/autocert`, no migration. | ~~M~~ |
| ~~**TOTP second factor for password logins** (+ recovery codes)~~ **SHIPPED 2026-07-21** — RFC 6238 TOTP + single-use recovery codes, self-service enroll/confirm/disable, login `totp_required` gate, QR enrolment. Passkeys/WebAuthn remains a follow-up. | ~~M~~ |
| ~~**Account lockout / progressive backoff**~~ **SHIPPED 2026-07-22** — progressive temporary per-account lockout with admin unlock; reveals only to the correct password (no enumeration); `JANUS_LOCKOUT_*`. | ~~S~~ |
| ~~**Session management** — list active sessions, revoke one/all (upstream gap 1.12)~~ **SHIPPED 2026-07-20** — `GET/DELETE /v1/auth/sessions`, Settings UI, `janus session` CLI. | ~~S~~ |
| ~~**Secret expiry / max-age policy** per key or config, surfaced in the in-tray ("STRIPE_KEY is 180d old")~~ **SHIPPED 2026-07-23** — advisory (blocks nothing): config default + per-key override, `stale` signal from the value's age; migration 000028, `secret:write` to set, editor chip + Overview in-tray + `janus secrets max-age` CLI. | ~~M~~ |
| ~~**Break-glass access** — time-boxed role elevation with a mandatory reason, stamped into the audit chain~~ **SHIPPED 2026-07-23** — guarded self-service (elevate only on scopes you already hold, ≤ owner, mandatory reason, TTL-clamped), authz max(bound, active grant) overlay, loud fail-closed audit + notification, migration 000031. | ~~M~~ |
| ~~**Per-token IP allowlists** and token usage anomaly notes (new IP → in-tray)~~ **SHIPPED 2026-07-23** — CIDR allowlist enforced in the auth middleware (tokens only, fail-closed), value-free new-IP note via `token_seen_ips` + in-tray, migration 000032. | ~~M~~ |
| ~~**GCP KMS / Azure Key Vault auto-unseal**~~ **SHIPPED 2026-07-23** — both on the parameterized `KMSUnsealer` (AWS unchanged); `JANUS_SEAL_TYPE=gcpkms`/`azurekv` with ambient cloud credentials, no migration, crypto at 100% coverage. | ~~M~~ |

### 2. Secret lifecycle & editor

| Feature | Why | Effort |
|---|---|---|
| ~~**Dotenv/properties import**~~ **SHIPPED 2026-07-19** — Import… in the editor: paste or choose a `.env`/`.properties` file, preview with per-key selection (new / overwrite / invalid), stage into the dirty buffer, commit as one version | The first thing every migrating user does is re-key an existing `.env` by hand. | ~~S~~ |
| ~~**Value generator** in the editor (random password / hex / base64, length picker)~~ **SHIPPED 2026-07-22** — client-side CSPRNG "Gen" popover (password w/ symbols & exclude-ambiguous toggles / hex / base64 + length). | ~~S~~ |
| ~~**Unused-secret detection** — "not read in 90 days" chip from audit data~~ **SHIPPED 2026-07-23** — advisory per-key last-read from `secret.reveal` audit events; `last_read_at`+`unused` on the masked list, editor chip + Overview in-tray count, `JANUS_UNUSED_SECRET_DAYS` (default 90), migration 000029. Value-free. | ~~M~~ |
| ~~**Per-key read insights** — last-read + 30-day sparkline in the editor row~~ **SHIPPED 2026-07-23** — value-free `GET .../read-insights` (last-read + 30-day daily reveal counts) from audit events, reusing the 000029 index; editor Sparkline panel. | ~~M~~ |
| ~~**Cross-environment diff view** — pick any two configs, see key-level presence/drift (values masked)~~ **SHIPPED 2026-07-23** — value-free `GET /v1/configs/{cid}/compare?against=` (booleans + origins only), dual `secret:read`, audited `config.compare`; new Compare screen. | ~~M~~ |
| ~~**Secret annotations** — owner + note metadata per key (never values)~~ **SHIPPED 2026-07-23** — value-free per-key owner/note (migration 000033), `secret:write` to set, editor affordance; mirrors max-age. | ~~M~~ |
| ~~**Require-approval-for-prod-edits** toggle~~ **SHIPPED 2026-07-24** — per-config `require_approval`; protected saves file an envelope-encrypted pending edit request that a different user approves (four-eyes, self-approval blocked, mark-on-success CAS) or rejects. Migration 000036, value-free, crypto 100%. | ~~M~~ |

### 3. Integrations & delivery

| Feature | Why | Effort |
|---|---|---|
| ~~**More sync providers**: GitLab CI, Cloudflare Workers, Vercel/Netlify env, AWS SSM/Secrets Manager~~ **ALL SHIPPED 2026-07-23** — the sync engine now has **8 providers**: `github`, `k8s`, `gitlab`, `aws_ssm`, `cloudflare`, `aws_secrets`, `vercel`, `netlify`. No migration. | ~~M each~~ |
| ~~**More CI federation issuers**: GitLab, Buildkite, CircleCI OIDC~~ **SHIPPED 2026-07-23** — provider-aware required-claim rule + issuer presets, single-active-issuer model. No migration. | ~~S each~~ |
| ~~**Inbound one-shot importers**: Doppler, Vault KV, AWS SM → project/config tree~~ **SHIPPED 2026-07-24** (CLI-first) — `janus import doppler|vault|aws-sm`: fetch → map to a Janus project/config → one batched write via the existing client; default value-free `--dry-run`, `--confirm` to write. No new endpoint/migration/dep. Web wizard = possible follow-up. | ~~L~~ |
| ~~**Notifications**: webhook + Slack + **SMTP** for rotation failures, sync errors, denials, pending approvals (upstream gap 1.14)~~ **SHIPPED** — webhook/Slack 2026-07-21 (migration 000024), SMTP email 2026-07-23 (migration 000027). | ~~M~~ |
| **Terraform provider** (projects, configs, secrets-as-writes, tokens, bindings) | Infra teams won't click UIs; declarative config is table stakes. | L |
| **Client SDKs** (~~Go~~, TypeScript, Python) with in-process caching + lease renewal | `janus run` covers processes; apps wanting native reads shouldn't hand-roll HTTP. **Go SDK SHIPPED 2026-07-24** — standalone `sdk/go/` (zero deps): typed reads + memory-only TTL cache + dynamic-lease renewal. TS + Python remain. | L |
| ~~**More rotators**: MySQL, Redis ACL, AWS IAM access keys, generic OAuth client-credential refresh~~ **ALL SHIPPED 2026-07-24** — 6 rotators (postgres, webhook, mysql, redis, + generating-rotator `oauth` & `aws_iam`); migration 000037 relaxes the type CHECK (fixed a latent gap that had also blocked mysql/redis). | ~~M each~~ |

### 4. Operations & observability

| Feature | Why | Effort |
|---|---|---|
| ~~**Prometheus `/metrics`** — request rates/latency, seal state, lease counts, rotation/sync failure gauges, audit head seq~~ **SHIPPED 2026-07-22** — hand-rolled zero-dep, `JANUS_METRICS_TOKEN`-gated; + `JANUS_LOG_LEVEL`/`FORMAT`. | ~~S~~ |
| ~~**Scheduled encrypted backups** to S3-compatible storage with retention + a restore-rehearsal command~~ **SHIPPED 2026-07-23** — `internal/backupsched` (JANUS_BACKUP_TICK, S3-compatible + static creds, retention prune, `backup_runs` migration 000035) + `janus backup rehearse` (verify-without-clobber). Sealed-artifact preserved. | ~~M~~ |
| ~~**Audit shipping** — stream JSONL to webhook/syslog/S3 for SIEM ingestion, with a high-water mark~~ **SHIPPED 2026-07-23** — `internal/auditship` tails past a durable high-water mark (migration 000034) to webhook (HMAC) or RFC 5424 syslog, advance-on-success (at-least-once). `JANUS_AUDIT_SHIP_*`. (S3 destination via scheduled backups.) | ~~M~~ |
| ~~**Health panel in Settings** — DB latency, scheduler tick ages, failed-run counts~~ **SHIPPED 2026-07-22** — admin `GET /v1/sys/status` + Settings → Health (DB latency/pool, seal, audit head, per-engine tick staleness, failed-run counts). | ~~S~~ |
| ~~**First-run onboarding checklist** on the dashboard (create project → add secrets → mint token → `janus run`) (upstream gap 1.13)~~ **SHIPPED 2026-07-23** — self-checking steps (project / secret / token existence) + copyable `janus run` block; hides once set up, dismissible. Frontend-only. | ~~S~~ |

### 5. UI polish

| Feature | Why | Effort |
|---|---|---|
| ~~**Global key search** in the command palette (search masked key names across configs)~~ **SHIPPED 2026-07-22** — `GET /v1/search/keys`, names-only, deny-by-default per-config filter; palette "Secret keys" group + `?key=` editor deep-link. | ~~S~~ |
| ~~**Bulk row selection** in the editor — multi-select → delete / promote / export~~ **SHIPPED 2026-07-23** — checkboxes + select-all (filter-aware), bulk Delete (dirty-buffer) / Reveal (audited) / Export (confirm-gated). Frontend-only. | ~~M~~ |
| ~~**JSON/PEM awareness** for file-type secrets — pretty-print, validate, syntax hint in the value editor~~ **SHIPPED 2026-07-23** — format badge + client-side well-formedness check while editing (JSON parse errors, PEM label/base64 faults), one-click Pretty-print for valid JSON; advisory, never blocks a save. | ~~S~~ |
| ~~**Shortcuts help modal** (`?`) + `g`-prefixed nav chords~~ **SHIPPED 2026-07-23** — `?` help modal + `g`-chord navigation to every screen; suppressed while typing / in dialogs. | ~~S~~ |
| ~~**Accessibility pass** — focus traps in modals, ARIA on tables/stamps, reduced-motion audit~~ **SHIPPED 2026-07-24** — reusable `trapFocus` action on all modals, `<th scope>`/`aria-label` on all tables, hardened `prefers-reduced-motion`; svelte-check 0 errors / 0 warnings. | ~~M~~ |
| **Mobile/tablet layout** for read-mostly screens (dashboard, audit, approvals) | Approving a promotion from a phone is a real workflow. | M |

---

## Suggested near-term slate

The roadmap is essentially exhausted (require-approval, all six rotators, inbound
importers, the Go SDK, and the accessibility pass all shipped 2026-07-24). What's
left:

1. **Terraform provider** (L) — the last big adoption lever.
2. **Client SDKs — TypeScript + Python** (L) — mirror the shipped Go SDK.
3. **Mobile/tablet layout** (5.6) — responsive read-mostly screens.
4. **Import web wizard** (S) on top of the shipped `janus import` CLI.
5. **Odds & ends** — more sync/CI targets on demand; Go SDK auto-renew helper.
