# Production deployment

This guide covers running Janus in production: TLS termination, configuration,
unseal, image deployment, sizing, backups, upgrades, and monitoring. It
complements [Operations: server & `janus` CLI](../operations.md) (the day-2
operational reference) and [Backup & restore](../ops/backup-restore.md).

## 1. Overview

Janus is a **single node + Postgres** deployment by design — there is no
HA/Raft clustering story (see the [non-goals](../../README.md#non-goals)).
Run one `janus server` process against one Postgres instance; scale
vertically, not horizontally.

By default the server speaks **plain HTTP** and expects to sit behind a
reverse proxy (Caddy, nginx, an ALB/NLB, etc.) that terminates TLS — this is
the recommended setup for most deployments. Small shops that want to run
Janus directly on TLS can enable the **native TLS listener** (static certs or
built-in ACME/Let's Encrypt) instead — see
[§2.1 Native TLS](#21-native-tls-in-the-binary).

The server **boots sealed**. The master key is not in memory until an
operator (or KMS auto-unseal) unseals it; every secret-touching route
returns `503 {"error":{"code":"sealed"}}` until then. Plan your deployment
(and any startup health-gating) around this — see
[§4 Unseal in production](#4-unseal-in-production).

## 2. TLS termination

Point your reverse proxy at the container's HTTP port (`8200` inside the
container). Two concrete examples:

### Caddy

```caddyfile
janus.example.com {
    reverse_proxy janus:8200
}
```

Caddy handles automatic HTTPS (ACME) with no further config. If Janus is on
the same Docker network as Caddy, `janus:8200` resolves via Docker DNS
(compose service name); otherwise use the host/IP and published port.

### nginx

```nginx
server {
    listen 443 ssl;
    server_name janus.example.com;

    ssl_certificate     /etc/nginx/certs/janus.example.com.pem;
    ssl_certificate_key /etc/nginx/certs/janus.example.com-key.pem;

    location / {
        proxy_pass http://janus:8200;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # Audit export (GET /v1/audit/export) can stream a large JSONL/CSV
        # response over a long-lived connection. The server itself disables
        # its write timeout by default for this reason (see §3). Don't
        # undo that at the proxy: avoid aggressive proxy_read_timeout and
        # keep buffering off so the client sees a steady stream rather than
        # a stalled buffer.
        proxy_buffering off;
        proxy_read_timeout 300s;
    }
}
```

Janus doesn't use WebSockets or server push, so no `Upgrade`/`Connection`
header handling is needed — the only streaming concern is the audit export
endpoint, which is a plain long-lived HTTP response.

### 2.1 Native TLS (in the binary)

If you'd rather not run a reverse proxy, Janus can terminate TLS itself. All
paths negotiate **TLS 1.2 or higher**. Two mutually exclusive modes; leaving
all `JANUS_TLS_*` variables unset keeps the default plain-HTTP behaviour.

**Static certificates** — point Janus at a PEM cert/chain and its key. Both
`JANUS_TLS_CERT` and `JANUS_TLS_KEY` must be set together (setting only one is
a fatal startup error):

```bash
JANUS_TLS_CERT=/etc/janus/tls/fullchain.pem
JANUS_TLS_KEY=/etc/janus/tls/privkey.pem
JANUS_LISTEN_ADDR=:8443
# Optional: run a tiny HTTP→HTTPS 301 redirect server on :80.
JANUS_TLS_REDIRECT_HTTP=:80
```

**ACME / Let's Encrypt** — Janus obtains and renews certificates automatically
via `golang.org/x/crypto/acme/autocert`. Set the hostname(s) it is allowed to
request certs for; the HTTP-01 challenge and an HTTP→HTTPS redirect are served
on `:80`, so port 80 must be reachable from the internet:

```bash
JANUS_TLS_ACME_DOMAINS=janus.example.com          # comma-separated whitelist
JANUS_TLS_ACME_EMAIL=ops@example.com              # optional ACME contact
JANUS_TLS_ACME_CACHE=/var/lib/janus/acme          # cert cache dir (persist this!)
JANUS_LISTEN_ADDR=:443
```

> Persist the ACME cache directory (`JANUS_TLS_ACME_CACHE`, default
> `./.janus-acme`) across restarts — otherwise Janus re-requests certificates
> on every boot and can hit Let's Encrypt rate limits.

Static certs and ACME are mutually exclusive — configuring both is a fatal
startup error. The startup log line reports which mode is active
(`serving=http`, `serving=https (static-cert)`, or `serving=https (acme)`).

## 3. Configuration

All server configuration is environment-only (no config file). These are the
`JANUS_*` variables the server and CLI actually read:

### Address & database

| Name | Meaning | Default |
|---|---|---|
| `JANUS_DATABASE_URL` | Postgres DSN. **Required** — the server refuses to start without it. | *(none — required)* |
| `JANUS_LISTEN_ADDR` | HTTP listen address for `janus server`. | `:8200` |
| `JANUS_ADDR` | Default server address used by the `janus` **client CLI** (`login`, `secrets`, etc.) when `--address` isn't passed. | *(none — must pass `--address` or set this)* |

### Native TLS

Optional. All unset → plain HTTP (TLS delegated to a reverse proxy). Static
certs and ACME are mutually exclusive; see
[§2.1 Native TLS](#21-native-tls-in-the-binary).

| Name | Meaning | Default |
|---|---|---|
| `JANUS_TLS_CERT` | Path to a PEM certificate/chain. Must be set together with `JANUS_TLS_KEY` (only one → fatal error). | *(none)* |
| `JANUS_TLS_KEY` | Path to the PEM private key for `JANUS_TLS_CERT`. | *(none)* |
| `JANUS_TLS_ACME_DOMAINS` | Comma-separated hostname whitelist; enables ACME/Let's Encrypt cert provisioning. HTTP-01 challenge + redirect served on `:80`. | *(none)* |
| `JANUS_TLS_ACME_EMAIL` | Optional ACME account contact address. | *(none)* |
| `JANUS_TLS_ACME_CACHE` | Directory where issued certs are cached. **Persist this** across restarts. | `./.janus-acme` |
| `JANUS_TLS_REDIRECT_HTTP` | Static-cert mode only: address (e.g. `:80`) for a tiny HTTP→HTTPS 301 redirect server. | *(none — off)* |

### Unseal / seal type

| Name | Meaning | Default |
|---|---|---|
| `JANUS_SEAL_TYPE` | `shamir`, `awskms`, `gcpkms`, or `azurekv`. Required before first `janus init`; after init the stored type is authoritative and this must keep matching it on every boot. | *(none — required before first init)* |
| `JANUS_AWS_KMS_KEY_ARN` | The KMS key ARN used to wrap/unwrap the master key when `JANUS_SEAL_TYPE=awskms`. Standard AWS SDK credential/region env vars (`AWS_REGION`, `AWS_ACCESS_KEY_ID`, etc.) apply as usual. | *(none — required when `JANUS_SEAL_TYPE=awskms`)* |
| `JANUS_GCP_KMS_KEY` | The GCP KMS crypto-key resource name (`projects/P/locations/L/keyRings/R/cryptoKeys/K`) used to wrap/unwrap the master key when `JANUS_SEAL_TYPE=gcpkms`. Credentials come from ambient GCP **application-default credentials** (`GOOGLE_APPLICATION_CREDENTIALS`, workload identity, or the metadata server). | *(none — required when `JANUS_SEAL_TYPE=gcpkms`)* |
| `JANUS_AZURE_KEYVAULT_URL` | The Azure Key Vault URL (`https://<vault>.vault.azure.net/`) when `JANUS_SEAL_TYPE=azurekv`. Credentials come from ambient `DefaultAzureCredential` (env vars, managed identity, or Azure CLI login). | *(none — required when `JANUS_SEAL_TYPE=azurekv`)* |
| `JANUS_AZURE_KEY_NAME` | The Key Vault key name that wraps the master key (an RSA/RSA-HSM key; wrapping uses RSA-OAEP-256) when `JANUS_SEAL_TYPE=azurekv`. | *(none — required when `JANUS_SEAL_TYPE=azurekv`)* |
| `JANUS_AZURE_KEY_VERSION` | Optional specific Key Vault key version; empty selects the key's current enabled version. | *(none — uses current version)* |

### HTTP server timeouts & limits

| Name | Meaning | Default |
|---|---|---|
| `JANUS_HTTP_READ_TIMEOUT` | `net/http` server `ReadTimeout` (Go duration, e.g. `30s`); `0` disables. | `30s` |
| `JANUS_HTTP_WRITE_TIMEOUT` | `net/http` server `WriteTimeout`; `0` disables. | `0` (disabled) — kept off by default so long `GET /v1/audit/export` streams aren't cut off |
| `JANUS_HTTP_IDLE_TIMEOUT` | `net/http` server `IdleTimeout` for keep-alive connections. | `120s` |
| `JANUS_HTTP_MAX_BODY_BYTES` | Max request body size in bytes, enforced via `http.MaxBytesReader`; `0` disables the cap. Restore (`janus restore`) is exempt since backup files can be large. | `10485760` (10 MiB) |
| `JANUS_SHUTDOWN_GRACE` | Graceful-drain window on `SIGTERM`/`SIGINT` (Go duration, must be positive). Bounds how long in-flight requests have to finish before the main server and any auxiliary listeners (ACME `:80`, HTTP→HTTPS redirect) are force-closed. | `10s` |

### Database connection pool (`pgx`)

All optional. When a variable is unset, `pgx`'s own default is kept (shown in
the Default column). Invalid values fail boot with a clear error.

| Name | Meaning | Default |
|---|---|---|
| `JANUS_DB_MAX_CONNS` | Maximum size of the connection pool (`pgxpool.Config.MaxConns`; positive integer). | *(pgx default: `max(4, NumCPU)`)* |
| `JANUS_DB_MIN_CONNS` | Minimum number of idle connections kept warm (`pgxpool.Config.MinConns`; non-negative integer). | *(pgx default: `0`)* |
| `JANUS_DB_MAX_CONN_LIFETIME` | Max lifetime of a pooled connection before it is retired (Go duration, positive). | *(pgx default: `1h`)* |
| `JANUS_DB_MAX_CONN_IDLE_TIME` | Max time an idle connection may sit before it is closed (Go duration, positive). | *(pgx default: `30m`)* |

### Session & scheduler ticks

| Name | Meaning | Default |
|---|---|---|
| `JANUS_SESSION_IDLE_TIMEOUT` | UI session idle timeout (Go duration); `0` disables. | `30m` |
| `JANUS_LOCKOUT_ENABLED` | Master switch for per-account login lockout (progressive backoff after repeated failures). Set `false` to disable entirely. | `true` |
| `JANUS_LOCKOUT_THRESHOLD` | Consecutive failed logins for an account before the first lockout. Non-positive values fall back to the default. | `5` |
| `JANUS_LOCKOUT_BASE` | First lockout window (Go duration); each successive lockout escalates from here (`base × 5^(level−1)`, capped at max). | `1m` |
| `JANUS_LOCKOUT_MAX` | Cap on the lockout window (Go duration). Raised to `JANUS_LOCKOUT_BASE` if set lower. | `1h` |
| `JANUS_METRICS_TOKEN` | Enables the Prometheus `/metrics` endpoint and the bearer token scrapers must present. Unset ⇒ `/metrics` returns `404` (disabled). See [observability](observability.md). | *(none — disabled)* |
| `JANUS_LOG_LEVEL` | `slog` level: `debug`, `info`, `warn`, `error`. Invalid values warn and fall back. | `info` |
| `JANUS_LOG_FORMAT` | Log handler format: `text` or `json`. | `text` |
| `JANUS_ROTATION_TICK` | In-process static-rotation scheduler interval; `0` disables the ticker. | `60s` |
| `JANUS_SYNC_TICK` | In-process sync-integrations scheduler interval; `0` disables. | `60s` |
| `JANUS_DYNAMIC_TICK` | In-process dynamic-lease manager tick (renew/expire sweep); `0` disables. | `60s` |
| `JANUS_UNUSED_SECRET_DAYS` | Advisory unused-secret threshold in days: a key with no per-key reveal within this window is flagged "unused" in the masked list, editor, and overview In tray. Positive integer; `0`/unset/invalid ⇒ 90. Advisory only — never blocks. | `90` |

> **Account lockout is per-account, across all source IPs (known tradeoff).**
> Progressive login lockout (`JANUS_LOCKOUT_*`) counts consecutive failed
> logins for an *account*, not per source IP. That's deliberate — it stops
> distributed/rotating-IP password guessing that a per-IP counter would miss —
> but it also means an attacker who knows a victim's email can deliberately
> feed wrong passwords to keep that account locked out (a targeted denial of
> service against one account). This is an accepted tradeoff. Tune it with the
> `JANUS_LOCKOUT_*` knobs above (raise `JANUS_LOCKOUT_THRESHOLD`, shorten
> `JANUS_LOCKOUT_BASE`/`JANUS_LOCKOUT_MAX`), or set `JANUS_LOCKOUT_ENABLED=false`
> to disable lockout entirely if your front door already rate-limits by IP.
> An admin can always clear a lockout immediately with `janus user unlock`
> (no need to wait out the window).

### Outbound integrations (egress SSRF guard)

Notification webhooks/Slack/SMTP, rotation webhooks, all sync providers, and
OIDC discovery/JWKS dial through a shared hardened dialer. It validates the
**resolved** IP at connect time (on every dial, including redirect hops), which
is what defeats DNS rebinding, and always rejects the link-local /
cloud-metadata ranges (`169.254.0.0/16`, `fe80::/10`, `fd00:ec2::254`) plus
unspecified/multicast. Loopback and RFC1918/ULA are **allowed** by default,
because a self-hosted deployment legitimately talks to in-cluster and LAN
targets.

| Name | Meaning | Default |
|---|---|---|
| `JANUS_OUTBOUND_BLOCK_PRIVATE` | Also reject loopback + RFC1918 + ULA on outbound integration calls. Set `true` only if no integration target is on a private network. | `false` |
| `JANUS_OUTBOUND_ALLOW_PROXY` | Let outbound integration calls honour `HTTP_PROXY` / `HTTPS_PROXY` / `ALL_PROXY` (and `NO_PROXY`). **Weakens the SSRF guard — see below.** | `false` |

> **Proxy environment variables are ignored by default, on purpose.** The guard
> inspects the IP the kernel is about to dial. Through an HTTP proxy that IP is
> the *proxy's*: the proxy then resolves and fetches whatever target the config
> named, so the link-local/metadata block stops applying to the real
> destination — silently, and fail-open. These are server-to-service
> integration calls, not user browsing, so Janus ignores `HTTP_PROXY` and
> friends for them unless you opt in.
>
> If your deployment's only egress path is a proxy, set
> `JANUS_OUTBOUND_ALLOW_PROXY=true`. The server then logs a startup warning
> naming the proxy variables in effect, and falls back to a **best-effort
> URL-time check** that still rejects targets written as a literal blocked IP
> (`http://169.254.169.254/…`). That check is *not* equivalent protection: it
> cannot see through a hostname, so a DNS name pointing at the metadata address
> will pass. When you enable it, enforce destination allowlisting on the proxy
> itself.
>
> **This gap is permanent by design, not a pending fix.** Once traffic leaves
> through a proxy, the proxy — not Janus — performs DNS resolution and opens the
> connection, so no client-side check can be authoritative: a split-horizon or
> attacker-controlled name can resolve differently there than here. Resolving
> hostnames locally would only add the *appearance* of protection. If you need
> the metadata/link-local guarantee, either leave
> `JANUS_OUTBOUND_ALLOW_PROXY` off (the default) so Janus dials directly and the
> connect-time guard applies, or enforce the destination policy **on the proxy**,
> which is the only component positioned to see the real target.

### CLI / client-only

These are read by the `janus` **client** commands, not the server process:

| Name | Meaning | Default |
|---|---|---|
| `JANUS_TOKEN` | A `janus_svc_…` service token; when set, the CLI sends it as a bearer token instead of using a stored login session (used for CI/machine auth). Takes precedence over any stored session. | *(none)* |
| `JANUS_CONFIG` / `JANUS_PROJECT` | Bind a shell/CI invocation to a specific project/config without a `.janus.yaml` file (see [`janus setup`](../cli.md)). | *(none)* |
| `JANUS_CONFIG_DIR` | Overrides where the CLI stores `auth.json`/config (default `~/.config/janus/`). | `~/.config/janus/` |
| `JANUS_RUN_CHILD` | Internal marker set by `janus run` on the injected child process; not for operator use. | *(none)* |
| `JANUS_ENV` | Environment name binding used by some CLI flows alongside `JANUS_PROJECT`/`JANUS_CONFIG`. | *(none)* |

Only `JANUS_DATABASE_URL` is strictly required to boot the server; everything
else has a workable default or is only needed for a specific seal type.

## 4. Unseal in production

The server always **boots sealed**: the master key isn't in memory, and every
secret-touching API route returns `503 {"error":{"code":"sealed"}}` until an
operator (or auto-unseal) supplies it. `GET /v1/sys/health`,
`/v1/sys/live`, `/v1/sys/ready`, and `/v1/sys/seal-status` all work while
sealed so you can health-gate correctly (see [§9](#9-monitoring)).

Two mechanisms, chosen at first `janus init` via `JANUS_SEAL_TYPE` (the
stored type is authoritative on every subsequent boot — the env var must
keep matching it):

- **Shamir (`JANUS_SEAL_TYPE=shamir`)** — the master key is split k-of-n
  (production default 3-of-5, configurable at `init` time). After every
  restart, an operator runs `janus unseal` once per share (share read from
  stdin with echo off) until the threshold is met. This is a manual
  ceremony by design — no single operator, config file, or secret holds the
  whole key.
- **Cloud KMS auto-unseal** — the master key is wrapped by a cloud KMS key and
  unwrapped automatically at startup with a single KMS decrypt call, no manual
  ceremony. This removes the human-in-the-loop unseal step but means the server
  needs working cloud credentials and network access to the KMS at boot, and
  the KMS key's access policy becomes part of your security perimeter for the
  master key. Three providers are supported, each pinned to a single key and
  relying on that cloud's ambient credential model:
  - **AWS KMS (`JANUS_SEAL_TYPE=awskms`)** — key via `JANUS_AWS_KMS_KEY_ARN`;
    credentials/region from the standard AWS SDK default chain
    (`AWS_REGION`, `AWS_ACCESS_KEY_ID`/role, etc.).
  - **GCP KMS (`JANUS_SEAL_TYPE=gcpkms`)** — key via `JANUS_GCP_KMS_KEY` (the
    `projects/…/cryptoKeys/…` resource name, a symmetric ENCRYPT_DECRYPT key);
    credentials from GCP **application-default credentials** (service-account
    JSON via `GOOGLE_APPLICATION_CREDENTIALS`, GKE workload identity, or the
    GCE metadata server). No key material is stored by Janus; encrypt uses the
    key's primary version and decrypt resolves the version from the ciphertext.
  - **Azure Key Vault (`JANUS_SEAL_TYPE=azurekv`)** — vault via
    `JANUS_AZURE_KEYVAULT_URL`, key via `JANUS_AZURE_KEY_NAME` (an RSA/RSA-HSM
    key; wrapping uses RSA-OAEP-256), and optional `JANUS_AZURE_KEY_VERSION`
    (empty = current version); credentials from ambient `DefaultAzureCredential`
    (environment vars, managed identity, or `az login`).

Either way, `janus seal-status` (or `GET /v1/sys/seal-status`) reports
`initialized`/`sealed`/`type`/`threshold`/submission progress, so you can
script a post-deploy check.

## 5. Running the image

> Deploying on Kubernetes, Docker Swarm, Nomad, Argo CD, or bare-metal
> systemd rather than plain Compose? Jump to
> [§10 Deployment modes](#10-deployment-modes) for per-target manifests and
> the Helm chart — then come back here for the config/unseal/monitoring
> reference each mode links into.

Pull a tagged release:

```sh
docker pull ghcr.io/steveokay/janus:v0.5.0
```

**Pin a version tag in production — do not run `:latest`.** Migrations run
automatically on server startup (see [§8](#8-upgrades)), so an unpinned
image can silently apply a newer schema migration on a routine restart.

### Verify what you run

Releases are **cosign keyless-signed** and carry **SLSA build-provenance**
attestations, so you can confirm an artifact was built by this repo's release
workflow before deploying it.

Verify the release binaries' checksums signature (cosign, keyless — the identity
is the release workflow's OIDC subject):

```sh
cosign verify-blob checksums.txt \
  --signature checksums.txt.sig \
  --certificate checksums.txt.pem \
  --certificate-identity-regexp 'https://github.com/steveokay/janus-secrets/\.github/workflows/release\.yml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Verify build provenance for a downloaded binary/archive or the container image
via the GitHub attestations API:

```sh
gh attestation verify janus_<version>_linux_amd64.tar.gz --repo steveokay/janus-secrets
gh attestation verify oci://ghcr.io/steveokay/janus:v0.5.0 --repo steveokay/janus-secrets
```

Each release archive also ships a **syft SBOM**, and an SBOM attestation is
attached to the image. See [`SECURITY.md`](../../SECURITY.md) and the
[threat model](../threat-model.md) for the trust assumptions behind this.

Minimal docker-compose for a production-shaped stack (app + Postgres; put a
reverse proxy from [§2](#2-tls-termination) in front of the `janus` service):

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: janus
      POSTGRES_PASSWORD: <use a secrets-managed value, not a literal>
      POSTGRES_DB: janus
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U janus -d janus"]
      interval: 3s
      timeout: 3s
      retries: 20

  janus:
    image: ghcr.io/steveokay/janus:v0.5.0
    command: server
    environment:
      JANUS_DATABASE_URL: postgres://janus:<password>@postgres:5432/janus?sslmode=disable
      JANUS_SEAL_TYPE: shamir   # or awskms, with JANUS_AWS_KMS_KEY_ARN
    ports:
      - "127.0.0.1:8200:8200"  # bind to loopback; the reverse proxy fronts this
    depends_on:
      postgres:
        condition: service_healthy
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8200/v1/sys/ready"]
      interval: 5s
      timeout: 3s
      retries: 12
    restart: unless-stopped

volumes:
  pgdata:
```

Use a real TLS-enabled Postgres connection (`sslmode=require` or stronger) in
production rather than `sslmode=disable`, and put Postgres behind your own
network boundary — it is never meant to be reachable from outside the
deployment.

For the full local/dev stack (with the dev-only 1-of-1 seal helper), see the
repo's own [`docker-compose.yml`](../../docker-compose.yml) and the
[Getting started](getting-started.md) guide — those are dev conveniences, not
a production template.

## 6. Sizing

Janus is a lightweight single-process Go server; there's no built-in
benchmark suite, so treat this as starting guidance rather than a
guarantee:

- **App container:** 1 vCPU / 256–512 MiB RAM is comfortable for small-to-mid
  teams (dozens of projects, low hundreds of req/s). CPU cost is dominated by
  Argon2id (login) and AES-GCM (secret read/write) — both cheap per-request
  but worth headroom if login or bulk-reveal traffic is heavy.
- **Postgres:** size connections, not just CPU/RAM — the server uses `pgx`'s
  pool, so keep `max_connections` comfortably above the pool's ceiling with
  room for `janus backup`/`migrate`/admin sessions. A small managed Postgres
  instance (1–2 vCPU, a few GB RAM) is enough for most single-tenant
  deployments; grow with row count and audit-log retention, not request
  rate.
- **Scaling model:** vertical only — there is no clustering or read-replica
  support (see [non-goals](../../README.md#non-goals)). If you outgrow one
  node, that's a signal to size the node up, not to add nodes.

## 7. Backups

Use [`janus backup`](../ops/backup-restore.md) for a **key-preserving
application-level dump** — every row exactly as stored (wrapped KEKs, wrapped
DEKs, ciphertext, password hashes, token HMACs), no plaintext secrets. Cron
it and ship the file offsite. Full procedure, including restore, is in
[Backup & restore](../ops/backup-restore.md).

That app-level backup is **not a substitute for Postgres-level backups**.
Also run standard Postgres backup practice for the underlying store — logical
`pg_dump`/`pg_restore` plus base backups and WAL archiving (e.g.
`pg_basebackup` + continuous WAL shipping, or your managed Postgres
provider's point-in-time-recovery feature) — so you can restore to an
arbitrary point in time, not just to the moment of the last `janus backup`.
Full commands, a side-by-side of **what each backup covers**, and a PITR
walkthrough are in [Postgres backup & restore](backup-and-restore.md).

Whichever path you restore from, remember: **a restored instance is useless
without the original unseal material.** Neither backup format stores the
master key or Shamir shares in the clear — your unseal shares (or KMS key
and its IAM access) are as much a part of your disaster-recovery plan as the
database dump itself.

## 8. Upgrades

- **Migrations run automatically on server startup** (`golang-migrate`
  against `migrations/`) — there is no separate manual migration step in the
  normal path (`janus migrate` exists for applying migrations explicitly,
  e.g. ahead of starting the server).
- **Back up first.** Take a `janus backup` (and ensure your Postgres-level
  backup/WAL archiving is current) before every upgrade — see
  [§7](#7-backups).
- **Pin, then bump.** Change the image tag in your compose/manifest to the
  new release, then restart. Don't float on `:latest`.
- **Roll forward only.** There's no supported downgrade path once a newer
  migration has applied — rolling back to an older image against an
  already-migrated schema is not supported. If an upgrade goes wrong,
  restore from the pre-upgrade backup onto a fresh instance instead.
- **No rolling/HA upgrade story.** Because Janus is single-node by design
  (see [§1](#1-overview)), an upgrade means stopping the old container and
  starting the new one — expect a brief window of `503`s (or connection
  refused) while the server restarts and comes back sealed. Plan upgrade
  windows accordingly, and remember the server needs unsealing again after
  the restart (manual Shamir ceremony, or automatic if using KMS — see
  [§4](#4-unseal-in-production)).

## 9. Monitoring

Wire your orchestrator's health checks (and any external uptime monitor) to
the `/v1/sys/*` probe endpoints:

- `GET /v1/sys/health` — always `200 {"status":"ok","initialized":bool,"sealed":bool}` while the process is up. Suitable for a container-level liveness probe (compose's own healthcheck uses `/v1/sys/ready`, see [§5](#5-running-the-image)).
- `GET /v1/sys/live` — a plain liveness probe (`200 {"status":"live"}`), independent of init/seal state.
- `GET /v1/sys/ready` — a readiness probe: `503` (`not_initialized` or `sealed`) until the instance is initialized *and* unsealed, `200 {"status":"ready"}` once it can actually serve secret operations. Use this for your load balancer / orchestrator readiness gate, not `/health`, if you want traffic held back until unseal completes.
- `GET /v1/sys/seal-status` — richer state (`initialized`, `sealed`, `type`, `threshold`/`shares`, Shamir submission `progress`) for dashboards or a post-deploy unseal script.
- `GET /v1/sys/version` — authenticated endpoint returning build info (version/commit/date); useful for confirming what's actually running after a deploy.

For usage/traffic visibility, the web UI's dashboard shows a **reads-24h**
metric (derived from audit events, no external metrics stack required) — see
[usage metrics](../web.md).

For scrape-based metrics, Janus exposes a **Prometheus `/metrics` endpoint**
(request rates/latencies keyed by chi route pattern, seal state, engine/DB/audit
gauges). It is **off by default** and enabled by setting `JANUS_METRICS_TOKEN`;
scrapers then present that value as `Authorization: Bearer <token>` (with the
token unset, `/metrics` returns `404`). The admin **health panel** in Settings,
backed by `GET /v1/sys/status`, surfaces DB latency, scheduler tick ages, and
failed-run counts. See the [observability guide](observability.md).

## 10. Deployment modes

Everything above is orchestrator-agnostic — TLS, the `JANUS_*` config
reference (§3), unseal (§4), and the probe endpoints (§9) apply no matter how
you run the container. This section is the how-to for each concrete target:
Docker/Compose, Kubernetes (raw manifests **and** the Helm chart), Docker
Swarm, GitOps (Argo CD / Flux), and the short cases (Nomad, bare-host
systemd).

Three facts shape every one of them:

- **Janus boots sealed** (§4). Nothing serves secrets until the master key is
  supplied. Where a human can't run `janus unseal` after each restart —
  Kubernetes, Nomad, autoscaled hosts — use **cloud-KMS auto-unseal** so each
  process self-unseals on boot.
- **Single node by design** (§1). Run exactly one `janus server` process
  against one Postgres. A second concurrent process double-runs the in-process
  schedulers (rotation / sync / dynamic leases); every mode below pins to one
  instance and a stop-old-then-start-new rollout.
- **Migrations run on boot** (§8). Pin an exact image tag; never float
  `:latest`, or a routine restart can silently migrate the schema.

Postgres is **bring-your-own** in all modes — a managed instance (RDS, Cloud
SQL, Azure Database) is strongly preferred. The bundled/inline Postgres shown
in some snippets below is an evaluation convenience, never a production store.

### 10.1 Docker (single container + Compose)

The baseline. A tagged container, a `JANUS_DATABASE_URL`, a `JANUS_SEAL_TYPE`,
and a reverse proxy in front. The production-shaped Compose stack (app +
Postgres + healthcheck) is already in [§5](#5-running-the-image); the
[Docker guide](docker.md) covers running the server container and feeding app
containers their secrets in depth — this section doesn't duplicate it. In
short:

```sh
docker pull ghcr.io/steveokay/janus:v0.1.0
docker run --rm -p 127.0.0.1:8200:8200 \
  -e JANUS_DATABASE_URL='postgres://janus:…@db:5432/janus?sslmode=require' \
  -e JANUS_SEAL_TYPE=shamir \
  ghcr.io/steveokay/janus:v0.1.0 server
```

Then port-forward/`curl` the container and run the one-time `janus init` +
`janus unseal` (or let KMS auto-unseal). Bind the published port to loopback
and let your reverse proxy ([§2](#2-tls-termination)) terminate TLS.

### 10.2 Kubernetes

Janus runs cleanly on Kubernetes, but three gotchas trip people up — get them
right and the rest is ordinary:

1. **It boots sealed → use KMS auto-unseal.** A `shamir` deployment leaves
   every fresh pod sealed and `NotReady` until a human runs `janus unseal`
   after *each* restart — painful when the scheduler reschedules a pod at 3am.
   Set `JANUS_SEAL_TYPE` to `awskms` / `gcpkms` / `azurekv` and grant the pod's
   ServiceAccount access to the key (IRSA / GKE Workload Identity / Azure AD
   Workload Identity) so the pod self-unseals on boot. Shamir on k8s is
   supported but is a deliberate manual-ceremony choice.
2. **Single node → `replicas: 1` + `strategy: Recreate`.** Two replicas
   double-run the schedulers. `Recreate` (not `RollingUpdate`) guarantees the
   old pod is gone before the new one starts, so you never have two at once.
   Expect a brief `503`/`NotReady` window on every rollout — that's the
   single-node upgrade model from [§8](#8-upgrades).
3. **Liveness ≠ readiness.** Liveness is
   [`GET /v1/sys/live`](#9-monitoring) — always `200`, sealed-safe; **never**
   gate liveness on unseal or a sealed pod will be killed in a loop and never
   reach the point where you can unseal it. Readiness is `GET /v1/sys/ready` —
   `200` only when the DB is reachable **and** the instance is initialized
   **and** unsealed, so traffic is held back until Janus can actually serve
   secrets. Both probes are unauthenticated and must be `httpGet` (the
   distroless image has no shell for an `exec` probe). An optional
   `startupProbe` on `/v1/sys/live` gives a slow database time before liveness
   starts counting.

Bake in the hardened `securityContext` that matches the distroless nonroot
image (uid/gid **65532**, no shell): `runAsNonRoot`, `runAsUser/Group: 65532`,
`readOnlyRootFilesystem: true` (Janus writes nothing to disk unless you use
in-binary ACME — terminate TLS at the ingress instead),
`allowPrivilegeEscalation: false`, `capabilities.drop: [ALL]`,
`seccompProfile.type: RuntimeDefault`.

Verify the image before you roll it out (§5, [`SECURITY.md`](../../SECURITY.md)):

```sh
gh attestation verify oci://ghcr.io/steveokay/janus:v0.1.0 --repo steveokay/janus-secrets
```

#### (a) Raw `kubectl apply` manifests

Provide the DSN via a Secret and keep the seal env on the Deployment. This
example uses AWS KMS auto-unseal via IRSA (annotate the ServiceAccount with
your role ARN); for GCP/Azure swap the seal env per [§4](#4-unseal-in-production).

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: janus-db
  namespace: janus
type: Opaque
stringData:
  # Use a managed Postgres 16+ and sslmode=require or stronger.
  JANUS_DATABASE_URL: postgres://janus:REPLACE@db.internal:5432/janus?sslmode=require
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: janus
  namespace: janus
  annotations:
    # AWS IRSA — grant this role kms:Decrypt (+ kms:Encrypt for init) on the key.
    eks.amazonaws.com/role-arn: arn:aws:iam::111122223333:role/janus-kms
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: janus
  namespace: janus
spec:
  replicas: 1                 # single-node by design — do not raise
  strategy:
    type: Recreate           # never run two Janus pods at once
  selector:
    matchLabels: { app: janus }
  template:
    metadata:
      labels: { app: janus }
    spec:
      serviceAccountName: janus
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        runAsGroup: 65532
        fsGroup: 65532
        seccompProfile: { type: RuntimeDefault }
      containers:
        - name: janus
          image: ghcr.io/steveokay/janus:v0.1.0   # pin; never :latest
          args: ["server"]
          ports:
            - { name: http, containerPort: 8200 }
          env:
            - { name: JANUS_SEAL_TYPE,      value: awskms }
            - { name: JANUS_AWS_KMS_KEY_ARN, value: arn:aws:kms:us-east-1:111122223333:key/abcd-… }
            - { name: AWS_REGION,           value: us-east-1 }
            - { name: JANUS_LOG_FORMAT,     value: json }
          envFrom:
            - secretRef: { name: janus-db }        # supplies JANUS_DATABASE_URL
          securityContext:
            runAsNonRoot: true
            runAsUser: 65532
            runAsGroup: 65532
            readOnlyRootFilesystem: true
            allowPrivilegeEscalation: false
            capabilities: { drop: [ALL] }
            seccompProfile: { type: RuntimeDefault }
          livenessProbe:                            # sealed-safe; never gate on unseal
            httpGet: { path: /v1/sys/live, port: http }
            periodSeconds: 10
          readinessProbe:                           # 200 only when initialized + unsealed
            httpGet: { path: /v1/sys/ready, port: http }
            periodSeconds: 10
          startupProbe:                             # give the DB time on first boot
            httpGet: { path: /v1/sys/live, port: http }
            failureThreshold: 30
            periodSeconds: 5
          resources:
            requests: { cpu: 100m, memory: 128Mi }
            limits:   { cpu: "1",  memory: 512Mi }
---
apiVersion: v1
kind: Service
metadata:
  name: janus
  namespace: janus
spec:
  selector: { app: janus }
  ports:
    - { name: http, port: 8200, targetPort: http }
```

```sh
kubectl create namespace janus
kubectl apply -f janus.yaml
```

Add an `Ingress` (terminating TLS at the ingress controller) or an
`ExternalName`/`LoadBalancer` Service to expose it; the pod itself stays plain
HTTP on `:8200`.

#### (b) `helm install` (the bundled chart)

The repo ships a chart at
[`deploy/helm/janus`](../../deploy/helm/janus) that encodes all of the above —
`replicas: 1` + `Recreate`, the hardened `securityContext`, the correct
liveness/readiness/startup probes, per-provider seal wiring, and a
ServiceAccount you annotate for cloud identity. See
[`deploy/README.md`](../../deploy/README.md) for the full values reference.

AWS KMS auto-unseal, referencing an existing DSN Secret (the preferred path —
keeps the Postgres password out of Helm values):

```sh
kubectl create secret generic janus-db -n janus \
  --from-literal=JANUS_DATABASE_URL='postgres://janus:…@db.internal:5432/janus?sslmode=require'

helm install janus deploy/helm/janus -n janus --create-namespace \
  --set seal.type=awskms \
  --set seal.awskms.keyArn=arn:aws:kms:us-east-1:111122223333:key/abcd-… \
  --set seal.awskms.region=us-east-1 \
  --set serviceAccount.annotations."eks\.amazonaws\.com/role-arn"=arn:aws:iam::111122223333:role/janus-kms \
  --set database.existingSecret=janus-db \
  --set image.tag=0.1.0
```

GCP KMS and Azure Key Vault swap the seal block:

```sh
# GKE Workload Identity
helm install janus deploy/helm/janus -n janus --create-namespace \
  --set seal.type=gcpkms \
  --set seal.gcpkms.key='projects/P/locations/L/keyRings/R/cryptoKeys/K' \
  --set serviceAccount.annotations."iam\.gke\.io/gcp-service-account"='janus@P.iam.gserviceaccount.com' \
  --set database.existingSecret=janus-db

# Azure AD Workload Identity (also needs the pod label — set podLabels)
helm install janus deploy/helm/janus -n janus --create-namespace \
  --set seal.type=azurekv \
  --set seal.azurekv.vaultUrl='https://myvault.vault.azure.net/' \
  --set seal.azurekv.keyName=janus-master \
  --set serviceAccount.annotations."azure\.workload\.identity/client-id"=<client-id> \
  --set podLabels."azure\.workload\.identity/use"=true \
  --set database.existingSecret=janus-db
```

For a quick kick-the-tyres cluster with no external database, the chart can
stand up an **evaluation-only** single-replica Postgres
(`--set postgresql.enabled=true`) — never use it in production.

Two things that trip people up on a first install, neither a fault:

- **Do not use `helm install --wait` with `seal.type=shamir`.** Readiness gates
  on unseal by design, so the pod stays NotReady until you supply the quorum and
  `--wait` times out. Install without it, then unseal.
- **The pod may restart a few times on a fresh install** (`Exit Code: 1`) until
  Postgres accepts connections. The chart has no wait-for-DB init container;
  Kubernetes retries and it settles on its own.

The chart validates its seal configuration at template time: an unknown
`seal.type`, or a cloud-KMS type without its key, fails immediately with a
message naming the missing value rather than producing a pod that can never
unseal.

#### One-time `janus init` (both paths)

After the first deploy, initialize the empty database exactly once. Even with
KMS auto-unseal you init once — it returns the first-admin password (and, for
Shamir, the unseal shares / recovery material) **once**, so capture them
securely:

```sh
kubectl -n janus port-forward svc/janus 8200:8200 &
export JANUS_ADDR=http://127.0.0.1:8200
janus init                 # or: curl -XPOST $JANUS_ADDR/v1/sys/init
janus seal-status          # awskms/gcpkms/azurekv → sealed:false automatically
```

With `JANUS_SEAL_TYPE=shamir`, run `janus unseal` once per share until the
threshold is met — and repeat after **every** pod restart. The pod stays
`NotReady` (readiness → `/v1/sys/ready`) the whole time it's sealed; that's
expected.

### 10.3 Docker Swarm

Swarm mirrors the Compose stack with a `deploy:` block pinning one replica and a
stop-first update policy so two tasks never run at once.

> **Janus reads plain environment variables — there is no `_FILE` indirection.**
> The distroless release image also has no shell to run an entrypoint wrapper, so
> Swarm's file-based `secrets:` (mounted at `/run/secrets/…`) cannot feed the DSN
> to Janus. Provide `JANUS_DATABASE_URL` as an **environment value** (e.g. via a
> deploy-time `.env` kept out of version control, interpolated by `docker stack
> deploy`); note it is then visible through `docker service inspect`. If you
> require file-based secret injection, front Janus with your own shell-bearing
> wrapper image instead of the distroless release.

> **No in-image healthcheck on Swarm.** The distroless release image has no
> shell, `curl`, or `wget`, so a `HEALTHCHECK`/`test:` line can't run inside
> the container. Either **omit** the healthcheck (shown below) and rely on an
> external monitor hitting `/v1/sys/ready`, or run the check from outside the
> container. Don't copy the Compose `wget` healthcheck from [§5](#5-running-the-image)
> — that snippet targets a shell-bearing image, not the distroless release.

`janus-stack.yml`:

```yaml
services:
  janus:
    image: ghcr.io/steveokay/janus:v0.1.0   # pin; never :latest
    command: server
    environment:
      JANUS_SEAL_TYPE: awskms
      JANUS_AWS_KMS_KEY_ARN: arn:aws:kms:us-east-1:111122223333:key/abcd-…
      AWS_REGION: us-east-1
      JANUS_LOG_FORMAT: json
      # Janus reads the DSN from the env directly (no _FILE convention).
      # ${JANUS_DATABASE_URL} is interpolated from your shell at deploy time.
      JANUS_DATABASE_URL: ${JANUS_DATABASE_URL}
    ports:
      - target: 8200
        published: 8200
        mode: host
    deploy:
      replicas: 1                    # single-node by design
      restart_policy:
        condition: any
        delay: 5s
      update_config:
        order: stop-first            # never run two Janus tasks at once
    # No healthcheck: distroless has no shell — monitor /v1/sys/ready externally.
```

```sh
# The DSN comes from your shell environment at deploy time (keep it out of VCS):
export JANUS_DATABASE_URL='postgres://janus:…@db:5432/janus?sslmode=require'
docker stack deploy -c janus-stack.yml janus
```

Then `janus init` / `janus unseal` against the published port as in §10.2.

### 10.4 GitOps (Argo CD / Flux)

Point a GitOps controller at the Helm chart and it reconciles the Deployment,
Service, Secret, and ServiceAccount for you. **`janus init` and (for Shamir)
`janus unseal` are one-time, out-of-band steps GitOps does not manage** — the
controller brings the pod up sealed and it stays `NotReady` until an operator
runs the init/unseal ceremony once (KMS auto-unseal then handles every
subsequent restart on its own).

**Argo CD `Application`** (chart sourced from this repo):

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: janus
  namespace: argocd
spec:
  project: default
  source:
    repoURL: https://github.com/steveokay/janus-secrets
    targetRevision: v0.1.0            # pin a released tag
    path: deploy/helm/janus
    helm:
      values: |
        image:
          tag: "0.1.0"
        seal:
          type: awskms
          awskms:
            keyArn: arn:aws:kms:us-east-1:111122223333:key/abcd-…
            region: us-east-1
        serviceAccount:
          annotations:
            eks.amazonaws.com/role-arn: arn:aws:iam::111122223333:role/janus-kms
        database:
          existingSecret: janus-db     # created out-of-band, not by Argo
  destination:
    server: https://kubernetes.default.svc
    namespace: janus
  syncPolicy:
    automated: { prune: true, selfHeal: true }
    syncOptions:
      - CreateNamespace=true
```

**Flux** users express the same thing with a `HelmRelease` pointing at a
`GitRepository` (or a packaged chart in a `HelmRepository`), with
`spec.values` carrying the same seal/database/serviceAccount blocks. Keep the
DSN Secret out of Git (SOPS/sealed-secrets, or create it manually) and
reference it via `database.existingSecret`.

### 10.5 Brief: Nomad and bare host / systemd

**HashiCorp Nomad** — a single-instance service job. Pin `count = 1`, run the
tagged image, and wire the seal env; register a Consul health check against
`/v1/sys/ready` (Nomad can HTTP-check without a shell in the container):

```hcl
job "janus" {
  group "server" {
    count = 1                          # single-node by design
    task "janus" {
      driver = "docker"
      config {
        image = "ghcr.io/steveokay/janus:v0.1.0"   # pin; never :latest
        args  = ["server"]
        ports = ["http"]
      }
      env {
        JANUS_DATABASE_URL   = "postgres://janus:…@db:5432/janus?sslmode=require"
        JANUS_SEAL_TYPE      = "awskms"
        JANUS_AWS_KMS_KEY_ARN = "arn:aws:kms:us-east-1:111122223333:key/abcd-…"
        AWS_REGION           = "us-east-1"
        JANUS_LOG_FORMAT     = "json"
      }
      service {
        port = "http"
        check { type = "http", path = "/v1/sys/ready", interval = "10s", timeout = "3s" }
      }
    }
    network { port "http" { to = 8200 } }
  }
}
```

**Bare host / systemd** — run the single binary directly (download the release
archive for your platform, or `go build ./cmd/janus`). Put config in an
`EnvironmentFile` and run as an unprivileged user:

```ini
# /etc/systemd/system/janus.service
[Unit]
Description=Janus secrets manager
After=network-online.target
Wants=network-online.target

[Service]
User=janus
Group=janus
EnvironmentFile=/etc/janus/janus.env     # JANUS_DATABASE_URL, JANUS_SEAL_TYPE, …
ExecStart=/usr/local/bin/janus server
Restart=on-failure
# Hardening (matches the container's posture)
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
# If using in-binary ACME, allow its cache dir to be writable:
# ReadWritePaths=/var/lib/janus/acme

[Install]
WantedBy=multi-user.target
```

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now janus
janus init && janus unseal          # one-time (Shamir); KMS auto-unseals on start
```

With Shamir, remember the manual unseal after every restart; on a bare host
that means an operator (or your own automation feeding shares) after each boot
— another reason to prefer KMS auto-unseal wherever a human isn't in the loop.
