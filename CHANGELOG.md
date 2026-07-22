# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed
- Replaced the React/Nocturne web SPA with a new Svelte 5 "Atrium" SPA
  (banknote-engraving / archival-ledger aesthetic, `daylight`/`nightwatch`
  themes, hand-written CSS tokens — no Tailwind, no React). Covers the same
  full API surface (init/unseal/login, projects → envs → configs, secret
  editor, promotion + approvals, audit ledger, tokens, members, transit,
  operations, integrations, trash, settings, command palette).

- Secret editor: bulk import from `.env` / Java `.properties` (paste or file
  picker, local parsing, preview with per-key new/overwrite/invalid
  selection, staged into the dirty buffer as one config version) and a
  confirm-gated **Download .env** export (audited per-key reveal, sorted and
  quoted output, filename-style keys skipped with a comment, mirroring the
  CLI). Environment rename on the project board (display name only; slug
  stays immutable).

### Added
- Two-factor authentication (TOTP): optional RFC 6238 second factor for
  password logins, with single-use recovery codes. Self-service enrolment from
  Settings (scannable QR + copyable secret, both shown once), confirm/disable/
  regenerate, and a login gate (`401 totp_required`) honoured by the web UI and
  `janus login`. The 160-bit secret is master-key-wrapped (bound to the user id,
  re-wrapped by master-key rotation); recovery codes are HMAC-hashed and
  single-use; codes/secrets never touch logs or the audit log. New
  `/v1/auth/totp/*` routes; migration 000025.
- Account lockout / progressive backoff: after repeated failed password logins
  an account is locked for an escalating, auto-expiring window
  (`JANUS_LOCKOUT_*`, default 5 failures → `1m→5m→25m→1h`), complementing the
  per-IP login rate limit. The lock is revealed only to a caller with the
  correct password (`429 account_locked` + `Retry-After`); a wrong password
  returns the byte-identical `invalid_credentials`, so account existence is not
  leaked, and attempts while locked never extend the window. Admins clear a lock
  via `POST /v1/users/{id}/unlock` (`user:manage`) or the Members page;
  migration 000026.
- Notifications (outbound alerting): configurable **webhook** and **Slack**
  channels that subscribe to `rotation.failed`, `sync.failed`,
  `promotion.pending`, and `access.denied` events. A crash-safe dispatcher
  (`JANUS_NOTIFY_TICK`, default 30s) tails the value-free audit log from a
  persisted cursor and fans matching events into a delivery outbox, retrying
  with exponential backoff — so alerts are never lost and can never carry a
  secret value. Destination URL + optional webhook HMAC signing key are
  write-only (master-key-wrapped, re-wrapped by master-key rotation, excluded
  from backups). New `notification:manage` RBAC action (admin/owner),
  `/v1/notifications/channels` REST surface (+ test + delivery history),
  `janus notifications` CLI, and a **Notifications** web screen. Migration
  000024.
- Session management (self-service): `GET /v1/auth/sessions` lists your active
  sessions with non-secret client metadata (IP, user-agent, last-seen) and a
  current-session marker; `DELETE /v1/auth/sessions/{id}` revokes one and
  `DELETE /v1/auth/sessions` signs out everywhere else. Sessions now record the
  client IP and user-agent at login (migration 000023). Surfaced in the web UI
  under **Settings → Active sessions** and via the `janus session list/revoke`
  CLI. No credential material is ever returned; a caller can only see and revoke
  their own sessions.
- Typed secrets (value/password/json/ssh_key/certificate/note): per-type editor,
  validation, and generate; type carried through promotion and clone; CLI
  `secrets set --type`.
- Filename-style secret keys (e.g. `foo.bar.txt`) — flat charset
  `[A-Za-z0-9._-]`; `janus run` and `.env` export skip keys that aren't valid
  env-var names (with a warning); new `janus secrets download --format files
  --output <dir>` materializes each secret to a file (traversal-guarded,
  requires `--plain`). Note: dotted keys can't be `${...}`-referenced and are
  skipped (not synced) by the GitHub Actions integration.

## [0.1.0] - 2026-07-16

First tagged release. Feature-complete across build Phases 1–3.

### Added
- **Core (Phase 1):** envelope-encryption key hierarchy with Shamir and cloud-KMS
  unseal; PostgreSQL store + migrations; Project → Environment → Config → Secret
  model with two-level (config + per-key) versioning, soft-delete/restore, config
  inheritance, and cross-config secret references; password + service-token auth;
  RBAC (viewer/developer/admin/owner); hash-chained audit log; REST API; `janus`
  CLI with `run` secret injection.
- **Transit + UI (Phase 2):** transit engine (encrypt/decrypt/sign/verify/rewrap,
  key versioning); React SPA (Nocturne design) covering projects, the secret
  editor, audit viewer, token/member management, transit, settings, operations,
  and an integrations hub; OIDC login and CI federation; reads-24h usage metrics.
- **Rotation + dynamic (Phase 3):** scheduled static rotation (Postgres + webhook);
  sync integrations (GitHub Actions, Kubernetes Secrets); dynamic Postgres
  credentials with a lease manager.
- **Hardening & depth:** project-KEK and master-key rotation; cursor pagination;
  Idempotency-Key middleware; HTTP timeouts/body caps; trash/restore, per-key
  history, and audit expand/timeline UI; a self-sufficient CLI control plane
  (project/env/config CRUD, token mint/list/revoke, whoami, completion, diff).
- **Release:** Apache-2.0 license; OpenAPI 3.1 spec; goreleaser multi-arch
  binaries + GHCR image; production deployment guide.

[Unreleased]: https://github.com/steveokay/janus-secrets/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/steveokay/janus-secrets/releases/tag/v0.1.0
