# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Typed secrets (value/password/json/ssh_key/certificate/note): per-type editor,
  validation, and generate; type carried through promotion and clone; CLI
  `secrets set --type`.

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
