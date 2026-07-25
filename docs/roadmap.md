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
| ~~**Require-approval-for-prod-edits** toggle~~ **SHIPPED 2026-07-24** — per-config `require_approval`; protected saves file an envelope-encrypted pending edit request that a different user approves (four-eyes, self-approval blocked, mark-on-success CAS) or rejects. Migration 000036, value-free, crypto 100%. **Hardened 2026-07-24 (PR #148):** rollback + promote-apply now honor it too, and approval is claim-before-commit — see Release & distribution. | ~~M~~ |

### 3. Integrations & delivery

| Feature | Why | Effort |
|---|---|---|
| ~~**More sync providers**: GitLab CI, Cloudflare Workers, Vercel/Netlify env, AWS SSM/Secrets Manager~~ **ALL SHIPPED 2026-07-23** — the sync engine now has **8 providers**: `github`, `k8s`, `gitlab`, `aws_ssm`, `cloudflare`, `aws_secrets`, `vercel`, `netlify`. No migration. | ~~M each~~ |
| ~~**More CI federation issuers**: GitLab, Buildkite, CircleCI OIDC~~ **SHIPPED 2026-07-23** — provider-aware required-claim rule + issuer presets, single-active-issuer model. No migration. | ~~S each~~ |
| ~~**Inbound one-shot importers**: Doppler, Vault KV, AWS SM → project/config tree~~ **SHIPPED 2026-07-24** (CLI-first) — `janus import doppler|vault|aws-sm`: fetch → map to a Janus project/config → one batched write via the existing client; default value-free `--dry-run`, `--confirm` to write. No new endpoint/migration/dep. Web wizard = possible follow-up. | ~~L~~ |
| ~~**Notifications**: webhook + Slack + **SMTP** for rotation failures, sync errors, denials, pending approvals (upstream gap 1.14)~~ **SHIPPED** — webhook/Slack 2026-07-21 (migration 000024), SMTP email 2026-07-23 (migration 000027). | ~~M~~ |
| ~~**Terraform provider** (projects, configs, secrets-as-writes, tokens, bindings)~~ **SHIPPED 2026-07-24** — `terraform-provider-janus/` (own module): project/env/config/secret/service-token resources (sensitive value + token-in-state caveat) + secret/config data sources, CRUD + import; hermetic unit tests. | ~~L~~ |
| ~~**Client SDKs** (Go, TypeScript, Python) with in-process caching + lease renewal~~ **ALL SHIPPED 2026-07-24** — standalone `sdk/go/` (zero deps), `sdk/ts/` (`janus-client`), `sdk/python/` (`janus_client`), each: typed reads + memory-only TTL cache + dynamic-lease renewal + typed errors. | ~~L~~ |
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

_Sections 6–9 added 2026-07-24 after the v0.1.0 release — the post-1.0 roadmap
from a full-system review (verified gaps, not speculation)._

### 6. Trust & supply chain

| Feature | Why | Effort |
|---|---|---|
| **`SECURITY.md`** — vulnerability disclosure policy (contact, scope, response SLO, safe-harbor) | A secrets manager without a disclosure policy is a red flag to any serious adopter; near-mandatory for the category. | S |
| **Signed releases** — cosign keyless signing + SBOM (syft) + SLSA provenance, wired into goreleaser for both binaries and the GHCR image | "Verify what you run" is table stakes for a security product; today releases are unsigned with no SBOM. Mostly goreleaser config. | S–M |
| **Dependabot/renovate** — automated dependency update PRs (Go modules, npm, actions) | The x/text vuln bump was done by hand once already; automate it. | S |
| **Threat-model document** — what Janus defends against and explicitly what it does not (root on the box, malicious Postgres superuser, compromised unseal quorum, …) | The crypto docs describe mechanisms; a written adversary model builds real credibility and scopes future security work. | S |

### 7. Product depth (post-1.0)

| Feature | Why | Effort |
|---|---|---|
| **`janus run --watch`** — restart/re-exec the child process when the bound config changes (poll the config version, later SSE) | `run` is the flagship; watch-mode is the most-missed Doppler behavior for long-running dev processes. | M |
| **`janus render`** — render secrets into a config-file template (Vault-agent style), plus an optional agent-style refresh loop | Apps that need files, not env vars; pairs naturally with `--watch`. | M |
| **Kubernetes service-account OIDC federation** — accept cluster OIDC issuers in the existing CI-federation trust bindings | In-cluster workloads fetch secrets keylessly — a cleaner k8s story than pushed Secrets, with no controller (stays inside the non-goals). | M |
| **Sync drift detection** — a scheduled verify pass that reads each sync target back and flags manual tampering (in-tray + notification) | Sync is push-only today; nothing notices when someone edits the GitHub/k8s copy out from under Janus. | M |
| **WebAuthn/passkeys** — second factor (and passwordless) for the UI login | The explicitly parked TOTP follow-up; passkeys are increasingly expected. | M–L |

### 8. Operational longevity

| Feature | Why | Effort |
|---|---|---|
| **Audit retention with hash-chain checkpointing** — periodic signed checkpoints so verified-and-shipped prefixes can be archived/pruned without breaking `GET /v1/audit/verify` | `audit_events` grows forever and the chain currently forbids pruning — the one true time bomb in the design. Audit shipping is the archive path. | M–L |
| **Secret value-version retention** — optional, owner-set "hard-destroy value versions older than N days/versions" policy | Every save keeps every DEK/ciphertext forever; long-lived instances need an explicit, audited pruning policy. | M |
| **Grafana dashboard JSON + example alert rules** shipped in `docs/` | `/metrics` exists; give operators the dashboard instead of making each one build it. | S |

### 9. Test depth

| Feature | Why | Effort |
|---|---|---|
| **Playwright smoke suite** — a browser E2E pass (init → unseal → login → create project → save secret → audited reveal) against the docker stack | The Atrium SPA has zero browser tests (`npm test` is `echo no web tests`); svelte-check + build can't catch behavioral regressions. | M |
| **Go fuzz tests** — native fuzzing for the reference parser (`${…}`), `.env`/properties importers, PEM sniffing, RESP encoding, federation JWT claims | Zero `Fuzz*` functions in a codebase that parses hostile input; Go makes this cheap. | S–M |

---

## Release & distribution

_The binary + container ship automatically on a `v*` tag (goreleaser → GitHub
Release + GHCR). The language-package registries do **not** — each needs an
account/credential the maintainer must supply, so they are tracked separately._

- [x] ~~**v0.1.0 — first tagged release**~~ **SHIPPED 2026-07-24** — tag `v0.1.0`
      (`main 9490a7d`); GitHub Release (6 multi-arch binaries + `checksums.txt`) +
      multi-arch GHCR image `ghcr.io/steveokay/janus:0.1.0`. CHANGELOG
      consolidated (nothing was tagged before → all of `main` ships as 0.1.0).
- [x] ~~**Pre-release security hardening (PR #148)**~~ **SHIPPED 2026-07-24** —
      the consolidation security review's findings: closed two require-approval
      (four-eyes) bypasses (**rollback** + **promote-apply** committed directly
      to protected configs; both now route through the edit-request flow via
      `promote.Plan` / `secrets.RollbackChanges`), made edit-request approval
      **claim-before-commit** (no double-commit), added **GitLab sync
      project-id validation** (URL-injection guard), and protected pending edit
      requests from **KEK-version retirement**. gosec 0, full suite green.
- [x] ~~**Post-1.0 white-box audit remediation**~~ **DONE 2026-07-24** (branch
      `hardening/security-audit-fixes`, pending merge; details in the internal
      audit tracker) — 5-agent white-box audit found 0 CRITICAL. The lone **HIGH**
      (H-1, promotion-request four-eyes) was re-examined and is **not** an
      exploitable bypass (`secret:promote` implies `secret:write` and requester ≠
      approver is enforced) — the invariant is now documented at the site rather
      than "fixed" with dead code. Fixed: **M-1** member-grant delegation capped at
      `BoundRole` not `EffectiveRole` (break-glass can no longer be made durable);
      **M-2** TOTP replay rejected by persisting the last consumed step (migration
      000038); **M-3** password change now revokes other sessions + rotates the
      current cookie; **M-4** systemic SSRF closed by one shared `nethard`
      hardened dialer (blocks link-local/cloud-metadata, `CheckRedirect`, bounded
      per-dial timeouts) across **every** operator-configured outbound caller —
      rotation (webhook/oauth/notify + Postgres/MySQL/Redis dials, **L-2**),
      notification (webhook/Slack/SMTP), sync (k8s/gitlab/cloudflare/vercel/
      netlify), and **OIDC discovery/JWKS** (I-4); **L-1** uniform API
      security-header middleware; **L-5** `.dockerignore`. L-3/L-4/L-6 are
      documented accepted tradeoffs. gosec 0.
- [ ] **Publish the TypeScript SDK to npm** (`janus-client`, `sdk/ts/`) — needs
      an npm account + automation token; add an npm-publish CI job (on an
      `sdk-ts-v*` tag or manual dispatch) running `npm publish` with `NPM_TOKEN`.
- [ ] **Publish the Python SDK to PyPI** (`janus_client`, `sdk/python/`) — needs
      a PyPI project + token (OIDC Trusted Publishing preferred); a `python -m
      build` → `pypa/gh-action-pypi-publish` job.
- [ ] **Publish the Terraform provider to the Terraform Registry**
      (`terraform-provider-janus/`) — needs a **GPG signing key** + a Registry
      account linked to the repo, and the provider's **own** goreleaser release
      workflow (the Registry ingests GPG-signed release archives from a `v*` tag
      in the provider module). Deferred provider work rides along: env-scoped
      tokens + a batch-secret resource.

---

## Suggested near-term slate

**The original roadmap (sections 1–5) is exhausted** and **v0.1.0 is released**
(see Release & distribution). The post-1.0 roadmap is sections 6–9. Suggested
first batch — **"Trust & Longevity"**, all parallel-friendly:

1. **Trust & supply chain sweep** (6.1–6.4) — `SECURITY.md` + threat model +
   dependabot + cosign/SBOM in goreleaser (one agent, mostly docs/config).
2. **`janus run --watch` + `janus render`** (7.1–7.2) — CLI-only.
3. **Audit chain checkpointing + retention** (8.1) — the deep one.
4. **Playwright smoke suite** (9.1) — web-only.

Then, as demand dictates: registry publishes (the three open boxes above, each
gated on a maintainer credential), k8s SA federation (7.3), sync drift
detection (7.4), passkeys (7.5), value-version retention (8.2), Grafana
dashboard (8.3), fuzzing (9.2), mobile/tablet layout (5.6), import web wizard,
and SDK depth (background auto-renew + `Run`-style helpers).
