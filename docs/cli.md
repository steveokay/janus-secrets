# The `janus` secrets CLI

The same `janus` binary that runs the server is also the operator/developer
client. Alongside the sys commands (`server`, `init`, `unseal`, `seal-status`,
`seal`, `migrate` — see [operations.md](operations.md)) it provides the
secrets workflow: authenticate, bind a directory to a config, read/write
secrets, and — the flagship — inject a config's secrets as environment
variables into a subprocess (`janus run`). These commands consume the `/v1/`
REST API; there is no separate client binary.

## The mental model

Three independent pieces of state drive every secrets command:

1. **Where** to talk to — the server *address*.
2. **Who** you are — a *credential* (an interactive session or a service token).
3. **Which** config you mean — a *binding* to a project / environment / config.

Each is resolved from a small, fixed precedence chain (flags beat environment
beats stored/committed state), so the same command behaves predictably whether
a human runs it interactively or CI runs it headless. The three chains are the
core of this document; the commands themselves are thin.

Two output rules hold everywhere:

- **stdout is data only** — values, tables, and downloads. This is what makes
  `$(janus secrets get KEY)` and `janus secrets download > .env` clean.
- **All diagnostics and prompts go to stderr.** A secret value never appears in
  a log line or an error string; it can only reach stdout as explicit data, or
  the child's environment under `janus run`.

Any error exits non-zero.

## Command surface

```
janus login [--email E] [--address URL]                 # password prompt → store session
janus logout                                            # server logout (best-effort) + clear session
janus setup [--project P --env E --config C]            # validate + write ./.janus.yaml
janus secrets list [--json]                             # masked table (no reveal, no audit)
janus secrets get KEY [--version N]                     # print one value to stdout (audited)
janus secrets set KEY[=VALUE] [K2=V2 …] [--message M]   # batch = one config version
janus secrets delete KEY [K2 …] [--yes] [--message M]   # tombstones → new config version
janus secrets download --format env|json|yaml [--output PATH] [--plain]
janus run [--preserve-env] [--project P --env E --config C] -- <cmd> [args…]
```

The address/credential/binding flags — `--address`, `--token`, `--project`,
`--env`, `--config` — are accepted by every secrets/`run` command (and the
relevant subset by `login`/`logout`/`setup`).

## Credentials & storage

Two credential tiers, mirroring Doppler/Vault:

- **Session** (interactive humans): `janus login` prompts for email (or
  `--email`) and password (echo-off on a TTY, a plain read when piped), calls
  `POST /v1/auth/login`, and stores the returned `janus_session` cookie value.
  Sessions have a 24h server-side TTL, so interactive users re-login daily. The
  session is sent as `Cookie: janus_session=<value>`.
- **Service token** (CI / machines): `JANUS_TOKEN=janus_svc_…` (or `--token`),
  sent as `Authorization: Bearer …`. Service tokens are long-lived and scoped
  to a single config or environment with read or read/write access. Mint one
  with `POST /v1/tokens` (see [operations.md](operations.md)); it is shown once.

### Storage: `auth.json`

`janus login` writes the address, session cookie, and email to
`<config-dir>/auth.json`, file mode `0600` (directory `0700`):

```json
{ "address": "http://127.0.0.1:8200", "session": "<janus_session value>", "email": "me@corp.io" }
```

`logout` calls `POST /v1/auth/logout` (best-effort — network/expiry errors are
ignored) and removes `session` from `auth.json`, keeping the address/email.

The config directory is resolved by a single `configDir()` helper as
`os.UserConfigDir()/janus`:

| OS | Config directory |
|---|---|
| Linux | `$XDG_CONFIG_HOME/janus`, else `~/.config/janus` |
| macOS | `~/Library/Application Support/janus` |
| Windows | `%AppData%\janus` |

**`JANUS_CONFIG_DIR` overrides the whole path** when set. Use it to relocate
the CLI's state, or to isolate a test/CI run portably (it is honored on every
OS, whereas `XDG_CONFIG_HOME` is a no-op on Windows). Only `auth.json` lives
here; the directory binding lives in the working directory (below), not here.

### Credential precedence (per request)

| Order | Source | Sent as |
|---|---|---|
| 1 | `--token` flag | `Authorization: Bearer …` |
| 2 | `JANUS_TOKEN` env | `Authorization: Bearer …` |
| 3 | stored session (`auth.json`) | `Cookie: janus_session=…` |

A bearer token always wins over a stored session, so setting `JANUS_TOKEN` in a
CI job overrides any leftover login. If none is present and the route needs
auth, the server's `401` is rewritten to an actionable "run `janus login`"
message.

### Address precedence

| Order | Source |
|---|---|
| 1 | `--address` flag |
| 2 | `JANUS_ADDR` env |
| 3 | `auth.json` `address` (written by `login`) |
| 4 | `http://127.0.0.1:8200` (default) |

## Directory binding & config resolution

A secrets command needs to know which config it operates on. That binding is
recorded per working directory in a committed `.janus.yaml`, written by
`janus setup`:

```yaml
project: acme-web
environment: dev
config: dev
```

The file holds **human slugs only** — no secret values — so it is safe to
commit; teammates who clone the repo inherit the binding. `project` and
`environment` are matched by **slug**; `config` is matched by **name** (the
config resource has no slug). `setup` resolves all three against the server
before writing, so a typo fails immediately rather than at first secret read.

`.janus.yaml` is read from the **current working directory only** — there is no
parent-directory walk in this version (documented non-goal). Run commands from
the directory you bound, or override with flags/env.

### Binding precedence (per field)

Each of project / env / config is resolved independently, so partial overrides
work (e.g. `--config prod` with project/env from the file):

| Order | Source |
|---|---|
| 1 | `--project` / `--env` / `--config` flags |
| 2 | `JANUS_PROJECT` / `JANUS_ENV` / `JANUS_CONFIG` env |
| 3 | `.janus.yaml` in the cwd |

If any of the three ends up empty, the command errors and points at
`janus setup`.

### Slug → config id resolution

The secret routes are keyed by a config uuid (`cid`). Every command resolves it
once per invocation by walking three list endpoints:

1. `GET /v1/projects` → match the project **slug** → `pid`
2. `GET /v1/projects/{pid}/environments` → match the env **slug** → `eid`
3. `GET /v1/projects/{pid}/environments/{eid}/configs` → match the config
   **name** → `cid`

A miss at any level yields a distinct
"project / environment / config `<slug>` not found" error naming the level that
failed.

## Commands

### `janus login`

```bash
janus login --address http://localhost:8200          # prompts for email + password
janus login --email me@corp.io                       # email from flag, password prompted
echo 'hunter2' | janus login --email me@corp.io      # password piped (echo-off skipped)
```

Authenticates and stores the session in `auth.json`. The password prompt is
echo-off on a TTY and a plain line read when stdin is piped. Prompts and the
`Logged in as …` confirmation go to stderr. The bootstrap admin credential
printed by `janus init` is the first login.

### `janus logout`

```bash
janus logout
```

Revokes the session server-side (best-effort) and clears it from `auth.json`.
Accepts `--address` / `--token`.

### `janus setup`

```bash
janus setup --project acme-web --env dev --config dev   # non-interactive
janus setup                                              # prompts for each slug/name
```

Validates the project/env/config against the server (resolving to a `cid`) and,
only on success, writes `.janus.yaml` in the current directory. A validation
failure writes nothing. With flags omitted it prompts for each field. Accepts
`--address` / `--token`.

### `janus secrets list`

```bash
janus secrets list           # KEY  VERSION  UPDATED table
janus secrets list --json    # machine-readable
```

Masked metadata only — key names, per-key value version, and update time.
**No values are shown and the read is not audited** (it hits the masked
endpoint). `--json` emits the raw envelope for scripting.

### `janus secrets get`

```bash
DB_URL=$(janus secrets get DATABASE_URL)     # value only → command substitution
janus secrets get API_KEY --version 3        # a historical value version
```

Reveals one value (an **audited** `secret.reveal`) and prints the **raw value
only** to stdout, with no decoration, so `$(…)` capture is exact. `--version N`
fetches a historical value.

### `janus secrets set`

```bash
janus secrets set DATABASE_URL=postgres://…                    # inline (argv-visible)
janus secrets set A=1 B=2 C=3 --message "seed dev config"      # batched → one version
janus secrets set TOKEN <<<'s3cr3t'                            # value from stdin (safer)
janus secrets set TOKEN                                        # TTY: echo-off prompt
```

Value sources, in order: inline `VALUE` / `K=V` → piped stdin → echo-off TTY
prompt. Inline values are **visible in the process list and shell history** — a
documented caveat; prefer stdin or the prompt for real secrets. Multiple pairs
batch into a single `PUT …/secrets`, committing **one new config version** (the
versioning-correct unit of diff/rollback). `--message` sets that version's
message. The `Saved N secret(s) as vN` confirmation goes to stderr.

### `janus secrets delete`

```bash
janus secrets delete OLD_KEY                       # confirms on a TTY
janus secrets delete OLD_A OLD_B --yes             # skip the confirmation
```

Tombstones one or more keys, committing a new config version. Confirms on a TTY
unless `--yes`. Accepts `--message`.

### `janus secrets download`

```bash
janus secrets download --format env                          # → stdout (KEY=value lines)
janus secrets download --format json > config.json           # your own redirect
janus secrets download --format env --output .env --plain    # CLI writes the file (0600)
```

Bulk-reveals every value and serializes it:

- `env` → `KEY=value` lines; values are single-quoted/escaped for POSIX shell
  safety only when they need it.
- `json` → indented `encoding/json`, keys sorted.
- `yaml` → `gopkg.in/yaml.v3`, keys sorted, values always quoted.

**The `--plain` rule:** streaming to **stdout needs no flag** — a shell
`> file` redirect is your own act, not a file the CLI created. But `--output
PATH` makes the *CLI* write a plaintext file, so it **requires `--plain`**;
without it the command refuses (`refusing to write plaintext to <PATH> without
--plain`) and writes nothing. With `--plain --output`, the file is created mode
`0600`.

### `janus run` (flagship)

```bash
janus run -- ./my-service                    # secrets injected as env vars
janus run -- node server.js --port 3000      # args after -- pass through verbatim
janus run --preserve-env -- printenv DB_URL  # an existing DB_URL wins over the secret
janus run --config prod -- ./my-service      # override just the config for this run
```

1. Resolves the bound config (binding precedence above). No binding is a clear
   error pointing at `janus setup`.
2. One **audited** bulk reveal (`GET …/secrets?reveal=true`) fetches every
   value.
3. Builds the child environment: it starts from the parent `os.Environ()` and
   overlays the config's secrets. **By default the secret wins** on a name
   collision; `--preserve-env` flips that so an existing environment variable
   wins. Names present on only one side always pass through (`PATH`, `HOME`,
   etc. are never dropped).
4. Execs the command with that environment, inheriting this process's
   stdin/stdout/stderr.

**`--` is required** to separate `janus run`'s own flags from the command;
omitting it is a usage error. Secret values live only in the child's
environment — never written to disk, never echoed in the CLI's own output.

**Signals & exit code.** Received signals are forwarded to the child, and the
child's exit code is propagated verbatim (`janus run -- false` exits `1`).
Exit-code propagation is cross-platform; **signal forwarding is best-effort on
Windows**, which lacks the full POSIX signal set — a documented platform
limitation. Under a POSIX shell (including this repo's Bash on Windows), Ctrl-C
reaches the child as expected.

## Error handling

Server error envelopes surface as actionable CLI messages, with the common
auth/seal statuses rewritten:

| Status | Rewritten message |
|---|---|
| `401` | not authenticated — run `janus login` |
| `403` | access denied: … |
| `503` | server is sealed — unseal it first |

A missing binding points at `janus setup`; a slug-not-found names the failing
level (project / environment / config). Diagnostics never include a secret
value.

## Non-goals (this version)

Deliberately deferred, so their absence is not surprising:

- OIDC / browser login and CI JWT exchange (password + `JANUS_TOKEN` only for
  now).
- OS keychain storage (the `0600` `auth.json` is the store).
- Parent-directory walk for `.janus.yaml` (cwd only).
- A global path-map directory binding (dropped in favor of the committed
  `.janus.yaml` plus flags/env as the single source of truth).
- Shell completions; sync / rotation / `.env` import (Phase 3).
