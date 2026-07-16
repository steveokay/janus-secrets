# Injecting secrets into your app

This guide covers the practical problem of getting a config's secrets
*into a running application* — as environment variables, or, when you must,
as a file. The star of the show is `janus run`, which fetches a config's
resolved secrets and hands them to your process without ever touching disk.

For the full command reference (every flag, precedence table, and error
message) see [../cli.md](../cli.md); this guide is the task-oriented
companion.

## The recommended path: `janus run`

```sh
janus run -- ./my-service
```

`janus run` does four things, in order:

1. **Resolves the config** you are bound to (see
   [Selecting the config](#selecting-the-config) below). No binding is a
   clear error pointing you at `janus setup`.
2. **Fetches the secrets** with a single audited bulk reveal
   (`GET /v1/configs/<id>/secrets?reveal=true`). Values are **resolved** by
   default — config inheritance and `${...}` references are applied before
   injection.
3. **Builds the child environment** by overlaying those secrets on the
   parent environment (precedence below).
4. **Execs your command** with that environment, wiring through this
   process's stdin/stdout/stderr, forwarding signals to the child, and
   propagating the child's exit code verbatim.

The secret values live only in the child process's environment. They are
never written to disk and never echoed in the CLI's own output.

### `--` is required

Everything after `--` is your command and its arguments, passed through
verbatim. Everything before `--` is parsed as `janus run`'s own flags.
Omitting `--` is a usage error.

```sh
janus run -- node server.js --port 3000      # `--port 3000` goes to node
janus run -- python -m gunicorn app:app      # python module invocation
janus run -- ./my-service --verbose          # any generic binary
```

### Signals and exit code

Received signals are forwarded to the child, and the child's exit code is
propagated as `janus run`'s own exit code:

```sh
janus run -- false ; echo $?                 # prints 1
```

Exit-code propagation is cross-platform. Signal forwarding is best-effort
on Windows, which lacks the full POSIX signal set — a documented platform
limitation. Under a POSIX shell, Ctrl-C reaches the child as expected.

## Environment-variable precedence

By default, **a secret overrides a same-named variable inherited from the
parent environment**. Names present on only one side always pass through, so
`PATH`, `HOME`, and friends are never dropped.

Pass `--preserve-env` to flip the collision rule so the **parent's existing
value wins**:

| Flag | On a name collision, the winner is |
|---|---|
| *(default)* | the **secret** from the config |
| `--preserve-env` | the **existing parent** environment variable |

```sh
DB_URL=postgres://local janus run -- printenv DB_URL
# default          → prints the secret's DB_URL (secret wins)
# --preserve-env   → prints postgres://local  (parent wins)
```

`--preserve-env` is useful when a local override (say, a developer pointing
at a local database) should take precedence over the stored config for one
run.

### `--raw`: unresolved values (debugging only)

By default, values are **resolved**: config inheritance is applied and
`${projects.other.prod.KEY}`-style references are expanded before injection.

`--raw` injects the config's own **stored** values verbatim — references are
**not** resolved and inherited values are excluded. This is a debugging aid
(to see exactly what is stored), not a normal run mode; an app given raw
values will receive literal `${...}` strings where references were meant to
be.

```sh
janus run --raw -- ./my-service              # stored values verbatim, unresolved
```

## Selecting the config

`janus run` (and every `janus secrets ...` command) needs to know which
project / environment / config it operates on. There are three ways to say
so, resolved **per field** with this precedence:

| Order | Source |
|---|---|
| 1 | `--project` / `--env` / `--config` flags |
| 2 | `JANUS_PROJECT` / `JANUS_ENV` / `JANUS_CONFIG` env |
| 3 | `.janus.yaml` in the current working directory |

Because each field resolves independently, partial overrides work — e.g.
take project and environment from the file but override just the config:

```sh
janus run --config prod -- ./my-service      # project/env from .janus.yaml, config from flag
```

If any of the three ends up empty, the command errors and points you at
`janus setup`.

### `janus setup` writes `.janus.yaml`

The recommended way to bind a working directory is `janus setup`. It
validates the project/env/config against the server before writing anything,
then records the binding in a committable `.janus.yaml`:

```sh
janus setup --project acme-web --env dev --config dev   # non-interactive
janus setup                                             # prompts for each field
```

The resulting file holds **human slugs only — no secret values** — so it is
safe to commit. Teammates who clone the repo inherit the binding:

```yaml
project: acme-web
environment: dev
config: dev
```

`project` and `environment` are matched by slug; `config` is matched by
name. The file is read from the **current working directory only** — there
is no parent-directory walk — so run commands from the directory you bound,
or override with flags/env.

### Inline selection (CI and one-offs)

When there is no `.janus.yaml` (a CI runner, an ad-hoc shell), pass the
binding inline via flags or the `JANUS_*` environment variables:

```sh
export JANUS_PROJECT=acme-web JANUS_ENV=prod JANUS_CONFIG=prod
janus run -- ./my-service
```

## Authenticating the client

`janus run` talks to the server, so it needs a **server address** and a
**credential**.

**Humans** log in interactively; the session cookie is stored on disk:

```sh
janus login --address http://localhost:8200         # prompts for email + password
```

**Apps and CI** use a **service token** instead — either the `--token` flag
or the `JANUS_TOKEN` environment variable. See
[./service-tokens.md](./service-tokens.md) for minting a scoped token (it is
shown once, at creation).

### Credential precedence (per request)

| Order | Source | Sent as |
|---|---|---|
| 1 | `--token` flag | `Authorization: Bearer ...` |
| 2 | `JANUS_TOKEN` env | `Authorization: Bearer ...` |
| 3 | stored session (`janus login`) | `Cookie: janus_session=...` |

A bearer token always wins over a stored session, so setting `JANUS_TOKEN`
in a CI job cleanly overrides any leftover interactive login.

### Address precedence

| Order | Source |
|---|---|
| 1 | `--address` flag |
| 2 | `JANUS_ADDR` env |
| 3 | address saved by `janus login` |
| 4 | `http://127.0.0.1:8200` (default) |

A minimal headless invocation therefore needs only two variables plus the
binding:

```sh
export JANUS_ADDR=https://janus.internal
export JANUS_TOKEN=janus_svc_...
export JANUS_PROJECT=acme-web JANUS_ENV=prod JANUS_CONFIG=prod
janus run -- ./my-service
```

## The file-based fallback: `janus secrets download`

Some runtimes cannot be launched through `janus run` (an existing process
manager, a container image you do not control). For those, `janus secrets
download` serializes a config's resolved secrets in one of three formats:

```sh
janus secrets download --format env          # KEY=value lines
janus secrets download --format json         # indented JSON, keys sorted
janus secrets download --format yaml         # YAML, keys sorted, values quoted
```

Like `run`, download **resolves** references by default; pass `--raw` for
the stored values verbatim.

### It prints to stdout and refuses to write plaintext to disk

By default, `download` writes to **stdout only**. Redirecting that stream to
a file is your own act, so it needs no special flag — but making the *CLI*
write a plaintext file is a deliberate guardrail: `--output PATH` **requires
`--plain`**, or the command refuses and writes nothing:

```sh
janus secrets download --format env --output .env
# → refusing to write plaintext to .env without --plain

janus secrets download --format env --output .env --plain
# → writes .env at mode 0600
```

### The no-disk pattern

Prefer feeding the output straight into the consumer via a shell process
substitution, so the plaintext never lands on disk at all:

```sh
docker run --env-file <(janus secrets download --format env) my-image
```

This gives a container the same environment `janus run` would build, without
a `.env` file to leak or forget. For a fuller Docker walkthrough see
[./docker.md](./docker.md); for Kubernetes, see
[./kubernetes.md](./kubernetes.md).

## How the app consumes it

Nothing about the application needs to know Janus exists. Whether injected by
`janus run` or sourced from a downloaded `env` file, the secrets arrive as
**ordinary environment variables** and are read the usual way:

```sh
# Node
process.env.DATABASE_URL

# Python
os.environ["DATABASE_URL"]

# Go
os.Getenv("DATABASE_URL")
```

If your app expects a **file** rather than env vars (a `config.json`, a
`.env` it parses itself), write that file from a `download` and point the app
at it — but keep it out of version control and prefer a tmpfs / short-lived
path.

## Security notes

- **Revealing values is audited.** Both `janus run` and `janus secrets
  download` perform an audited bulk reveal; every read is recorded in the
  hash-chained audit log (key names and paths only — never values).
- **Never commit downloaded plaintext.** `.janus.yaml` is safe to commit
  (slugs only); a downloaded `.env`/`.json`/`.yaml` is not. Add such files
  to `.gitignore`, or avoid them entirely with the no-disk pattern above.
- **Prefer `janus run` over files.** Injecting directly into a child process
  keeps plaintext in memory only. Reach for `download --plain --output` only
  when a file is genuinely unavoidable, and use the tightest possible path
  and lifetime.
- **Inline values are visible.** Downloading and `run` never expose values on
  the command line, but `janus secrets set KEY=VALUE` does put the value in
  your shell history and process list — a separate caveat covered in
  [../cli.md](../cli.md).

## See also

- [../cli.md](../cli.md) — full `janus` CLI reference (all flags, precedence,
  errors).
- [./service-tokens.md](./service-tokens.md) — minting scoped service tokens
  for apps and CI.
- [./docker.md](./docker.md) — running containers with Janus-injected
  secrets.
- [./kubernetes.md](./kubernetes.md) — the Kubernetes Secrets sync
  integration.
- [../references.md](../references.md) — the inheritance and `${...}`
  reference model that resolution applies.
