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
| **Break-glass access** — time-boxed role elevation with a mandatory reason, stamped into the audit chain | Incidents need a paved road that is loud, not shared root credentials. | M |
| **Per-token IP allowlists** and token usage anomaly notes (new IP → in-tray) | Cheap, high-signal containment for exfiltrated tokens; IPs are already in every audit event. | M |
| **GCP KMS / Azure Key Vault auto-unseal** | The `Unsealer` interface already exists; AWS-only is an adoption blocker off-AWS. | M |

### 2. Secret lifecycle & editor

| Feature | Why | Effort |
|---|---|---|
| ~~**Dotenv/properties import**~~ **SHIPPED 2026-07-19** — Import… in the editor: paste or choose a `.env`/`.properties` file, preview with per-key selection (new / overwrite / invalid), stage into the dirty buffer, commit as one version | The first thing every migrating user does is re-key an existing `.env` by hand. | ~~S~~ |
| ~~**Value generator** in the editor (random password / hex / base64, length picker)~~ **SHIPPED 2026-07-22** — client-side CSPRNG "Gen" popover (password w/ symbols & exclude-ambiguous toggles / hex / base64 + length). | ~~S~~ |
| **Unused-secret detection** — "not read in 90 days" chip from audit data, with a bulk-review view | Dead secrets are silent risk; all the data is already in `audit_events`. | M |
| **Per-key read insights** — last-read + 30-day sparkline in the editor row | Turns "can I delete this?" from a guess into a lookup. | M |
| **Cross-environment diff view** — pick any two configs, see key-level presence/drift (values masked) | Promote covers adjacent stages; "why does staging behave differently from prod" needs an arbitrary diff. | M |
| **Secret annotations** — owner + note metadata per key (never values) | "What is this and who do I ask" is unanswerable today; helps rotation triage. | M |
| **Require-approval-for-prod-edits** toggle — direct saves to protected configs create a promotion-style request instead | The approvals machinery exists; extending four-eyes review to raw prod edits closes its biggest bypass. | M |

### 3. Integrations & delivery

| Feature | Why | Effort |
|---|---|---|
| **More sync providers**: GitLab CI variables, Cloudflare Workers secrets, Vercel/Netlify env, AWS SSM/Secrets Manager (outbound) | The sync engine is provider-pluggable; each new target is mostly an adapter + creds form. Prioritize by demand. | M each |
| **More CI federation issuers**: GitLab, Buildkite, CircleCI OIDC | The trust-binding model generalizes; only issuer/claims mapping differs. | S each |
| **Inbound one-shot importers**: Doppler, Vault KV, AWS SM → project/config tree | Migration friction is the #1 adoption cost; a `janus import` command + wizard screen. | L |
| ~~**Notifications**: webhook + Slack + **SMTP** for rotation failures, sync errors, denials, pending approvals (upstream gap 1.14)~~ **SHIPPED** — webhook/Slack 2026-07-21 (migration 000024), SMTP email 2026-07-23 (migration 000027). | ~~M~~ |
| **Terraform provider** (projects, configs, secrets-as-writes, tokens, bindings) | Infra teams won't click UIs; declarative config is table stakes. | L |
| **Client SDKs** (Go, TypeScript, Python) with in-process caching + lease renewal | `janus run` covers processes; apps wanting native reads shouldn't hand-roll HTTP. | L |
| **More rotators**: MySQL, Redis ACL, AWS IAM access keys, generic OAuth client-credential refresh | Same crash-safe framework, new drivers. | M each |

### 4. Operations & observability

| Feature | Why | Effort |
|---|---|---|
| ~~**Prometheus `/metrics`** — request rates/latency, seal state, lease counts, rotation/sync failure gauges, audit head seq~~ **SHIPPED 2026-07-22** — hand-rolled zero-dep, `JANUS_METRICS_TOKEN`-gated; + `JANUS_LOG_LEVEL`/`FORMAT`. | ~~S~~ |
| **Scheduled encrypted backups** to S3-compatible storage with retention + a restore-rehearsal command | A backup button is not a backup strategy; the sealed-material export already exists. | M |
| **Audit shipping** — stream JSONL to webhook/syslog/S3 for SIEM ingestion, with a high-water mark | Compliance teams want the ledger in *their* store; export-on-demand doesn't scale to that. | M |
| ~~**Health panel in Settings** — DB latency, scheduler tick ages, failed-run counts~~ **SHIPPED 2026-07-22** — admin `GET /v1/sys/status` + Settings → Health (DB latency/pool, seal, audit head, per-engine tick staleness, failed-run counts). | ~~S~~ |
| ~~**First-run onboarding checklist** on the dashboard (create project → add secrets → mint token → `janus run`) (upstream gap 1.13)~~ **SHIPPED 2026-07-23** — self-checking steps (project / secret / token existence) + copyable `janus run` block; hides once set up, dismissible. Frontend-only. | ~~S~~ |

### 5. UI polish

| Feature | Why | Effort |
|---|---|---|
| ~~**Global key search** in the command palette (search masked key names across configs)~~ **SHIPPED 2026-07-22** — `GET /v1/search/keys`, names-only, deny-by-default per-config filter; palette "Secret keys" group + `?key=` editor deep-link. | ~~S~~ |
| **Bulk row selection** in the editor — multi-select → delete / promote / export | One-at-a-time actions don't survive 40-key configs. | M |
| ~~**JSON/PEM awareness** for file-type secrets — pretty-print, validate, syntax hint in the value editor~~ **SHIPPED 2026-07-23** — format badge + client-side well-formedness check while editing (JSON parse errors, PEM label/base64 faults), one-click Pretty-print for valid JSON; advisory, never blocks a save. | ~~S~~ |
| ~~**Shortcuts help modal** (`?`) + `g`-prefixed nav chords~~ **SHIPPED 2026-07-23** — `?` help modal + `g`-chord navigation to every screen; suppressed while typing / in dialogs. | ~~S~~ |
| **Accessibility pass** — focus traps in modals, ARIA on tables/stamps, reduced-motion audit | The bones are semantic; a deliberate pass would close the gaps. | M |
| **Mobile/tablet layout** for read-mostly screens (dashboard, audit, approvals) | Approving a promotion from a phone is a real workflow. | M |

---

## Suggested near-term slate

If I picked the next five, weighing leverage against effort (the earlier
slates — dotenv import, metrics + health, notifications, session management +
TOTP, global key search, JSON/PEM awareness, shortcuts help, first-run
onboarding checklist — are all shipped):

1. **More sync providers** (3.1, e.g. GitLab CI / AWS SSM) — extend the
   provider-pluggable sync engine.
2. **Unused-secret detection** (2.3) — the data is already in `audit_events`;
   companion to the shipped max-age nags.
3. **Cross-environment diff view** (2.5) — arbitrary key-level config drift.
4. **GCP KMS / Azure Key Vault auto-unseal** (1.7) — off-AWS adoption.
5. **Token `last_used` / user `last_login` tracking** — stale-token warning +
   "last login" column.

(Native TLS listener, advisory secret max-age / expiry, and the first-run
onboarding checklist all shipped 2026-07-23.)
