# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
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

### Security
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
  create project → save secret → audited reveal → chain-verified badge.

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
