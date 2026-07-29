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
| ~~**Secret annotations** — owner + note metadata per key (never values)~~ **SHIPPED 2026-07-23**, **REVISED 2026-07-28** — value-free per-key note (migration 000033), `secret:write` to set, editor affordance. Ownership moved to the PROJECT in migration 000049: per-key owner was the wrong grain and read confusingly beside group-owned projects. Advisory only, never an authorization input. | ~~M~~ |
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
| ~~**Mobile/tablet layout** for read-mostly screens (dashboard, audit, approvals)~~ **SHIPPED 2026-07-26 (PR #192)** — the SPA had **no shell breakpoint at all**: on a 390px phone the 236px cover took 60% of the screen and the remainder was **clipped, not scrolled** (`.desk` is `overflow: hidden`), so headline, folio bar and every ledger column were cut off with no way to reach them. The load-bearing fix is one line — `.desk { min-width: 0 }`. A grid item defaults to `min-width: auto` ("never narrower than my content"), and **ten screens already declared `.table-wrap { overflow-x: auto }`** whose scroll simply never engaged because the track never had to shrink. Below 1024px the cover becomes an off-canvas drawer (scrim, Escape, close-on-navigate, `aria-expanded`/`-controls`, `visibility: hidden` while closed so it is not a keyboard trap); 1024px keeps the sidebar for tablet **landscape**. `trapFocus` gained an optional `enabled` param because the cover is always mounted and only sometimes modal. One global `.page-head { flex-wrap: wrap }` fixes all **14** screens that declare it identically. Caught en route: the Audit result filter's `denied` segment was pushed off-screen and **unreachable** on a phone, and the Overview rosette's `right: -40px` bleed had been putting a horizontal scrollbar on **every width, desktop included**. Ships `web/tests/e2e/shots.mjs`, a read-only harness that screenshots every screen at phone/tablet/laptop in both themes and measures the **content area** — measuring only the document reported everything green, which is exactly how this shipped unnoticed. | ~~M~~ |

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
| ~~**`janus run --watch`** — restart/re-exec the child process when the bound config changes~~ **SHIPPED 2026-07-25 (PR #152)** — `--watch [--watch-interval 10s]` polls the bound config's current version (value-free version metadata only) and on a bump gracefully restarts the child (SIGTERM→5s grace→Kill, cross-platform build-tagged; Windows uses Kill), re-fetching secrets and re-spawning with fresh env; std streams stay wired. No `--watch` = unchanged single run. | ~~M~~ |
| ~~**`janus render`** — render secrets into a config-file template, plus an optional agent-style refresh loop~~ **SHIPPED 2026-07-25 (PR #152)** — `render --template <f> --out <f> [--watch] [--interval]`: Go `text/template` (missingkey=error) with secrets as both `{{ .KEY }}` and a `secret "KEY"` func; atomic `0600` write (reuses the `download --plain` file path), prints a plaintext-file notice; `--watch` re-renders on version bumps via the shared poll helper. | ~~M~~ |
| ~~**Kubernetes service-account OIDC federation** — accept cluster OIDC issuers in the existing CI-federation trust bindings~~ **SHIPPED 2026-07-25 (PR #174)** — trust is now **multi-issuer** (migration 000042; the pre-existing single row is backfilled), because the old single-issuer config would have made a cluster issuer *evict* GitHub Actions. The verifier is chosen from the token's `iss`, re-pinned after go-oidc verifies the signature against that issuer's JWKS, and bindings are filtered by the signing issuer before any claim matching — a token from issuer A can never satisfy a binding for issuer B. Nested claims flatten to dotted paths so bindings can pin `kubernetes.io.namespace` / `…serviceaccount.name`; non-string scalars are still dropped (no type coercion) and ambiguous literal-vs-nested keys reject the token. A `kubernetes` preset requires a binding to pin service-account identity. | ~~M~~ |
| ~~**Sync drift detection** — a scheduled verify pass that reads each sync target back and flags manual tampering (in-tray + notification)~~ **SHIPPED 2026-07-25 (PR #175)** — an optional `Verifier` interface with a declared capability, so no destination over-claims: **value drift** where the API can return values (`k8s`, `gitlab`, `aws_ssm`, `aws_secrets`, `vercel`, `netlify`) and **names-only** where it cannot (`github`, `cloudflare` are write-only by design) — those report `values_compared: false` end-to-end so "no drift" never reads as "values verified". Value-free throughout: `hmac.Equal` over keyed HMACs, never persisting digests or plaintext. Migration 000041, `JANUS_SYNC_VERIFY_TICK` (default off), manual `POST …/verify`, `sync.drift` notification, ops-console surfacing. | ~~M~~ |
| ~~**WebAuthn/passkeys** — second factor (and passwordless) for the UI login~~ **SHIPPED 2026-07-25 (PR #176)** — enrolment + single-step passkey sign-in + management (migration 000040). Uses `github.com/go-webauthn/webauthn` under a **recorded CLAUDE.md exception** (approved 2026-07-25, the second and final crypto-adjacent one); only public credential material is stored. Single-use expiring challenges; the account is taken from the challenge, never the client response; counter regression is fatal; `userVerification: required` on every ceremony, which is why a passkey sign-in is not additionally TOTP-gated; RP ID/origins from `JANUS_WEBAUTHN_*`, boot-validated, never the request Host; `login/begin` returns a stable decoy so it is not an account-existence oracle. **Passwordless/discoverable login deferred** (enrolled with `residentKey: preferred`, so adding conditional UI later needs no re-enrolment). | ~~M–L~~ |
| ~~**WebAuthn passwordless / discoverable login** — username-less sign-in with conditional UI (autofill)~~ **SHIPPED 2026-07-26 (PR #184)** — migration 000044. The identified flow takes the account from the challenge; discoverable login cannot, so identity comes from the **stored credential** and never the client-supplied `userHandle`: the `rawID` is looked up in our store, the account is that row's owner, the presented handle must equal the owner's (constant-time), and the library is handed only that owner's credential set — closing credential substitution, which is pinned by a dedicated test. A separate `login_discoverable` challenge pool stops challenges crossing ceremonies, and all failures are byte-identical 401s. New enrolments request `residentKey: required` and capture `credProps` (true/false/unknown, promoted on first successful passwordless assertion) surfaced as a **Passwordless** column, so an older non-discoverable passkey is visible rather than mysteriously failing. Conditional UI shipped. Found and fixed a real bug on the way: `createCredential()` was silently dropping the `extensions` field, so `credProps` never reached the browser. | ~~S–M~~ |
| ~~**Terraform provider: environment-scoped tokens + batch-secret resource**~~ **SHIPPED 2026-07-26 (PR #186)** — `janus_service_token` gains `scope_kind` (`config` default \| `environment`) beside the existing `scope` UUID, guarded by a hand-rolled `stringOneOf` validator so a bad value fails at **`plan`**, locally, rather than as a server 400 — and with no new dependency (the provider still needs only terraform-plugin-framework). The new **`janus_secrets`** resource sends every add, change and removal in an apply as ONE `PUT /v1/configs/{id}/secrets`, so Janus records **one config version** for the whole set — the unit of diff and rollback — instead of one per key; keys absent from the map are never touched, so it composes with per-key `janus_secret` on the same config. Drift detection is **honestly metadata-only**: the masked list is value-free, so the provider cannot compare stored plaintext and does not claim to, tracking each key's server-side `value_version` instead (a moved counter = an out-of-band write). Because the `secrets` map is `Sensitive` in full, plan output masks key *names* too — hence the companion non-sensitive `value_versions` map, without which an operator could not see which keys a plan touches. | ~~M~~ |
| ~~**SDK depth** — background auto-renew for dynamic leases, plus `Run`-style helpers in all three SDKs~~ **SHIPPED 2026-07-26 (PR #183)** — Go/TS/Python share one renewal policy (2/3 of remaining TTL, ±10% jitter, retries halving so they converge on expiry). Opt-in, idempotently stoppable with no timer/goroutine leaks, and terminal-vs-retryable errors surfaced rather than swallowed. `Run`-style helpers revoke on success, error and panic without masking the caller's error. The load-bearing detail: the server **clamps** a renewal to `MaxExpiresAt` and returns 200, so reaching the max TTL looks like a successful renew — the loop detects the ceiling both by comparing against `max_expires_at` and by noticing an expiry that no longer advances (the issue endpoint omits the field), then ends with a terminal error instead of renewing forever. Zero new dependencies in any SDK. | ~~M~~ |
| ~~**Web import wizard** — drive the `janus import` sources (Doppler / Vault KV / AWS SM) from the UI~~ **SHIPPED 2026-07-26 (PR #187)** — a format picker on the editor's existing Import flow: `env` (unchanged), **Doppler** (flat JSON), **Vault KV v2** (unwraps the `.data.data` envelope), **AWS Secrets Manager** (parses the JSON inside `SecretString`). Deliberately **paste-based**: unlike the CLI importer it never holds a Doppler/Vault/AWS credential and makes **no outbound call of its own**, so the wizard adds no CORS, SSRF or third-party-credential surface to the server — each format instead displays the exact source CLI command that produces the paste. Entries flow into the existing per-key preview (new/overwrite/invalid) and commit as one config version through the unchanged dirty-buffer path; parsing is pure client-side and unit-tested. | ~~M~~ |

### 8. Operational longevity

| Feature | Why | Effort |
|---|---|---|
| ~~**Audit retention with hash-chain checkpointing** — periodic signed checkpoints so verified-and-shipped prefixes can be archived/pruned without breaking `GET /v1/audit/verify`~~ **SHIPPED 2026-07-25 (PR #153)** — migration `000039_audit_checkpoints`; HMAC-SHA256 checkpoint MAC over length-prefixed `through_seq‖through_hash‖event_count` with a **domain-separated** key derived from the master-key-wrapped token-HMAC key (`internal/crypto` untouched, stdlib only); owner-only `audit:manage` endpoints `POST/GET /v1/audit/checkpoint` + `POST /v1/audit/prune`; verify validates the latest checkpoint MAC then walks from `through_seq+1` (forged checkpoint → `checkpoint_mac_invalid`, no genesis fallback); prune is fail-closed (needs valid checkpoint, clamps to the auditship high-water mark, never deletes anchors); audit-viewer checkpoint stamp + owner "Create checkpoint". | ~~M–L~~ |
| ~~**Secret value-version retention** — optional, owner-set "hard-destroy value versions older than N days/versions" policy~~ **SHIPPED 2026-07-25 (PR #181)** — migration 000043; owner-only `secret:prune`; `GET/PUT /v1/configs/{cid}/versions/retention` + `POST .../versions/prune`; `janus secrets retention|prune`; floors via `JANUS_SECRET_RETAIN_MIN_VERSIONS`/`_MIN_DAYS` plus a per-config override that can only retain **more**. The design point: pruning deletes whole **config versions** (their manifest entries follow the `config_version_id` cascade) and then garbage-collects `secret_values` no surviving entry references. Deleting value rows directly — the obvious reading of the task — would have triggered the `secret_values ON DELETE CASCADE` from 000005 and silently removed manifest entries, leaving old versions rollback-able to an *incomplete* config with no error. The invariant is re-checked inside the transaction and a shortfall aborts the prune. `dry_run` defaults true (it performs the work and rolls back, so the preview is exact); pending edit requests block the config, pending-promotion sources are pinned, soft-deleted configs are never touched; no background sweep by design. | ~~M~~ |
| ~~**Grafana dashboard JSON + example alert rules** shipped in `docs/`~~ **SHIPPED 2026-07-25 (PR #180)** — `deploy/grafana/janus-overview.json` (24 panels: seal state first, HTTP rate/latency/5xx, per-engine scheduler tick age, rotation/sync failures, dynamic leases, DB pool saturation, audit head slope, Go runtime), `alerts.yaml` (13 Prometheus-format rules with `for:` durations and operator-facing annotations), and a README with token setup, `scrape_configs`, and a `ServiceMonitor`. Every expression was evaluated against a live Prometheus scraping a real instance (23/23 non-empty) and the dashboard imported into Grafana 13.1.0 — so the panels are known to render, not merely well-formed. Found that three counter-named series are actually gauges (`audit_head_seq`, `rotation_runs_failed`, `sync_runs_failed`), so they use `deriv()`/`offset` rather than `rate()`. | ~~S~~ |

### 9. Test depth

| Feature | Why | Effort |
|---|---|---|
| ~~**Playwright smoke suite** — a browser E2E pass (init → unseal → login → create project → save secret → audited reveal) against the docker stack~~ **SHIPPED 2026-07-25 (PR #151)** — `web/tests/e2e/smoke.spec.ts` (8 ordered steps incl. Shamir 5/3 init + unseal quorum + audited reveal + chain-verified badge), `playwright.config.ts` (`JANUS_E2E_BASE_URL`, default `:8210`), opt-in `.github/workflows/e2e.yml` (`workflow_dispatch`/`e2e` label; not in per-PR CI), additive `data-testid`s only. Full run needs the live docker stack. | ~~M~~ |
| ~~**Go fuzz tests** — native fuzzing for the reference parser (`${…}`), `.env`/properties importers, PEM sniffing, RESP encoding, federation JWT claims~~ **SHIPPED 2026-07-25** — 8 `Fuzz*` targets that assert **security invariants** rather than mere absence of panics: filename-safety of secret keys (the `download --format files` traversal guard), RESP encode→decode round-trip (Redis command injection), the `${…}` reference parser (attacker-reachable via any secret value), the transit ciphertext envelope, and CI-federation claim projection/matching. The federation target immediately found a latent fail-open (`claimsSatisfy` treated an absent claim as satisfying an empty required value) — not exploitable, since `CreateFederationBinding` rejects empty values and is the only write path, but hardened so the matcher is safe independently. `.env`/properties and PEM sniffing turned out to be **client-side TypeScript** with no Go parser, so no Go target applies. | ~~S–M~~ |

---

## Release & distribution

_The binary + container ship automatically on a `v*` tag (goreleaser → GitHub
Release + GHCR). The language-package registries do **not** — each needs an
account/credential the maintainer must supply, so they are tracked separately._

- [x] ~~**v0.2.0 — the post-1.0 release**~~ **TAGGED 2026-07-26** — everything
      built after v0.1.0 ships in one release (passkeys incl. passwordless, k8s
      service-account federation, sync drift detection, value-version retention,
      audit checkpointing, Grafana, SDK lease auto-renew, the Terraform batch
      resource, the web import wizard, the mobile/tablet layout, plus the Trash
      destroy / passkey-enrolment-logout / expired-session fixes). It also
      reclaims `ghcr.io/steveokay/janus:latest` from the `v0.1.1-rc1`
      prerelease that had taken it — `latest` carries `skip_push: auto`, so a
      stable tag repoints it automatically.
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
- [x] ~~**Post-1.0 white-box audit remediation**~~ **SHIPPED 2026-07-25**
      (PR #149, merged to `main`; details in the internal audit tracker) — 5-agent white-box audit found 0 CRITICAL. The lone **HIGH**
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
      in the provider module). The provider is otherwise feature-complete — the
      previously-deferred env-scoped tokens and batch-secret resource shipped
      2026-07-26 (PR #186), leaving only the credential-gated publish.

---

## Post-v0.2.0 — work generated by using the product

Both roadmaps are exhausted, so new work now comes from **using** Janus rather
than from a plan. Everything below originated in two bugs the maintainer hit in
normal use and one incident during development.

**Shipped 2026-07-26/27:** owner disaster recovery (`janus admin
reset-password`, PR #198) — losing the last owner password previously meant
destroying the instance; `janus doctor` (PR #200) — 19 preflight checks for
configuration that parses and is still wrong; browser E2E over the destructive
and security screens (PR #199) — 26 tests over Trash, tokens, members,
four-eyes approvals and break-glass; and a fix for `janus master-key rekey`
being unable to read a piped quorum (PR #198).

**Found by that new E2E coverage.** The first is fixed: the secret editor read
the protected (four-eyes) flag from the registry cache without waiting for
hydration, so a deep link — a cold load — rendered a protected config as
unprotected, down to a Save button reading "Save as vN". The server-side control
always held, so nothing was exploitable; the UI misreported it. Fixed in PR #202
by reading the flag from the server, and pinned by a cold-context deep-link E2E
test. The other three — the OIDC-status probe sharing the password rate limiter,
the config-delete redirect, and a duplicate `aria-label` on the approvals table —
were all found **already fixed** when the tracker was verified against the code
on 2026-07-28; they had been repaired in passing without being checked off.
Detail and reasoning in [`../status.md`](../status.md).

**Found by deploying to a real cluster (2026-07-28/29).** The Helm chart had
never been deployed nor covered by CI; running it on minikube surfaced three
defects, all fixed in PR #217 (the API Service also selected the bundled
Postgres pod; an invalid or incomplete seal config rendered silently; the chart
had no CI at all). Exercising the two Kubernetes integrations then showed that
the chart's own SSRF hardening broke both: `JANUS_OUTBOUND_BLOCK_PRIVATE=true`
also blocks `kubernetes.default.svc`, a ClusterIP in a private range, so the
sync provider failed with a sanitized `apply failed` and service-account
federation with a generic 401.

**The allowlist that answers it shipped 2026-07-29.** `JANUS_OUTBOUND_ALLOW`
(chart: `env.outboundAllow`) exempts named IPs/CIDRs from that tightening, so an
operator keeps the SSRF control on *and* reaches the API server, instead of
being told to turn the control off. The load-bearing rule is that it exempts the
private-space block and **nothing else** — the link-local / cloud-metadata
ranges can never be allowlisted, and an entry naming one fails at boot rather
than looking effective. Entries are addresses, never hostnames, because the
guard validates the *resolved* IP and trusting a name would reopen DNS
rebinding. Still open from the same exercise: federation cannot verify an issuer
whose certificate is signed by the cluster CA (there is no per-issuer
`ca_cert`, though the sync provider accepts one). Detail in
[`../status.md`](../status.md).

## RBAC at organisation scale

Raised 2026-07-27. **Item 1 (groups) shipped 2026-07-27**; items 2 and 3 remain. The target shape is an
organisation with many product teams, each owning some projects and unable to
see each other's secrets, with instance owner/admin seeing everything.

Janus already does the **visibility** half: project/trash/token lists filter per
item, so a fresh account with no bindings genuinely sees nothing. What is missing
is what makes that arrangement manageable, delegable and auditable beyond a
handful of people. None of it is a security hole — the server denies correctly
today — it is the gap between *correct* and *usable by an org*.

**Do these three, in this order:**

1. ~~**Group-based role bindings from OIDC group claims.**~~ **SHIPPED
   2026-07-27** — migration 000045. A binding may target a **group** instead of
   a user, so "Team Payments owns these projects" is one row per project rather
   than one per person per project. Two kinds, never both: `oidc` (membership
   is a snapshot refreshed from the IdP's group claim at each login, and an
   admin can never hand-add a member) and `local` (an explicit list, for
   instances with no IdP and for password logins). Keeping them distinct is
   what lets us state and hold *access granted via an IdP group is fully
   described by the IdP* — a hybrid group would make an access review against
   Entra return a clean result that is not true, and would turn an IdP outage
   into a permanent invisible grant, since a login sync only ever clears rows
   it owns. The engine gained `WithGroups`, mirroring `WithGrants`: group
   bindings arrive as ordinary `RoleBinding`s (stamped `ViaGroupID`), so
   `userAllows`/`bindingApplies`/`BoundRole` gained **no new concepts** and the
   union rule stayed single — and it fails closed, since a group-store error
   denies rather than resolving on direct bindings alone. A group binding tops
   out at **admin**: owner rotates the master key, prunes the audit chain and
   destroys secret history, so it stays a deliberate direct binding — which
   also means `CountInstanceOwners` needed no change and an IdP outage can
   never strand the instance. Two separate authorities: the catalog is
   instance-scoped `group:manage`, while *binding* a group is `member:manage`
   at that scope under the same `BoundRole` cap as `memberPut` (without it,
   groups would be a way around M-1). The claim resolver distinguishes "in no
   groups" (clear the snapshot) from "this token cannot tell us" — Entra swaps
   the claim for a Graph pointer past ~200 groups, and reading that as empty
   would clear every membership and look exactly like a legitimate removal from
   all of them. `/groups` screen, a Groups section on Members, `janus group`,
   and a [guide](guides/groups.md).
2. ~~**Delegated project creation.**~~ **SHIPPED 2026-07-27** — migration
   `000047`. A group may be marked `can_create_projects`; a member creating a
   project binds it to the group at admin and themselves at owner, with no
   instance-wide read. Self-serve and isolation are no longer mutually
   exclusive. Implemented as a capability rather than a role, because any role
   granting `project:create` at instance scope also grants `project:read`
   there — the exact leak being closed.
3. ~~**Scoped audit read.**~~ **SHIPPED 2026-07-27** — migration `000048`.
   `audit:read` honoured at project scope, with the restriction applied in the
   query so keyset paging cannot truncate a trail. The scope column stays
   OUTSIDE the chain hash (a hashed field would break `audit/verify` for every
   existing event), which makes it an index rather than evidence — recorded as
   an explicit non-defense in the threat model. `verify` stays instance-only,
   since a subset of a hash chain cannot be verified. **With this, all three
   org-scale items are done and the isolation story has no remaining leak.**

**Found by deploying the Helm chart for the first time (2026-07-28), all
fixed:** the chart shipped in #165 with no CI and had never actually been
deployed. Its API Service selected on name+instance only, which the bundled
evaluation Postgres also carries — so `kubectl port-forward svc/janus`, the
command its own NOTES.txt gives for the **unseal** step, picked the database pod
and failed. API traffic was never mis-routed (Postgres joined as a port-less
endpoint) but sat one label away. An invalid `seal.type`, and the chart's own
defaults (`awskms` with an empty key), also rendered a pod that could never
unseal. Fixed by pinning `component: server` on the Service selector, validating
the seal config at template time, and adding the `helm` CI job that was missing
entirely. Details in `status.md`.

**Raised by building groups (2026-07-27), tracked in `status.md`:** Members
reported group-derived access as *no access* — **fixed the same day** by
resolving it server-side (`derived_members` on the scope's group-binding list)
and showing the effective role with a direct/via-group source column. Note the
item as first written claimed an "RBAC matrix" still plotted users only; **there
is no matrix** — it was React-era and the Atrium rewrite dropped it, so
rebuilding a users × scopes grid remains genuinely open. Also: an OIDC group's member list
covers only users who have signed in, since membership is a login snapshot;
Entra's ~200-group overage leaving a retained snapshot stale with no time bound
**was fixed 2026-07-28** (`JANUS_OIDC_GROUP_MAX_AGE`, migration 000050 — a
generic maximum snapshot age rather than an Entra-specific Graph fetch; local
membership never expires); the static nav showing Groups to accounts that cannot
use it **was fixed 2026-07-28** together with its root cause — `GET
/v1/auth/me` now reports effective permissions and the shell renders from them,
so the rail, the command palette and the `g`-chords all hide what an account
cannot use (see below); and neither the Terraform provider nor the SDKs can
manage groups.

**Permission-gated navigation shipped 2026-07-28.** `/v1/auth/me` gained a
`permissions` object and the UI stopped being a static list. It is a **hint** —
the server authorizes every request and a hidden screen is still reachable by
URL — so what it buys is that a non-admin no longer discovers their permissions
by collecting 403s. The design decision worth keeping: permissions are reported
as **two sets**, `instance` and `anywhere`, because a project viewer holds
`transit:read` inside their project while the transit endpoints authorize at
instance scope; one flat list would have shown Transit and then refused it.
`authz.Effective` resolves bindings once and reuses the same predicates `Can`
does, so the hint cannot drift from the decision. Detail in
[`../status.md`](../status.md) and
[members-and-rbac.md](guides/members-and-rbac.md).

**Environment-scoped four-eyes shipped 2026-07-27** (migration `000046`), which
is what makes the no-deny-rules decision defensible: `require_approval` was per
config and defaulted to off, so a config created in production started
unprotected and the "a broad grant is fine, because prod writes are four-eyes"
argument rested on a control nobody had enabled. Protection is now also a
property of the environment, unioned with the config's own flag.

Also tracked, lower priority: exposing effective permissions to the UI so the
nav can gate instead of collecting 403s; the fact that a binding can never
*narrow* another one (better answered by defaulting prod to `require_approval`
than by deny rules, which would cost the engine its clarity); `secret:read`
being all-or-nothing per config; and a per-user "effective access" view for
offboarding. Detail and reasoning in [`../status.md`](../status.md).

## What's actually left

**Both roadmaps are now exhausted.** Sections 1–5 (the original) closed before
v0.1.0; sections 6–9 (post-1.0, added 2026-07-24) closed out across
2026-07-25/26. The "Trust & Longevity" batch all shipped — trust & supply-chain
sweep (6.1–6.4), `janus run --watch` + `render` (7.1–7.2, PR #152), audit
checkpointing + retention (8.1, PR #153), Playwright smoke (9.1, PR #151) — and
so did everything queued behind it: k8s SA federation (7.3, PR #174), sync drift
detection (7.4, PR #175), passkeys (7.5, PR #176) and discoverable login
(PR #184), value-version retention (8.2, PR #181), the Grafana dashboard (8.3,
PR #180), fuzzing (9.2), SDK depth (PR #183), the Terraform batch resource +
env-scoped tokens (PR #186), and the web import wizard (PR #187).

The mobile/tablet layout (5.6) shipped 2026-07-26 (PR #192) — the last open
**build** item. **Everything still open is blocked on a maintainer credential,
not on code:**

| # | Item | Blocked on |
|---|---|---|
| 1 | Publish the TypeScript SDK to npm | npm account + automation token |
| 2 | Publish the Python SDK to PyPI | PyPI project + Trusted Publishing |
| 3 | Publish the Terraform provider to the Registry | GPG signing key + Registry account |

Each is a short CI job once the credential exists; everything they would publish
is already written, tested, and — as of 2026-07-26 — covered by CI (`go-modules`,
`sdk-ts`, `sdk-python` jobs; before that, `go test ./...` never descended into
the nested modules and five of Dependabot's seven watched directories had no
test run). **There is no outstanding engineering work on the roadmap** — further
items should come from real usage, not be invented to keep the list populated.
