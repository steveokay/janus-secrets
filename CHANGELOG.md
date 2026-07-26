# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **The web UI works on a phone and a tablet.** The SPA previously had no shell
  breakpoint at all: the 236px nav took most of a phone screen and the rest was
  *clipped rather than scrolled*, so the page heading, the top bar and every
  ledger column were cut off with no way to reach them. Below 1024px the nav is
  now an off-canvas drawer (scrim, `Esc`, closes on navigation, and out of the
  tab order while shut); 1024 keeps the sidebar for tablet landscape. Wide
  ledgers scroll horizontally within their sheet rather than being cut off,
  page headers wrap instead of squeezing the title to one word per line, and the
  Audit result filter no longer pushes its `denied` segment off-screen where it
  could not be reached at all. Desktop layout is unchanged — though it also
  loses a stray horizontal scrollbar the dashboard ornament had been causing at
  every width.
- **Kubernetes service-account federation + multi-issuer trust.** The federation
  trust anchor is now a *set* of issuers (`/v1/sys/oidc/federation/issuers`), so a
  CI provider and a Kubernetes cluster can be trusted at the same time; every
  trust binding is pinned to one issuer, and the verifier is chosen by the
  token's own `iss` (signature checked against that issuer's JWKS), so a token
  from one issuer can never satisfy a binding written for another. Nested JWT
  claims are matched by dotted path (`kubernetes.io.serviceaccount.name`) with
  non-string values still dropped rather than coerced and any path collision
  rejecting the token. A `kubernetes` preset forces bindings to pin
  service-account identity (`sub`, or namespace + service-account name).
  The pre-existing single issuer and its bindings are preserved on upgrade.
- **WebAuthn passkeys for UI login.** Enrol per-device passkeys from
  **Settings → Passkeys** and sign in with one in a single step — no password,
  no TOTP code. Two ceremonies, in separate challenge pools: **email-identified**
  (the account comes from the challenge) and **passwordless/discoverable** (no
  address typed; the account is resolved from the presented credential id in
  Janus's own store, and the assertion's `userHandle` is only cross-checked
  against that owner, never used to select the account — plus conditional-
  mediation autofill where the browser supports it). Enrolment sets
  `residentKey: required` so new passkeys always work passwordlessly, and records
  the `credProps.rk` hint so Settings can report per-credential discoverability.
  `userVerification` is required on **every** ceremony, challenges are single-use
  and expiring, the signature counter is enforced as a compare-and-swap, and both
  login-begin endpoints answer identically for real, absent, and disabled
  accounts. Only public credential material is stored, so master-key rotation has
  nothing to re-wrap. Off until an operator sets `JANUS_WEBAUTHN_RP_ID` /
  `JANUS_WEBAUTHN_ORIGINS` (validated at boot, never derived from the request
  `Host`); removing the last passkey can never lock a user out. Routes under
  `/v1/auth/webauthn/*`; migrations 000040 and 000044. See
  [docs/guides/passkeys.md](docs/guides/passkeys.md).
- **Sync drift detection.** Sync was one-way: push and forget. A verification
  pass now reads the destination back and reports `missing` / `modified` /
  `extra` / `unreadable` keys — `POST /v1/sync/targets/{id}/verify`,
  `GET .../verify-runs`, `PATCH .../verify-schedule`, and `janus sync verify` /
  `verify-schedule`. Every one of the eight providers declares a read
  capability: six compare **values** (`k8s`, `gitlab`, `aws_ssm`,
  `aws_secrets`, `vercel`, `netlify`), while `github` and `cloudflare` are
  write-only at the API and so report **`names_only`** — a clean result there
  means the key names line up, not that the values match, and the API says so.
  Comparison is value-free: remote values are HMACed under the existing
  sync-fingerprint subkey and compared in constant time, never persisted or
  logged. Extras count as drift only when the target prunes. The scheduler is
  **off by default** (`JANUS_SYNC_VERIFY_TICK`, unset/`0`) because verifying
  reads values back out of external systems; manual verification always works.
  Migration 000041.
- **Secret value-version retention.** Nothing had ever removed a
  `secret_values` row, so a long-lived instance kept every value a key had ever
  held. Pruning is now explicit, audited, and done at **config-version**
  granularity (the unit of diff and rollback), so every retained version stays
  fully restorable — unreferenced value rows are garbage-collected afterwards,
  and the prune transaction re-asserts the invariant. `GET/PUT
  /v1/configs/{cid}/versions/retention` and `POST .../versions/prune`, plus
  `janus secrets retention get|set|clear` and `janus secrets prune` (with
  `--dry-run`). Gated by the new **owner-only** `secret:prune` action — the only
  operation in Janus that destroys secret history irreversibly. An instance-wide
  floor (`JANUS_SECRET_RETAIN_MIN_VERSIONS` / `JANUS_SECRET_RETAIN_MIN_DAYS`,
  both off by default) and an optional per-config override can only ever retain
  **more**, never less. Migration 000043.
- **Kubernetes service-account federation + multi-issuer trust.** The federation
  trust anchor is now a *set* of issuers (`/v1/sys/oidc/federation/issuers`), so a
  CI provider and a Kubernetes cluster can be trusted at the same time; every
  trust binding is pinned to one issuer, and the verifier is chosen by the
  token's own `iss` (signature checked against that issuer's JWKS), so a token
  from one issuer can never satisfy a binding written for another. Nested JWT
  claims are matched by dotted path (`kubernetes.io.serviceaccount.name`) with
  non-string values still dropped rather than coerced and any path collision
  rejecting the token. A `kubernetes` preset forces bindings to pin
  service-account identity (`sub`, or namespace + service-account name), joining
  the existing `github` / `gitlab` / `buildkite` / `circleci` presets.
  The pre-existing single issuer and its bindings are preserved on upgrade.
  Migration 000042.
- **Grafana dashboard + example alert rules** (`deploy/grafana/`): an import-ready
  `janus-overview.json` board and 13 Prometheus alerting rules in `alerts.yaml`
  covering sealed/down/absent, restart loops, error rate and latency, stalled
  schedulers, rotation & sync failure trends, unreapable dynamic leases, DB-pool
  exhaustion, a stalled audit head, and goroutine growth. Built only from metrics
  the server actually exports; `/metrics` still requires `JANUS_METRICS_TOKEN`.
- **SDK background lease auto-renew + `Run`-style helpers** across all three
  client SDKs: a lease renewer that keeps a dynamic credential alive in the
  background (fractional renew-ahead with jitter, a minimum interval, and
  typed stop reasons for max-TTL / lease-gone / unauthorized / rejected), plus a
  helper that issues a lease, holds it renewed for the duration of your callback,
  and revokes it on the way out — `janus.RunWithDynamic` (Go),
  `LeaseRenewer` / `withDynamic` (TypeScript), `LeaseRenewer` (Python).
- **Terraform: environment-scoped service tokens and a `janus_secrets` batch
  resource.** `janus_service_token` now accepts `environment` as well as `config`
  scope (the only two kinds Janus mints), and the new `janus_secrets` resource
  manages a whole key/value map through the batch write endpoint, so an apply
  touching N keys creates exactly **one** config version instead of N.
- **Paste-based import wizard in the web UI.** The secret editor's importer now
  recognises **Doppler**, **Vault KV v2**, and **AWS Secrets Manager** CLI export
  output alongside `.env` / Java `.properties`, with format auto-detection and an
  override. Deliberately **paste-based**: it adds no server endpoint, holds no
  third-party credential, and makes no outbound call — you run the export command
  yourself and paste the result. Leaf-flattening semantics mirror the `janus
  import` CLI sources.
- **`janus run --watch`** — supervise the child and gracefully restart it
  (SIGTERM → grace → Kill) when the bound config's version bumps, re-fetching
  secrets; poll cadence via `--watch-interval` (default `10s`).
- **`janus render`** — fill a Go `text/template` with a config's secrets and
  write it to a file (atomic, `0600`); `--watch` re-renders on
  version bumps.
- **Audit hash-chain checkpointing + retention** — owner-only
  (`audit:manage`) `POST/GET /v1/audit/checkpoint` and `POST /v1/audit/prune`.
  Signed (domain-separated HMAC) checkpoints let `verify` anchor on a trusted
  checkpoint and walk forward, so verified-and-shipped prefixes can be pruned;
  prune is fail-closed and clamped to the audit-ship high-water mark. An
  owner-only "create checkpoint" affordance is surfaced in the audit viewer.

### Fixed
- **Destroying a config or environment from the Trash always failed.** Both
  delete handlers resolved the authorization scope through a live-only read, but
  everything reachable from the Trash is by definition already soft-deleted — so
  the lookup returned `404` before the permission check ever ran, and the UI
  reported "Action failed" even for an owner. The restore handlers had always
  done this correctly; only the delete side was missed. Projects were unaffected,
  which is why it presented as config-specific. Also fixes the parent chain:
  soft-delete does not cascade, so a config whose *environment* was separately
  deleted was listed in the Trash yet could be neither restored nor destroyed.
  Authorization is unchanged in strength — only finding the row changed.
- **A failed passkey enrolment silently signed the user out.** The 401
  session-expiry handling keyed off the HTTP status alone, but enrolling a
  passkey is done by an already-authenticated user and a ceremony that fails to
  verify (wrong RP origin, a cancelled prompt, a cloned authenticator) answers
  `401`. The client now reads the error *code*, which the server already
  distinguishes, and treats only `session_expired` / `unauthenticated` as a lost
  session.
- **An expired session reported the wrong thing entirely.** Every request
  returned `401` while the shell still looked signed in, so the failure surfaced
  as a feature-level message ("Could not create the project.") that pointed
  nowhere near the cause. The UI now returns to the login gate — except on a
  wrong password, a failed passkey assertion, or the probe used to *test*
  authentication.
- **The secret editor's value field collapsed to a sliver.** The row's action
  cluster is `nowrap` and has gained a button per feature, so under the table's
  auto layout the value column absorbed the entire shortfall.
- **Unmatched `/v1/` paths returned `200` + the SPA's `index.html`.** The SPA
  fallback answered every unmatched route, so a typo'd or removed endpoint
  replied with HTML — breaking the documented error envelope and making SDKs
  parse HTML as JSON. API paths now get the standard JSON `404` (and a JSON
  `405` where chi previously sent a bodiless one), while client-side deep links
  still fall through to the SPA.
- **Clearer validation errors.** A malformed batch-secret-write body no longer
  reports "at least one change is required" (it says the body is invalid JSON),
  and "invalid scope kind" now names the two kinds a service token may use.
- **`janus --config <uuid>` was unusable.** The CLI insisted on
  project + environment + config and resolved slugs through project-level reads
  that a config-scoped token — the narrowest, CI-recommended credential —
  legitimately lacks. A config given as a UUID now short-circuits resolution.
- **`event_count` is a lifetime total.** It is carried forward across checkpoints
  and prunes rather than silently becoming a retained-row count, so the number
  `verify` reports stays comparable after a prune.
- **A prerelease no longer hijacks the `latest` image tag.** `docker_manifests`
  pushed `ghcr.io/steveokay/janus:latest` unconditionally, so an `-rc` tag became
  `latest`; `skip_push: auto` now omits the manifest for prerelease tags, matching
  the `release.prerelease: auto` behaviour already applied to the GitHub Release.

### Security
- **The SSRF guard could be defeated by an HTTP proxy.** With `HTTP_PROXY` /
  `HTTPS_PROXY` / `ALL_PROXY` set, the hardened dialer only ever saw the
  *proxy's* address, so the link-local / cloud-metadata block silently stopped
  applying to the real destination. Proxying is now **off by default** on the
  guarded clients; `JANUS_OUTBOUND_ALLOW_PROXY` re-enables it for deployments
  whose only egress path is a proxy, logging a startup warning (naming only the
  variables that are set — a proxy URL can embed credentials) and applying a
  partial URL-time host check.
- **Audit checkpoints verify the chain before signing.** Signing a tampered head
  would have laundered a detectable compromise into an undetectable one. Both
  `POST /v1/audit/checkpoint` and `POST /v1/audit/prune` now fail closed with
  `chain does not verify` unless the chain validates at that moment.
- **Prune's retention guards.** The "never prune un-shipped events" ceiling is
  now bound to **shipping history** (the durable high-water mark) rather than to
  whether the shipper happens to be wired into the running process, so removing
  `JANUS_AUDIT_SHIP_*` from a deployment that has shipped cannot quietly drop the
  protection. An optional minimum-retention floor was added on top —
  `JANUS_AUDIT_RETAIN_MIN_DAYS` and `JANUS_AUDIT_RETAIN_MIN_EVENTS`, both off by
  default, combined by taking the strictest ceiling. Unreadable state aborts the
  prune rather than pruning unguarded.
- **Systemic SSRF hardening** (`internal/nethard`): a shared hardened dialer
  re-checks the resolved IP on every dial (defeating DNS-rebinding), blocks
  link-local/cloud-metadata ranges unconditionally (optional private-range block
  via `JANUS_OUTBOUND_BLOCK_PRIVATE`), and caps redirect hops/scheme — applied to
  every operator-configured outbound caller (rotation, sync, notifications, and
  OIDC discovery/JWKS).
- **Trust & supply chain:** `SECURITY.md` disclosure policy (GitHub private
  vulnerability reporting) and `docs/threat-model.md`; releases now cosign
  keyless-signed with syft SBOMs and SLSA build-provenance attestations; a
  `.github/dependabot.yml` keeps Go/npm/pip/Actions dependencies current.
- White-box audit remediation: TOTP codes are no longer replayable; a password
  change revokes other sessions and rotates the caller's own cookie; a
  break-glass-elevated user can no longer mint a durable binding at the elevated
  role; uniform security headers on `/v1/` responses.

### Tests
- Playwright browser smoke suite (`web/tests/e2e/`): init → unseal → login →
  create project → save secret → audited reveal → chain-verified badge — since
  extended to cover the passkey ceremonies and the paste-based import wizard.
- **Go fuzz targets over the hostile-input parsers** — eight in total, across
  `internal/transit` (envelope parse + round-trip), `internal/resolve` (reference
  segment parsing), `internal/secrets` (key validation), `internal/rotation`
  (RESP encoding, asserting no command injection), and `internal/auth` (JWT
  string-claim extraction, nested-claim flattening, and fail-closed claim
  matching).
- **Schema/enum drift guard** — a test introspects the live database's `CHECK`
  constraints and asserts they accept exactly the values the code's registries
  produce, so a rotator, sync provider, or notification channel can never again
  exist in Go while being impossible to persist.
- **WebAuthn browser ceremony verified end to end** with Chrome's CDP virtual
  authenticator, so registration and both sign-in paths are exercised by a real
  browser rather than only by a synthetic authenticator in Go.

## [0.1.0] - 2026-07-24

First tagged release. Feature-complete across build Phases 1–3, plus the UI
depth, additional auth/notification/integration work, and the pre-release
security hardening that followed. (Nothing was tagged before this; everything on
`main` ships as 0.1.0.)

### Added
- **Core (Phase 1):** envelope-encryption key hierarchy with Shamir and cloud-KMS
  unseal; PostgreSQL store + migrations; Project → Environment → Config → Secret
  model with two-level (config + per-key) versioning, soft-delete/restore, config
  inheritance, and cross-config secret references; password + service-token auth;
  RBAC (viewer/developer/admin/owner); hash-chained audit log; REST API; `janus`
  CLI with `run` secret injection.
- **Transit + UI (Phase 2):** transit engine (encrypt/decrypt/sign/verify/rewrap,
  key versioning); the Svelte 5 **"Atrium"** SPA (banknote-engraving /
  archival-ledger aesthetic, `daylight`/`nightwatch` themes, hand-written CSS
  tokens — no Tailwind) covering init/unseal/login, projects → envs → configs,
  the secret editor, promotion + approvals, audit ledger, token/member
  management, transit, operations, an integrations hub, trash, and settings, with
  a command palette; OIDC login and CI federation (GitHub Actions, GitLab,
  Buildkite, CircleCI); reads-24h usage metrics.
- **Rotation + dynamic (Phase 3):** scheduled static rotation (Postgres, webhook,
  MySQL, Redis, plus external-credential-generating OAuth and AWS IAM rotators);
  sync integrations to eight providers (GitHub Actions, Kubernetes, GitLab CI,
  Cloudflare, Vercel, Netlify, AWS SSM, AWS Secrets Manager); dynamic Postgres
  credentials with a TTL/renewal/revocation lease manager.
- **Hardening & depth:** project-KEK and master-key rotation; cursor pagination;
  Idempotency-Key middleware; HTTP timeouts/body caps; trash/restore, per-key
  history, and audit expand/timeline UI; a self-sufficient CLI control plane
  (project/env/config CRUD, token mint/list/revoke, whoami, completion, diff);
  break-glass emergency role elevation; protected configs (require-approval /
  four-eyes edit requests); env→env promotion with an approval workflow and
  locked keys.
- **Auth & accounts:** two-factor authentication (TOTP + single-use recovery
  codes); progressive account lockout; self-service session management; per-token
  IP allowlists.
- **Editor & search:** bulk import from `.env` / Java `.properties` with preview;
  confirm-gated **Download .env** export; typed secrets
  (value/password/json/ssh_key/certificate/note); a client-side value generator;
  filename-style secret keys; global secret-key-name search from the command
  palette; environment rename on the project board.
- **Notifications & observability:** outbound webhook / Slack / SMTP alerting on
  rotation & sync failures, denials, and pending approvals; a Prometheus
  `/metrics` endpoint and an admin health panel; audit shipping to a
  webhook/syslog SIEM; configurable log level/format.
- **Integrations & SDKs:** Go, TypeScript, and Python client SDKs and a Terraform
  provider; inbound importers for Doppler, Vault, and AWS Secrets Manager;
  scheduled encrypted S3-compatible backups with retention.
- **Release:** Apache-2.0 license; OpenAPI 3.1 spec; goreleaser multi-arch
  binaries + GHCR image; production deployment guide.

### Security
- Closed two require-approval (four-eyes) bypasses where **rollback** and
  **promote-apply** committed directly to protected configs; both now route the
  resulting changeset through the edit-request approval flow. Made edit-request
  approval **claim-before-commit** to prevent a double-commit race under
  concurrent approvers, added **GitLab sync project-id validation** (request-URL
  injection guard), and protected pending edit requests from **KEK-version
  retirement** during project-KEK rotation.

[Unreleased]: https://github.com/steveokay/janus-secrets/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/steveokay/janus-secrets/releases/tag/v0.1.0
