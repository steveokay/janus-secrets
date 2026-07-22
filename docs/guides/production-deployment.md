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

The server is **intentionally TLS-less**: it speaks plain HTTP and expects to
sit behind a reverse proxy (Caddy, nginx, an ALB/NLB, etc.) that terminates
TLS. Native TLS in the binary is a possible later milestone, not present
today.

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

## 3. Configuration

All server configuration is environment-only (no config file). These are the
`JANUS_*` variables the server and CLI actually read:

### Address & database

| Name | Meaning | Default |
|---|---|---|
| `JANUS_DATABASE_URL` | Postgres DSN. **Required** — the server refuses to start without it. | *(none — required)* |
| `JANUS_LISTEN_ADDR` | HTTP listen address for `janus server`. | `:8200` |
| `JANUS_ADDR` | Default server address used by the `janus` **client CLI** (`login`, `secrets`, etc.) when `--address` isn't passed. | *(none — must pass `--address` or set this)* |

### Unseal / seal type

| Name | Meaning | Default |
|---|---|---|
| `JANUS_SEAL_TYPE` | `shamir` or `awskms`. Required before first `janus init`; after init the stored type is authoritative and this must keep matching it on every boot. | *(none — required before first init)* |
| `JANUS_AWS_KMS_KEY_ARN` | The KMS key ARN used to wrap/unwrap the master key when `JANUS_SEAL_TYPE=awskms`. Standard AWS SDK credential/region env vars (`AWS_REGION`, `AWS_ACCESS_KEY_ID`, etc.) apply as usual. | *(none — required when `JANUS_SEAL_TYPE=awskms`)* |

### HTTP server timeouts & limits

| Name | Meaning | Default |
|---|---|---|
| `JANUS_HTTP_READ_TIMEOUT` | `net/http` server `ReadTimeout` (Go duration, e.g. `30s`); `0` disables. | `30s` |
| `JANUS_HTTP_WRITE_TIMEOUT` | `net/http` server `WriteTimeout`; `0` disables. | `0` (disabled) — kept off by default so long `GET /v1/audit/export` streams aren't cut off |
| `JANUS_HTTP_IDLE_TIMEOUT` | `net/http` server `IdleTimeout` for keep-alive connections. | `120s` |
| `JANUS_HTTP_MAX_BODY_BYTES` | Max request body size in bytes, enforced via `http.MaxBytesReader`; `0` disables the cap. Restore (`janus restore`) is exempt since backup files can be large. | `10485760` (10 MiB) |

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
- **Cloud KMS auto-unseal (`JANUS_SEAL_TYPE=awskms`)** — the master key is
  wrapped by an AWS KMS key (`JANUS_AWS_KMS_KEY_ARN`) and unwrapped
  automatically at startup with a single KMS decrypt call, no manual
  ceremony. This removes the human-in-the-loop unseal step but means the
  server needs working AWS credentials and network access to KMS at boot,
  and your KMS key's IAM policy becomes part of your security perimeter for
  the master key.

Either way, `janus seal-status` (or `GET /v1/sys/seal-status`) reports
`initialized`/`sealed`/`type`/`threshold`/submission progress, so you can
script a post-deploy check.

## 5. Running the image

Pull a tagged release:

```sh
docker pull ghcr.io/steveokay/janus:v0.5.0
```

**Pin a version tag in production — do not run `:latest`.** Migrations run
automatically on server startup (see [§8](#8-upgrades)), so an unpinned
image can silently apply a newer schema migration on a routine restart.

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
Also run standard Postgres backup practice for the underlying store — base
backups plus WAL archiving (e.g. `pg_basebackup` + continuous WAL shipping,
or your managed Postgres provider's point-in-time-recovery feature) — so you
can restore to an arbitrary point in time, not just to the moment of the
last `janus backup`.

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

**There is no Prometheus `/metrics` endpoint yet.** If you need
scrape-based metrics (request rates, latencies, error counts) you'll need to
front Janus with something that derives them externally (e.g. reverse-proxy
access-log metrics) for now; a native `/metrics` endpoint is tracked as a
known gap (`gaps.md` §7.6) and is not yet implemented — don't point a
Prometheus scrape config at Janus expecting one.
