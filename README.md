# Janus

**A self-hosted secrets manager you actually own.** One Go binary plus
PostgreSQL — no SaaS, no multi-tenancy, no per-seat pricing, and your keys never
leave your server in plaintext.

[![ci](https://github.com/steveokay/janus-secrets/actions/workflows/ci.yml/badge.svg)](https://github.com/steveokay/janus-secrets/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/steveokay/janus-secrets)](https://github.com/steveokay/janus-secrets/releases)
[![license](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

Janus stores secrets in a **project → environment → config** tree, injects them
into your process with `janus run`, and records every read in a hash-chained
audit log. It also does the things you would otherwise bolt on later:
encryption-as-a-service, scheduled rotation, one-way sync to your CI and
clusters, and short-lived database credentials.

```sh
janus run -- ./my-service      # secrets arrive as env vars; nothing hits disk
```

---

## Quickstart

```sh
git clone https://github.com/steveokay/janus-secrets.git
cd janus-secrets
make dev-up        # builds, starts Postgres + Janus, then inits and unseals a 1-of-1 dev seal
```

The UI is now on **<http://localhost:8210>**. `make dev-up` prints a one-time
admin password — that is your login.

> The dev seal keeps its single share in `.dev/janus-share`, and **that share is
> the master key**. It exists to make local development painless and is not a
> production posture — see [Unseal](#unseal--the-server-starts-locked) below.

Then, from any project directory:

```sh
janus login --address http://localhost:8210
janus setup                             # binds this directory to a project/env/config
janus secrets set DATABASE_URL=postgres://…
janus run -- ./my-service               # injected as environment variables
```

New here? The [getting-started guide](docs/guides/getting-started.md) walks the
same path with more explanation, including doing it by hand instead of via
`make dev-up`.

## What you get

**Secrets**
- A **project → environment → config** tree with per-key secrets.
- **Two-level versioning**: each save creates one immutable *config version*
  (the unit of diff and rollback), and every key also keeps its own value
  history. Soft delete with restore; hard destroy is separate and explicit.
- **Inheritance** from a base config, and **references** between secrets
  (`${projects.app.prod.KEY}`), resolved at read time with cycle detection.
- Advisory **max-age** and **unused-key** flags, owner/note annotations, and
  typed values.

**Getting secrets to where they run**
- `janus run` injects into a subprocess — with `--watch` to restart it when the
  config changes — and `janus render` fills a template.
- **Sync** one-way to GitHub Actions, GitLab CI, Kubernetes, Cloudflare, Vercel,
  Netlify, AWS SSM and AWS Secrets Manager, with **drift detection** that reads
  the destination back.
- **Import** from `.env` / `.properties` files, or from Doppler, Vault KV and
  AWS Secrets Manager — in the CLI or a paste-based wizard in the UI.
- **SDKs** for Go, TypeScript and Python, and a **Terraform provider**.

**Access**
- Email + password (Argon2id) with optional **TOTP**, **WebAuthn passkeys** as a
  single-step sign-in, and **OIDC** login.
- **Scoped service tokens** for machines, and **OIDC-federated workload
  identity** so GitHub Actions or a Kubernetes pod exchanges its own JWT for a
  short-lived token — no long-lived secret in CI.
- **Deny-by-default RBAC** (viewer ⊂ developer ⊂ admin ⊂ owner) at instance,
  project or environment scope, plus **break-glass** time-boxed elevation.
- Four-eyes approval for **protected configs** and **environment promotion**.

**Accountability**
- An **append-only, hash-chained audit log**. Recording is fail-closed: if the
  audit write fails, the request fails.
- `GET /v1/audit/verify` walks the chain; the UI surfaces a "chain verified"
  badge. Export as JSONL or CSV, or **ship** it to a webhook or syslog.
- Signed **checkpoints** let a long log be pruned without breaking verification.

**More than storage**
- **Transit** — encryption as a service, with named keys, versioning and
  rewrap. Janus holds the keys, your app holds the ciphertext.
- **Rotation** — scheduled rotators for Postgres, MySQL, Redis, AWS IAM, OAuth
  clients and generic webhooks.
- **Dynamic credentials** — short-lived Postgres roles with a lease manager
  that renews and revokes.

**Operating it**
- A **Svelte SPA** embedded in the binary and served same-origin — no Node in
  production, and it works on a phone.
- Prometheus `/metrics`, a health panel, and a ready-made
  [Grafana dashboard + alerts](deploy/grafana).
- Encrypted backups (including scheduled to S3-compatible storage), master-key
  and project-key rotation, and a native TLS listener with optional ACME.

## How it works

### Envelope encryption

Three levels, so no single stored value can decrypt anything on its own:

| Key | Lives | Wrapped by |
|---|---|---|
| **Master key** (root KEK) | server memory only, after unseal | never persisted in plaintext |
| **Project KEK** | Postgres, wrapped | the master key |
| **DEK** (one per secret version) | Postgres, wrapped | that project's KEK |

Values are AES-256-GCM with random, never-reused nonces. Every ciphertext's
**AAD binds it to its exact storage slot** — project, config, key, version — so
ciphertext moved or swapped between slots fails to decrypt rather than
silently decrypting as another secret. The storage layer is **crypto-blind**:
it persists opaque bytes and never holds a key or a plaintext.

### Unseal — the server starts locked

The master key is never written down. On boot Janus is **sealed** and every
secret operation returns `503` until an operator unseals it, either by
submitting **Shamir** shares (k-of-n, default 3-of-5, `janus init` prints them
exactly once) or automatically via **cloud KMS** (AWS KMS, GCP KMS, Azure Key
Vault).

Details, including key rotation and the key-check value: [docs/crypto.md](docs/crypto.md).

### Configuration

The server is configured by environment only. The essentials:

| Variable | Meaning |
|---|---|
| `JANUS_DATABASE_URL` | Postgres DSN (required) |
| `JANUS_LISTEN_ADDR` | listen address, default `:8200` |
| `JANUS_SEAL_TYPE` | `shamir`, `awskms`, `gcpkms` or `azurekv` — set before first init; the stored type wins afterwards |
| `JANUS_ADDR` | the CLI's default server address |

Every variable is listed in
[docs/operations.md](docs/operations.md#configuration-environment-variables).

## Deploying

The binary and a multi-arch container publish automatically on a release tag.
The [production-deployment guide](docs/guides/production-deployment.md) covers
TLS, unseal strategy, sizing, backups and upgrades, and its
[deployment modes](docs/guides/production-deployment.md#10-deployment-modes)
section covers Docker Compose, Kubernetes, Swarm, Argo CD / Flux, Nomad and
systemd. There is a [Helm chart](deploy/helm/janus) for Kubernetes.

**Verify what you run.** Releases are cosign keyless-signed with syft SBOMs and
SLSA build-provenance attestations — check them with `cosign verify-blob` or
`gh attestation verify`.

## Documentation

Everything lives under [`docs/`](docs/) — start at the
[documentation index](docs/README.md).

**Guides** — [getting started](docs/guides/getting-started.md) ·
[injecting secrets](docs/guides/injecting-secrets.md) ·
[managing secrets](docs/guides/managing-secrets.md) ·
[the web UI](docs/guides/using-the-web-ui.md) ·
[service tokens](docs/guides/service-tokens.md) ·
[members & RBAC](docs/guides/members-and-rbac.md) ·
[passkeys](docs/guides/passkeys.md) ·
[two-factor auth](docs/guides/two-factor-auth.md) ·
[SSO & federation](docs/guides/sso-and-federation.md) ·
[promoting between environments](docs/guides/promoting-environments.md) ·
[protected configs](docs/guides/protected-configs.md) ·
[importing & exporting](docs/guides/import-export.md) ·
[GitHub Actions](docs/guides/github-actions.md) ·
[Docker](docs/guides/docker.md) ·
[Kubernetes](docs/guides/kubernetes.md) ·
[observability](docs/guides/observability.md) ·
[backup & restore](docs/guides/backup-and-restore.md) ·
[break-glass](docs/guides/break-glass.md)

**Reference** — [architecture](docs/architecture.md) ·
[cryptography](docs/crypto.md) ·
[data model & versioning](docs/data-model.md) ·
[references & inheritance](docs/references.md) ·
[CLI](docs/cli.md) · [operations](docs/operations.md) ·
[transit](docs/transit.md) · [OIDC](docs/oidc.md) ·
[CI federation](docs/ci-federation.md) · [web UI](docs/web.md) ·
[OpenAPI spec](docs/openapi.yaml) · [threat model](docs/threat-model.md)

**Engines** — [rotation](docs/ops/rotation.md) · [sync](docs/ops/sync.md) ·
[dynamic secrets](docs/ops/dynamic.md) ·
[backup & restore](docs/ops/backup-restore.md)

## Security

- AES-256-GCM everywhere, Argon2id for passwords, HMAC-SHA256 for token hashing
  — only token *hashes* are stored, never the tokens. Constant-time comparison
  for every key-check, token and MAC check.
- **No plaintext secret or key material in logs or errors**, enforced by leak
  tests at the crypto, secrets, HTTP and audit layers. An audit `Event` has no
  value field *by construction*, so a secret cannot enter the audit log even by
  mistake.
- The crypto is Go's standard library and `golang.org/x/crypto` only. Two
  exceptions are recorded, both for standards work that should not be
  hand-rolled: JOSE/JWKS verification for OIDC, and COSE/attestation parsing
  for passkeys. Envelope, transit and unseal crypto remain stdlib.
- **Outbound requests are SSRF-hardened**: a shared dialer re-checks the
  *resolved* IP on every dial (defeating DNS rebinding), blocks link-local and
  cloud-metadata ranges, and caps redirects — applied to every
  operator-configured caller, including OIDC discovery.
- `internal/crypto` is held to **100% statement coverage**, enforced in CI,
  including tamper and nonce-reuse cases. CI also runs `govulncheck` and
  `gosec` as build failures.

Full detail in [docs/threat-model.md](docs/threat-model.md), which is explicit
about what Janus does **not** defend against. To report a vulnerability, see
[SECURITY.md](SECURITY.md).

## Non-goals

Deliberately out of scope: HA / Raft clustering (run one node with Postgres
backups), PKI / certificate authority, SSH signing, HSM / PKCS#11,
multi-tenancy and organizations, and FIPS certification claims.

## Contributing

Build instructions, the test suite, the CI gates and the crypto and migration
rules are in [CONTRIBUTING.md](CONTRIBUTING.md). The short version:

```sh
make dev-up     # full local stack
make test       # every module, plus the web tests
```

The current state and remaining work are tracked in
[docs/roadmap.md](docs/roadmap.md) and [status.md](status.md).

## Trademarks

Third-party product and company names used in this project — including Doppler,
HashiCorp Vault, Amazon Web Services (AWS), Google Cloud, Microsoft Azure,
Kubernetes, GitHub, GitLab, and any others — are the trademarks or registered
trademarks of their respective owners. Janus is an independent project and is
**not affiliated with, endorsed by, or sponsored by** any of them. Such names
appear here solely to identify the third-party systems that Janus interoperates
with (for example, the `janus import` source systems and the cloud KMS providers
used for auto-unseal), which is nominative use.

## License

Janus is licensed under the **Apache License, Version 2.0** — see
[`LICENSE`](LICENSE) and [`NOTICE`](NOTICE).

The vendored `internal/crypto/shamir/` package is licensed under **MPL-2.0**
(see its `LICENSE`); its per-file headers are retained. MPL-2.0 is file-level
copyleft and compatible with Apache-2.0 distribution.
