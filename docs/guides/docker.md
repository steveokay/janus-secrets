# Running Janus and apps with Docker

This guide has two audiences. **Part 1** is for operators running the
Janus server itself in a container. **Part 2** is for developers who
want to hand secrets from a running Janus to their own application
container.

Everything below is grounded in the shipped `Dockerfile`,
`docker-compose.yml`, and the environment-variable table in
[../operations.md](../operations.md). Where an example is meant to be
adapted rather than copied verbatim, it is labelled as such.

---

## Part 1 — Running the Janus server in Docker

### The image

Janus builds as a single static binary with the web UI embedded. The
shipped `Dockerfile` is multi-stage:

1. **web stage** (`node:22-alpine`) runs `npm ci` and `npm run build`,
   producing `web/dist/`.
2. **go stage** (`golang:1.26-alpine`) copies that `dist/` into
   `internal/web/dist/` so it is picked up by `go:embed`, then builds a
   `CGO_ENABLED=0` static binary.
3. **runtime stage** (`alpine:3.21`) runs as an unprivileged `janus`
   user (uid 10001), exposes `8200`, and has `ENTRYPOINT ["janus"]`
   with `CMD ["server"]`.

Because the UI is embedded, there is no Node process in production — one
binary serves both the API and the SPA.

### The compose stack

The checked-in `docker-compose.yml` is a two-service dev stack — Postgres
plus Janus — both with healthchecks:

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: janus
      POSTGRES_PASSWORD: janus-dev
      POSTGRES_DB: janus
    ports:
      - "127.0.0.1:5433:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U janus -d janus"]
      interval: 3s
      timeout: 3s
      retries: 20

  janus:
    build: .
    command: server
    environment:
      JANUS_DATABASE_URL: postgres://janus:janus-dev@postgres:5432/janus?sslmode=disable
      JANUS_SEAL_TYPE: shamir
    ports:
      - "127.0.0.1:8210:8200"
    depends_on:
      postgres:
        condition: service_healthy
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8200/v1/sys/ready"]
      interval: 5s
      timeout: 3s
      retries: 12

volumes:
  pgdata:
```

Notes worth internalizing:

- The Janus container listens on `8200` internally; the stack maps it to
  `8210` on the host (`127.0.0.1:8210:8200`), and Postgres maps to `5433`
  on the host (`127.0.0.1:5433:5432`). Both are bound to loopback.
- `depends_on … condition: service_healthy` holds Janus back until
  Postgres passes `pg_isready`.
- The Janus healthcheck polls `/v1/sys/ready` — it reports healthy only
  once the server is up **and** unsealed (see below).
- The `pgdata` named volume persists the database across restarts.

The server auto-applies its embedded migrations at boot (golang-migrate
takes a Postgres advisory lock, so concurrent boots are safe); no
separate migrate step is required for a normal `up`.

### Server environment variables

The full table is in [../operations.md](../operations.md#configuration-environment-variables).
The ones that matter for a container:

| Variable | Required | Meaning |
|---|---|---|
| `JANUS_DATABASE_URL` | yes | Postgres DSN, e.g. `postgres://janus:pw@postgres:5432/janus?sslmode=disable` |
| `JANUS_SEAL_TYPE` | before first init | `shamir`, `awskms`, `gcpkms`, or `azurekv`. After init the stored type is authoritative; a conflicting env value is a **fatal boot error** |
| `JANUS_AWS_KMS_KEY_ARN` | for `awskms` | KMS key id/ARN/alias (plus the standard AWS SDK env for credentials/region) |
| `JANUS_GCP_KMS_KEY` | for `gcpkms` | GCP KMS key resource `projects/P/locations/L/keyRings/R/cryptoKeys/K` (ambient GCP application-default credentials) |
| `JANUS_AZURE_KEYVAULT_URL` + `JANUS_AZURE_KEY_NAME` | for `azurekv` | Key Vault URL + RSA key name (ambient `DefaultAzureCredential`; optional `JANUS_AZURE_KEY_VERSION`) |
| `JANUS_LISTEN_ADDR` | no | HTTP listen address, default `:8200` |
| `JANUS_SESSION_IDLE_TIMEOUT` | no | Session inactivity window (default `30m`; `0` disables) |
| `JANUS_ROTATION_TICK` | no | Rotation scheduler tick; `0` disables (default `60s`) |
| `JANUS_SYNC_TICK` | no | Sync scheduler tick; `0` disables (default `60s`) |
| `JANUS_DYNAMIC_TICK` | no | Dynamic-lease scheduler tick; `0` disables (default `60s`) |

There is no config file — all server configuration is environment.

### Boots sealed: init once, unseal every start

A fresh Janus container boots **uninitialized**; after `init` it boots
**sealed** on every subsequent start. While sealed, every route except
`/v1/sys/*` answers `503 {"error":{"code":"sealed"}}`, and the container's
`/v1/sys/ready` healthcheck stays unhealthy.

The full seal ceremony — `janus init`, distributing Shamir shares to
custodians, `janus unseal`, and recovery from a bad share — lives in
[../operations.md](../operations.md#the-mental-model). The short version
for a container:

- **Shamir seal** (`JANUS_SEAL_TYPE=shamir`): you must `janus init` once,
  then `janus unseal` after **every** container start. Point the CLI at
  the container: `janus init --shares 5 --threshold 3` and
  `janus unseal` against `http://127.0.0.1:8210` (the host-mapped port).
  This is a manual step on each restart — acceptable for a single node
  you tend by hand, painful for autoscaling or unattended reboots.
- **Cloud KMS auto-unseal** (`JANUS_SEAL_TYPE=awskms` | `gcpkms` | `azurekv`):
  the server unseals itself with one KMS `Decrypt` call at every boot. This is
  the right choice for containers that must come back up unattended after a
  crash, redeploy, or host reboot. Each provider uses that cloud's ambient
  credentials (AWS default chain, GCP application-default credentials, Azure
  `DefaultAzureCredential`) and its key env var (`JANUS_AWS_KMS_KEY_ARN`,
  `JANUS_GCP_KMS_KEY`, or `JANUS_AZURE_KEYVAULT_URL` + `JANUS_AZURE_KEY_NAME`).
  You still `janus init` **once** (no shares);
  from then on restarts are hands-off. If KMS is unreachable at boot the
  container stays up but sealed and logs a warning; `janus unseal` (no
  share) retries.

### Orchestration probes

All of these are reachable while sealed (they live under `/v1/sys/`) and
are documented in [../operations.md](../operations.md#sys-http-api):

- `GET /v1/sys/live` — liveness. The process is up. Use as a
  container/pod liveness probe.
- `GET /v1/sys/ready` — readiness. Up **and** unsealed. Use as a
  readiness/traffic-gating probe (this is what the compose healthcheck
  uses). A sealed server is deliberately *not* ready.
- `GET /v1/sys/seal-status` — full seal state
  (`initialized`, `sealed`, `type`, `threshold`, and mid-ceremony
  `progress`). Useful for dashboards and unseal automation, not as a
  binary probe.
- `GET /v1/sys/health` — always `200` while the process is up, with
  `initialized`/`sealed` flags in the body.

### A production-shaped compose example (adapt me)

The following is **illustrative** — it is a starting point to adapt to
your infrastructure, not a drop-in production config. It differs from the
shipped dev stack in three ways: a persistent Postgres volume with real
credentials sourced from the host environment, `awskms` seal so the
container auto-unseals on restart, and no hard-coded passwords in the
file. Terminate TLS at a reverse proxy in front of this, or enable Janus's
native TLS listener (static certs or ACME via `JANUS_TLS_*` — see
[production-deployment.md §2.1](production-deployment.md#21-native-tls-in-the-binary)).
See also the security notes in
[../operations.md](../operations.md#security-posture-and-current-caveats).

```yaml
# ILLUSTRATIVE — adapt credentials, networking, and secrets management.
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: ${PG_USER}
      POSTGRES_PASSWORD: ${PG_PASSWORD}
      POSTGRES_DB: janus
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${PG_USER} -d janus"]
      interval: 5s
      timeout: 3s
      retries: 20
    restart: unless-stopped

  janus:
    image: your-registry/janus:latest   # built from the repo Dockerfile
    command: server
    environment:
      JANUS_DATABASE_URL: postgres://${PG_USER}:${PG_PASSWORD}@postgres:5432/janus?sslmode=disable
      JANUS_SEAL_TYPE: awskms
      JANUS_AWS_KMS_KEY_ARN: ${JANUS_AWS_KMS_KEY_ARN}
      # Standard AWS SDK env for KMS credentials/region:
      AWS_REGION: ${AWS_REGION}
      AWS_ACCESS_KEY_ID: ${AWS_ACCESS_KEY_ID}
      AWS_SECRET_ACCESS_KEY: ${AWS_SECRET_ACCESS_KEY}
    ports:
      - "8200:8200"          # front this with a TLS-terminating proxy
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

Run `janus init` against this instance exactly once (no shares under
`awskms`); every restart thereafter auto-unseals.

---

## Part 2 — Giving your app container secrets from Janus

Your application container should never bake secret **values** into its
image or its compose file. Instead, fetch them from Janus at container
start. Two patterns follow. Both need a scoped **service token** — see
[./service-tokens.md](./service-tokens.md) for how to mint one (shown
once at creation, HMAC-only on the server).

The broader story of getting secrets into a process — `janus run`,
precedence, and `download` — is in
[./injecting-secrets.md](./injecting-secrets.md).

### Pattern A (recommended): `janus run --` as the entrypoint

Make `janus run` the container's entrypoint so the app fetches its
secrets live at start and gets them injected as environment variables.
This is the flagship pattern: values only ever reach the child process's
environment — never the image, never disk, never Janus's own logs. To
pick up rotated or newly-added secrets, restart/redeploy the container.

This requires the `janus` binary in the image (copy it in from the Janus
build, or ship it via a small init/sidecar), plus at runtime a
`JANUS_ADDR`, a `JANUS_TOKEN`, and the project/env/config coordinates —
either as `JANUS_PROJECT`/`JANUS_ENV`/`JANUS_CONFIG` env vars or a
committed `.janus.yaml` binding (human slugs only, safe to commit).

A Dockerfile that layers the `janus` binary in front of your app:

```dockerfile
# ILLUSTRATIVE — adapt base images and the copy source for janus.
FROM your-registry/janus:latest AS janus

FROM your-app-base:latest
COPY --from=janus /usr/local/bin/janus /usr/local/bin/janus
COPY . /app
WORKDIR /app

# janus run fetches secrets, injects them as env vars, then execs the app.
ENTRYPOINT ["janus", "run", "--"]
CMD ["./my-service"]
```

The matching compose service — coordinates and credentials come from the
environment, so no secret values appear here:

```yaml
# ILLUSTRATIVE — JANUS_TOKEN is a scoped service token; keep it out of
# source control (inject via your platform's secret store).
services:
  my-service:
    image: your-registry/my-service:latest
    environment:
      JANUS_ADDR: https://janus.internal
      JANUS_TOKEN: ${JANUS_TOKEN}        # scoped service token, read-only
      JANUS_PROJECT: acme
      JANUS_ENV: prod
      JANUS_CONFIG: prod
    restart: unless-stopped
```

`janus run` performs one **audited** bulk reveal, overlays the secrets
onto the process environment (the secret wins on a name clash;
`--preserve-env` flips that), then execs the command after the required
`--`. The child inherits stdin/stdout/stderr, signals are forwarded, and
the child's exit code is propagated verbatim — so it behaves like a
transparent wrapper.

If you would rather not put the binary in your app image, run a tiny
`janus`-only init/sidecar container that writes the secrets into a shared
tmpfs and have the app read from there — but the entrypoint pattern above
is simpler and keeps values off any filesystem entirely.

### Pattern B: `--env-file` from a download at `docker run` time

If you cannot change the image's entrypoint, feed secrets in via
`--env-file` at launch, sourcing them from a `janus secrets download`
without ever writing plaintext to disk:

```sh
docker run --env-file <(janus secrets download --format env) \
  your-registry/my-service:latest
```

The `download` streams to stdout by default and the process substitution
(`<(...)`) keeps the plaintext in a transient pipe rather than a file on
disk. This is a snapshot taken at launch — to refresh, re-run the
container.

**Guardrail:** `janus secrets download` will only write a plaintext file
to disk if you explicitly pass `--plain` (with `--output PATH` it refuses
and writes nothing otherwise; `--plain --output` creates the file mode
`0600`). Prefer the streaming/process-substitution form above — a
plaintext `.env` on disk is the least-safe output.

### Kubernetes

For Kubernetes there is a native **sync integration** that replicates a
config's resolved secrets into a Kubernetes `Secret` (via server-side
apply), rather than wrapping each pod's entrypoint. See
[./kubernetes.md](./kubernetes.md).
