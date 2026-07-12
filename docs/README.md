# Janus documentation

How the system works, feature by feature. These docs describe behavior and
design intent; they are kept in sync with the code as milestones land.

## System functionality

- [Architecture overview](architecture.md) — layering, packages, build phases,
  and how a secret flows through the system.
- [Cryptography](crypto.md) — envelope encryption, the key hierarchy, AAD
  binding, the in-memory keyring, and the two unseal mechanisms (Shamir + AWS
  KMS). **Implemented.**
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
  a GitHub Actions OIDC JWT for a short-lived scoped service token via
  structured-claim trust bindings. **Implemented.**
- [Web UI](web.md) — the embedded React SPA: unseal/login, the secret editor,
  version diff, audit viewer, token/member management, transit console, the
  dashboard, and the operations console. **Implemented.**
- [Operations: server & `janus` CLI](operations.md) — running the server, the
  seal lifecycle (init/unseal/seal), the sys HTTP API, configuration, the dev
  workflow, and the KMS auto-unseal setup. **Implemented.**
- [CLI reference](cli.md) — the `janus` secrets client: `login`/`setup`/
  `secrets`/`run`, the credential/address/binding precedence rules, the
  `.janus.yaml` format, and the `run` / `--plain` semantics. **Implemented.**

## Design specs & plans

- [`superpowers/specs/`](superpowers/specs/) — per-milestone design documents.
- [`superpowers/plans/`](superpowers/plans/) — per-milestone implementation
  plans.

## Status

All three build phases (Core, Transit + UI, Rotation + dynamic) have shipped.
See [`../status.md`](../status.md) for the live milestone tracker and
[`../gaps.md`](../gaps.md) for the current gap analysis and priority order.
