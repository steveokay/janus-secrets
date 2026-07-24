# Janus documentation

How the system works, feature by feature. These docs describe behavior and
design intent; they are kept in sync with the code as milestones land.

New here? Start with the [Getting started](guides/getting-started.md) guide,
which takes you from an empty machine to a secret injected into a real
process. The **How-to guides** below are task-oriented; the **System
functionality** and **Operations & integrations** sections are the
behavior/design references they link into.

## How-to guides

Task-oriented walkthroughs for the common workflows:

- [Getting started](guides/getting-started.md) — bring up the stack, unseal,
  log in, create your first project, and run a command with its secrets
  injected.
- [Using the web UI](guides/using-the-web-ui.md) — the in-browser first-run
  ceremony (init → unseal → login), themes, the command palette, the in-tray,
  and a map of where every task lives.
- [Importing & exporting](guides/import-export.md) — bulk import from `.env`
  / Java `.properties` with preview and per-key selection; the audited
  **Download .env** export; filename-style "file" keys.
- [Inbound importers](guides/importing.md) — one-shot `janus import
  doppler|vault|aws-sm`: pull secrets from Doppler, Vault KV, or AWS Secrets
  Manager into a Janus project/config, with a value-free `--dry-run`.
- [Promoting between environments](guides/promoting-environments.md) — the
  per-project pipeline, drag-and-drop promotion, the key-level review panel,
  approval requests (four-eyes), and locked keys.
- [Protected configs (require-approval)](guides/protected-configs.md) — mark a
  config so direct saves become an envelope-encrypted pending edit request a
  different person approves (four-eyes) — the edit-approval flow.
- [Operations console](guides/operations-console.md) — creating and running
  rotation policies, sync targets (GitHub / Kubernetes), and dynamic
  Postgres roles with shown-once credential issuance, all from the UI.
- [Members & RBAC](guides/members-and-rbac.md) — inviting users and binding
  roles at instance / project / environment scope, with the guardrails.
- [Break-glass access](guides/break-glass.md) — guarded, time-boxed emergency
  role elevation: the guard rules, TTL, and the loud audit + notification trail.
- [SSO & CI federation](guides/sso-and-federation.md) — configuring OIDC
  login and GitHub Actions trust bindings from the Integrations page.
- [Two-factor authentication](guides/two-factor-auth.md) — enabling TOTP for
  password logins, signing in with a code, recovery codes, and disabling.
- [Observability](guides/observability.md) — the Prometheus `/metrics`
  endpoint, the Settings health panel, and log level/format env vars.
- [Audit shipping](guides/audit-shipping.md) — stream the audit log as JSONL
  to a webhook or syslog for SIEM ingestion, with a durable high-water mark
  (at-least-once, no gaps).
- [Master key & backups](guides/master-key-and-backup.md) — master-key
  rotation, the Shamir rekey ceremony, encrypted backup download, passphrase
  change.
- [Backup & restore (data + scheduled S3)](guides/backup-and-restore.md) —
  `pg_dump`/`pg_restore` + WAL/PITR for the Postgres data, plus scheduled
  encrypted backups to S3-compatible storage with retention and a
  restore-rehearsal command.
- [Trash & recovery](guides/trash-and-recovery.md) — soft delete, restore,
  and permanent destroy.
- [Notifications](guides/notifications.md) — webhook / Slack alerting on
  rotation & sync failures, denials, and pending approvals.
- [Injecting secrets into your app](guides/injecting-secrets.md) — `janus run`
  in depth, env-var precedence, `.janus.yaml` binding, client auth, and the
  `janus secrets download` file fallback with its `--plain` guardrail.
- [Managing secrets](guides/managing-secrets.md) — the project → env → config
  → secret hierarchy, creating containers (UI/API), the `janus secrets` CLI,
  two-level versioning/rollback, soft-delete vs. destroy, and references/
  inheritance.
- [Service tokens](guides/service-tokens.md) — minting scoped `janus_svc_…`
  tokens via `POST /v1/tokens` or the web UI, the scoping model, per-token IP
  allowlists + new-IP notes, last-used tracking, and using them from apps/CI.
- [Go client SDK](guides/go-sdk.md) — the standalone `sdk/go/` module: a typed
  client with an in-process TTL cache and dynamic-lease renewal, for apps that
  read secrets natively instead of hand-rolling HTTP.
- [TypeScript client SDK](guides/typescript-sdk.md) — the standalone `sdk/ts/`
  npm package (`janus-client`): a typed, zero-dependency client mirroring the
  Go SDK (in-memory TTL cache, typed errors, dynamic-lease renewal) for Node
  18+ and modern runtimes.
- [Python client SDK](guides/python-sdk.md) — the standalone `sdk/python/`
  package (`janus_client`), stdlib-only, mirroring the Go SDK: typed reads, an
  in-memory TTL cache, typed exceptions, and dynamic-credential leases.
- [GitHub Actions integration](guides/github-actions.md) — pushing secrets
  into Actions (sync) vs. pulling them keyless via OIDC federation, and when
  to use which.
- [Running Janus and apps with Docker](guides/docker.md) — running the server
  container and feeding app containers their secrets.
- [Kubernetes integration](guides/kubernetes.md) — syncing a config to a
  namespaced `Secret`, refreshing running pods, and whether you need a
  controller (you don't, for the sync itself).
- [Production deployment](guides/production-deployment.md) — TLS (native
  `JANUS_TLS_*` static-cert/ACME listener **or** a reverse proxy), the full
  `JANUS_*` configuration reference (DB pool, shutdown grace, all engine
  ticks), unseal in production (Shamir vs. AWS/GCP/Azure KMS auto-unseal),
  running the released image, sizing, backups, upgrades, and monitoring.

## System functionality

- [Architecture overview](architecture.md) — layering, packages, build phases,
  and how a secret flows through the system.
- [Cryptography](crypto.md) — envelope encryption, the key hierarchy, AAD
  binding, the in-memory keyring, and the unseal mechanisms (Shamir + cloud-KMS
  auto-unseal for **AWS KMS, GCP KMS, and Azure Key Vault**). **Implemented.**
- [Data model & versioning](data-model.md) — the project → environment → config
  → secret hierarchy and the two-level (config-version + per-key) versioning
  scheme. **Implemented.**
- [References & inheritance](references.md) — config inheritance (child-wins
  merge) and secret references (`${projects.…}` / `${KEY}`), resolved at read
  time with cycle detection and strict per-target authorization. **Implemented.**
- [Transit engine](transit.md) — encryption-as-a-service: named keys,
  encrypt/decrypt/sign/verify/rewrap/datakey, key versioning, and the
  `janus:v<N>:` envelope. **Implemented.**
- [OIDC login](oidc.md) — human sign-in via a generic OIDC provider
  (Authorization Code + PKCE + state + nonce), master-key-wrapped client secret,
  and admin config under `/v1/sys/oidc`. **Implemented.**
- [CI federation](ci-federation.md) — OIDC-federated machine identity: exchanging
  a CI OIDC JWT (**GitHub Actions, GitLab, Buildkite, CircleCI**) for a
  short-lived scoped service token via provider-aware structured-claim trust
  bindings. **Implemented.**
- [Web UI](web.md) — the embedded **Atrium** SPA (Svelte, dual-theme
  "Security Printing" design): init/unseal/login ceremony, the secret editor
  (import/export, multi-line + JSON/PEM-aware values, bulk select, per-key
  history + read insights, max-age/unused/annotation chips, value generator,
  locked keys), promotion + approvals, cross-env diff, audit ledger, tokens,
  scoped members, transit, operations, integrations, break-glass, trash, and
  settings — with a keyboard-shortcut palette, `?`/`g`-nav, and an
  accessibility pass (focus traps, table ARIA, reduced-motion). **Implemented.**
- [Operations: server & `janus` CLI](operations.md) — running the server, the
  seal lifecycle (init/unseal/seal), the sys HTTP API, configuration, the dev
  workflow, and the KMS auto-unseal setup. **Implemented.**
- [CLI reference](cli.md) — the `janus` secrets client: `login`/`setup`/
  `secrets`/`run`, the credential/address/binding precedence rules, the
  `.janus.yaml` format, and the `run` / `--plain` semantics. **Implemented.**

## Operations & integrations

Running Janus and connecting it to the outside world:

- [Server & `janus` CLI operations](operations.md) — the operational home
  page: running the server, the seal lifecycle, sys HTTP API, and config.
- [Backup & restore](ops/backup-restore.md) — the key-preserving instance
  dump and restore procedure.
- [Static rotation](ops/rotation.md) — scheduled secret rotation with **six
  rotators** (`postgres`, `webhook`, `mysql`, `redis`, plus the
  external-credential-generating `oauth` and `aws_iam`), the in-process
  scheduler, and failure/backoff. **Implemented.**
- [Sync integrations](ops/sync.md) — one-way replication of a config's resolved
  secrets to **eight providers** (GitHub Actions, Kubernetes `Secret`s, GitLab
  CI, Cloudflare Workers, Vercel, Netlify, AWS SSM, AWS Secrets Manager):
  prune/full-mirror, change detection, and credential masking. **Implemented.**
- [Kubernetes integration](guides/kubernetes.md) — the end-to-end k8s how-to
  (cluster RBAC, consuming the Secret, refreshing pods) built on the sync
  reference.
- [Dynamic Postgres credentials](ops/dynamic.md) — Vault-style short-lived
  database roles with a TTL/renewal/revocation lease manager. **Implemented.**

## API reference

- [OpenAPI spec](openapi.yaml) — machine-readable REST API definition for
  all `/v1/` routes; feed it to your favorite OpenAPI viewer/codegen tool.

## Design specs & plans

- [`superpowers/specs/`](superpowers/specs/) — per-milestone design documents.
- [`superpowers/plans/`](superpowers/plans/) — per-milestone implementation
  plans.

## Status

All three build phases (Core, Transit + UI, Rotation + dynamic) have shipped.
See [`../status.md`](../status.md) for the open-items tracker (backend/ops
gaps + the forward product roadmap).

Forward-looking feature proposals also live in [roadmap.md](roadmap.md).
