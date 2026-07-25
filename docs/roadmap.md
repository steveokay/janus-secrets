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
| ~~**More sync providers**: GitLab CI, Cloudflare Workers, Vercel/Netlify env, AWS SSM/Secrets Manager~~ **ALL SHIPPED 2026-07-23** — the sync engine now has **8 providers**: `github`, `k8s`, `gitlab`, `aws_ssm`, `cloudflare`, `aws_secrets`, `vercel`, `netlify`. No migration.  **CORRECTION (2026-07-25):** these were shipped in code but NOT actually usable — migration `000011` pinned `sync_targets.provider` to `CHECK (provider IN ('github','k8s'))` and no later migration widened it, so persisting a `gitlab`/`aws_ssm`/`cloudflare`/`aws_secrets`/`vercel`/`netlify` target failed the constraint. Found and fixed by migration `000041` (PR #175), with a store regression test creating a target for all eight. Same class of bug as the `rotation_policies.type` CHECK that `000037` fixed — a CREATE-time enum later features outgrew. | ~~M each~~ |
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
| ~~**`SECURITY.md`** — vulnerability disclosure policy~~ **SHIPPED 2026-07-25** — GitHub private vulnerability reporting (primary) + email fallback, response-target table, supported versions, in/out-of-scope tied to the threat model, safe-harbor, verify-what-you-run pointer. | ~~S~~ |
| ~~**Signed releases** — cosign keyless + SBOM (syft) + SLSA provenance (binaries + GHCR image)~~ **SHIPPED 2026-07-25** — goreleaser: Syft SBOMs, Cosign keyless signature over `checksums.txt`, Cosign-signed multi-arch manifests (by digest); release workflow adds `attestations: write`, installs Cosign+Syft, emits SLSA build-provenance for binaries/archives **and** the image + an image-SBOM attestation. `goreleaser check` green; needs a real `v*` tag to exercise fully. | ~~S–M~~ |
| ~~**Dependabot** — automated dependency update PRs (Go modules, npm, actions)~~ **SHIPPED 2026-07-25** — `.github/dependabot.yml`, weekly grouped PRs across all 3 Go modules, both npm packages, the Python SDK, and GitHub Actions. | ~~S~~ |
| ~~**Threat-model document** — what Janus defends against and explicitly what it does not~~ **SHIPPED 2026-07-25** — `docs/threat-model.md`: trust boundaries, assets, 7-actor adversary table, defended properties→mechanisms, explicit non-defenses, operator responsibilities, crypto assumptions. | ~~S~~ |

### 7. Product depth (post-1.0)

| Feature | Why | Effort |
|---|---|---|
| ~~**`janus run --watch`** — restart/re-exec the child process when the bound config changes~~ **DONE 2026-07-25 (PR #152, pending merge)** — `--watch [--watch-interval 10s]` polls the bound config's current version (value-free version metadata only) and on a bump gracefully restarts the child (SIGTERM→5s grace→Kill, cross-platform build-tagged; Windows uses Kill), re-fetching secrets and re-spawning with fresh env; std streams stay wired. No `--watch` = unchanged single run. | ~~M~~ |
| ~~**`janus render`** — render secrets into a config-file template, plus an optional agent-style refresh loop~~ **DONE 2026-07-25 (PR #152, pending merge)** — `render --template <f> --out <f> [--watch] [--interval]`: Go `text/template` (missingkey=error) with secrets as both `{{ .KEY }}` and a `secret "KEY"` func; atomic `0600` write (reuses the `download --plain` file path), prints a plaintext-file notice; `--watch` re-renders on version bumps via the shared poll helper. | ~~M~~ |
| ~~**Kubernetes service-account OIDC federation** — accept cluster OIDC issuers in the existing CI-federation trust bindings~~ **SHIPPED 2026-07-25 (PR #174)** — trust is now **multi-issuer** (migration 000042; the pre-existing single row is backfilled), because the old single-issuer config would have made a cluster issuer *evict* GitHub Actions. The verifier is chosen from the token's `iss`, re-pinned after go-oidc verifies the signature against that issuer's JWKS, and bindings are filtered by the signing issuer before any claim matching — a token from issuer A can never satisfy a binding for issuer B. Nested claims flatten to dotted paths so bindings can pin `kubernetes.io.namespace` / `…serviceaccount.name`; non-string scalars are still dropped (no type coercion) and ambiguous literal-vs-nested keys reject the token. A `kubernetes` preset requires a binding to pin service-account identity. | ~~M~~ |
| ~~**Sync drift detection** — a scheduled verify pass that reads each sync target back and flags manual tampering (in-tray + notification)~~ **SHIPPED 2026-07-25 (PR #175)** — an optional `Verifier` interface with a declared capability, so no destination over-claims: **value drift** where the API can return values (`k8s`, `gitlab`, `aws_ssm`, `aws_secrets`, `vercel`, `netlify`) and **names-only** where it cannot (`github`, `cloudflare` are write-only by design) — those report `values_compared: false` end-to-end so "no drift" never reads as "values verified". Value-free throughout: `hmac.Equal` over keyed HMACs, never persisting digests or plaintext. Migration 000041, `JANUS_SYNC_VERIFY_TICK` (default off), manual `POST …/verify`, `sync.drift` notification, ops-console surfacing. | ~~M~~ |
| ~~**WebAuthn/passkeys** — second factor (and passwordless) for the UI login~~ **SHIPPED 2026-07-25 (PR #176)** — enrolment + single-step passkey sign-in + management (migration 000040). Uses `github.com/go-webauthn/webauthn` under a **recorded CLAUDE.md exception** (approved 2026-07-25, the second and final crypto-adjacent one); only public credential material is stored. Single-use expiring challenges; the account is taken from the challenge, never the client response; counter regression is fatal; `userVerification: required` on every ceremony, which is why a passkey sign-in is not additionally TOTP-gated; RP ID/origins from `JANUS_WEBAUTHN_*`, boot-validated, never the request Host; `login/begin` returns a stable decoy so it is not an account-existence oracle. **Passwordless/discoverable login deferred** (enrolled with `residentKey: preferred`, so adding conditional UI later needs no re-enrolment). | ~~M–L~~ |
| **WebAuthn passwordless / discoverable login** — username-less sign-in with conditional UI (autofill) | Explicitly deferred in PR #176 rather than half-implemented. Credentials already enrol with `residentKey: preferred`, so enabling it later needs no re-enrolment — only the discoverable-login handler and autofill wiring. | S–M |
| **Terraform provider: environment-scoped tokens + batch-secret resource** | Both were deferred when the provider shipped; a resource per secret is unwieldy for a large config, and tokens can currently only be config-scoped. | M |
| **SDK depth** — background auto-renew for dynamic leases, plus `Run`-style helpers in all three SDKs | Deferred when the SDKs shipped: callers must renew leases by hand today, which is the part most likely to be got wrong. | M |
| **Web import wizard** — drive the `janus import` sources (Doppler / Vault KV / AWS SM) from the UI | Import shipped CLI-first with the wizard recorded as a possible follow-up; the UI is where a migrating user starts. | M |

### 8. Operational longevity

| Feature | Why | Effort |
|---|---|---|
| ~~**Audit retention with hash-chain checkpointing** — periodic signed checkpoints so verified-and-shipped prefixes can be archived/pruned without breaking `GET /v1/audit/verify`~~ **DONE 2026-07-25 (PR #153, pending merge)** — migration `000039_audit_checkpoints`; HMAC-SHA256 checkpoint MAC over length-prefixed `through_seq‖through_hash‖event_count` with a **domain-separated** key derived from the master-key-wrapped token-HMAC key (`internal/crypto` untouched, stdlib only); owner-only `audit:manage` endpoints `POST/GET /v1/audit/checkpoint` + `POST /v1/audit/prune`; verify validates the latest checkpoint MAC then walks from `through_seq+1` (forged checkpoint → `checkpoint_mac_invalid`, no genesis fallback); prune is fail-closed (needs valid checkpoint, clamps to the auditship high-water mark, never deletes anchors); audit-viewer checkpoint stamp + owner "Create checkpoint". | ~~M–L~~ |
| ~~**Secret value-version retention** — optional, owner-set "hard-destroy value versions older than N days/versions" policy~~ **SHIPPED 2026-07-25 (PR #181)** — migration 000043; owner-only `secret:prune`; `GET/PUT /v1/configs/{cid}/versions/retention` + `POST .../versions/prune`; `janus secrets retention|prune`; floors via `JANUS_SECRET_RETAIN_MIN_VERSIONS`/`_MIN_DAYS` plus a per-config override that can only retain **more**. The design point: pruning deletes whole **config versions** (their manifest entries follow the `config_version_id` cascade) and then garbage-collects `secret_values` no surviving entry references. Deleting value rows directly — the obvious reading of the task — would have triggered the `secret_values ON DELETE CASCADE` from 000005 and silently removed manifest entries, leaving old versions rollback-able to an *incomplete* config with no error. The invariant is re-checked inside the transaction and a shortfall aborts the prune. `dry_run` defaults true (it performs the work and rolls back, so the preview is exact); pending edit requests block the config, pending-promotion sources are pinned, soft-deleted configs are never touched; no background sweep by design. | ~~M~~ |
| ~~**Grafana dashboard JSON + example alert rules** shipped in `docs/`~~ **SHIPPED 2026-07-25 (PR #180)** — `deploy/grafana/janus-overview.json` (24 panels: seal state first, HTTP rate/latency/5xx, per-engine scheduler tick age, rotation/sync failures, dynamic leases, DB pool saturation, audit head slope, Go runtime), `alerts.yaml` (13 Prometheus-format rules with `for:` durations and operator-facing annotations), and a README with token setup, `scrape_configs`, and a `ServiceMonitor`. Every expression was evaluated against a live Prometheus scraping a real instance (23/23 non-empty) and the dashboard imported into Grafana 13.1.0 — so the panels are known to render, not merely well-formed. Found that three counter-named series are actually gauges (`audit_head_seq`, `rotation_runs_failed`, `sync_runs_failed`), so they use `deriv()`/`offset` rather than `rate()`. | ~~S~~ |

### 9. Test depth

| Feature | Why | Effort |
|---|---|---|
| ~~**Playwright smoke suite** — a browser E2E pass (init → unseal → login → create project → save secret → audited reveal) against the docker stack~~ **DONE 2026-07-25 (PR #151, pending merge)** — `web/tests/e2e/smoke.spec.ts` (8 ordered steps incl. Shamir 5/3 init + unseal quorum + audited reveal + chain-verified badge), `playwright.config.ts` (`JANUS_E2E_BASE_URL`, default `:8210`), opt-in `.github/workflows/e2e.yml` (`workflow_dispatch`/`e2e` label; not in per-PR CI), additive `data-testid`s only. Full run needs the live docker stack. | ~~M~~ |
| ~~**Go fuzz tests** — native fuzzing for the reference parser (`${…}`), `.env`/properties importers, PEM sniffing, RESP encoding, federation JWT claims~~ **SHIPPED 2026-07-25** — 7 `Fuzz*` targets that assert **security invariants** rather than mere absence of panics: filename-safety of secret keys (the `download --format files` traversal guard), RESP encode→decode round-trip (Redis command injection), the `${…}` reference parser (attacker-reachable via any secret value), the transit ciphertext envelope, and CI-federation claim projection/matching. The federation target immediately found a latent fail-open (`claimsSatisfy` treated an absent claim as satisfying an empty required value) — not exploitable, since `CreateFederationBinding` rejects empty values and is the only write path, but hardened so the matcher is safe independently. `.env`/properties and PEM sniffing turned out to be **client-side TypeScript** with no Go parser, so no Go target applies. | ~~S–M~~ |

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

1. ~~**Trust & supply chain sweep** (6.1–6.4)~~ **DONE 2026-07-25** — `SECURITY.md`
   + `docs/threat-model.md` + `.github/dependabot.yml` + cosign/SBOM/SLSA-provenance
   in goreleaser & the release workflow.
2. ~~**`janus run --watch` + `janus render`** (7.1–7.2)~~ **DONE 2026-07-25 (PR #152).**
3. ~~**Audit chain checkpointing + retention** (8.1)~~ **DONE 2026-07-25 (PR #153).**
4. ~~**Playwright smoke suite** (9.1)~~ **DONE 2026-07-25 (PR #151).**

Then, as demand dictates: registry publishes (the three open boxes above, each
gated on a maintainer credential), k8s SA federation (7.3), sync drift
detection (7.4), passkeys (7.5), value-version retention (8.2), Grafana
dashboard (8.3), fuzzing (9.2), mobile/tablet layout (5.6), import web wizard,
and SDK depth (background auto-renew + `Run`-style helpers).
