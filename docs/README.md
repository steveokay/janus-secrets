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
- [Operations: server & `janus` CLI](operations.md) — running the server, the
  seal lifecycle (init/unseal/seal), the sys HTTP API, configuration, the dev
  workflow, and the KMS auto-unseal setup. **Implemented.**

## Design specs & plans

- [`superpowers/specs/`](superpowers/specs/) — per-milestone design documents.
- [`superpowers/plans/`](superpowers/plans/) — per-milestone implementation
  plans.

## Status

See [`../status.md`](../status.md) for the live milestone tracker.
