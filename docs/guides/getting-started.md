# Getting started

This guide takes you from an empty machine to a running Janus instance with
your first secret injected into a real process. Janus ships as a single `janus`
Go binary plus PostgreSQL; the same binary is both the server and the
developer/operator CLI. We use the bundled docker-compose stack throughout so
you have a working instance in a few commands, then walk the end-to-end flow:
bring the stack up, unseal it, log in, create a project, and run a command with
its secrets in the environment.

For the full seal lifecycle, the complete CLI reference, and every
configuration knob, this guide links out to [operations.md](../operations.md)
and [cli.md](../cli.md) rather than repeating them.

## Prerequisites

You need one of two toolchains:

- **Docker + Docker Compose** (recommended) — the quickstart path. The compose
  stack builds the binary and runs Postgres and Janus for you.
- **A Go toolchain** (latest stable) — only if you want to build the `janus`
  binary locally for the CLI, or run the server outside Docker. `make build`
  builds the web assets, embeds them, and produces `bin/janus`.

The examples assume a POSIX shell. On Windows, this repository's Bash (Git Bash)
runs the shell scripts and `make` targets.

## 1. Bring up the stack

The one-shot path builds the binary, starts the compose stack, and unseals a
dev instance:

```sh
make dev-up
```

`make dev-up` runs `build` (web + Go), then `docker compose up -d --build`
(Postgres + Janus, both with healthchecks), then `scripts/dev-unseal.sh`. The
compose stack maps the app to host port **8210** (container `8200`) and Postgres
to host port **5433**. The unseal script:

1. waits for the server to answer,
2. if the instance is uninitialized, runs `janus init --shares 1 --threshold 1`
   and caches the single share in **`.dev/janus-share`** (gitignored, mode
   `0600`),
3. unseals with the cached share — idempotent, so it is safe to re-run after
   every restart.

**The dev share IS the master key.** A 1-of-1 seal with the share written to
disk is a deliberate dev-only convenience; production uses a real *k*-of-*n*
split and never writes shares to disk. Dev and production share every code path
— the only "dev mode" is the share count.

### Manual path

If you would rather drive the steps yourself (or `make dev-up` is inconvenient
on your platform), bring the containers up directly and then unseal:

```sh
docker compose up -d --build     # Postgres + Janus, app on :8210, db on :5433
./scripts/dev-unseal.sh          # init (1-of-1) + cache share + unseal
```

`docker compose up` builds the web assets and the Go binary inside the image, so
you do not need a local Node or Go toolchain for the server itself. You do need
a local `bin/janus` (via `make build`) to run the CLI commands below against the
instance — or run the CLI from inside the container.

After the stack is up, the instance is reachable at `http://127.0.0.1:8210`.
Point the CLI at it once via the `JANUS_ADDR` environment variable so you can
omit `--address` on every command:

```sh
export JANUS_ADDR=http://127.0.0.1:8210
```

## 2. Initialize and unseal

Janus boots **sealed**: the master key is not in memory, and every route except
`/v1/sys/*` answers `503 {"error":{"code":"sealed"}}` until you unseal. The seal
lifecycle is a three-state machine:

```
uninitialized ──init──▶ sealed ──unseal──▶ unsealed
                          ▲                    │
                          └──────seal──────────┘
```

For a **dev** instance the `make dev-up` / `dev-unseal.sh` flow above has
already done this for you. To do it by hand — or to understand the production
flow — the two commands are:

```sh
janus init --shares 5 --threshold 3   # Shamir default is 3-of-5; run once
janus unseal                          # submit one share per call, echo-off prompt
```

`janus init` prints the unseal shares **and a one-time initial-admin
credential** exactly once (that admin gets the instance-owner role). Distribute
the shares to separate custodians immediately — Janus never stores or re-shows
them. After a restart the server is sealed again; any 3 of the 5 custodians
resubmit a share to unseal. `janus seal-status` shows progress mid-ceremony.

AWS KMS auto-unseal, recovery from a mistyped share (`janus unseal --reset`),
and the full production ceremony are covered in
[operations.md](../operations.md).

## 3. First login

Log in with the bootstrap admin credential that `janus init` printed (for a
`make dev-up` instance, re-run `janus init` output is not re-shown — see the
seal lifecycle in [operations.md](../operations.md) for recovering the initial
admin). `janus login` prompts for the password and stores a session cookie in
`auth.json` (mode `0600`):

```sh
janus login --address http://127.0.0.1:8210   # prompts for email + password
```

Sessions have a 24h server-side TTL, so interactive users re-login daily.
Machine and CI callers skip `login` and instead present a **service token** via
`JANUS_TOKEN` — see [service-tokens.md](./service-tokens.md).

## 4. Create your first project, environment, config, and secret

Janus organizes secrets as **project → environment → config → secret** (a
Doppler-style hierarchy). Create one of each, then set a secret. The quickest
way is the CLI, binding the current directory to a config once with
`janus setup` and then writing:

```sh
cd my-service
janus setup --project my-service --env dev --config dev   # writes ./.janus.yaml
janus secrets set DATABASE_URL=postgres://localhost/app   # one new config version
janus secrets list                                        # masked KEY/ORIGIN/VERSION/UPDATED
```

Each save commits **one immutable config version** (the unit of diff and
rollback). `janus secrets list` is a masked, non-audited metadata read — it
never shows values. Creating the project/environment/config resources
themselves, batched edits, versioning, diff, and rollback are covered in
[managing-secrets.md](./managing-secrets.md).

## 5. Run your app with secrets injected

The flagship feature is `janus run`, which injects a config's secrets as
environment variables into a subprocess — no plaintext `.env` on disk. Bind a
directory once with `janus setup` (done above), then wrap your command after a
required `--`:

```sh
janus setup --project my-service --env dev --config dev   # once per directory
janus run -- ./my-service                                 # secrets → child env
```

`janus run` performs one **audited** bulk reveal, overlays the secrets onto the
parent environment (the **secret wins** on a name clash; `--preserve-env` flips
that), then execs the command. The child inherits stdin/stdout/stderr and its
exit code is propagated verbatim. Secret values only ever reach the child's
environment — never disk, never the CLI's own logs.

For CI/machine usage, `--preserve-env`, `--raw` (unresolved values), signal
handling, and exporting to files, see
[injecting-secrets.md](./injecting-secrets.md).

## Where to go next

| Guide | What it covers |
|---|---|
| [injecting-secrets.md](./injecting-secrets.md) | `janus run` in depth, CI flows, `download`, `--preserve-env`/`--raw` |
| [managing-secrets.md](./managing-secrets.md) | Project/env/config CRUD, batched saves, versioning, diff, rollback |
| [service-tokens.md](./service-tokens.md) | Minting scoped `janus_svc_…` tokens via the REST API / web UI |
| [github-actions.md](./github-actions.md) | OIDC-federated machine identity for GitHub Actions |
| [./kubernetes.md](./kubernetes.md) | Syncing a config to Kubernetes Secrets |
| [../cli.md](../cli.md) | Full `janus` CLI reference — every command, precedence, and flag |
| [../operations.md](../operations.md) | Server operations: seal lifecycle, env vars, auth/RBAC, audit |

Note: service tokens are minted via `janus token mint`, the REST API
(`POST /v1/tokens`), or the web UI — see
[service-tokens.md](./service-tokens.md).
