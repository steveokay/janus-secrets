# The `janus` secrets CLI

The same `janus` binary that runs the server is also the operator/developer
client. Alongside the sys commands (`server`, `init`, `unseal`, `seal-status`,
`seal`, `migrate`, `backup`, `restore` — see [operations.md](operations.md))
it provides the secrets workflow: authenticate, bind a directory to a config,
read/write secrets, and — the flagship — inject a config's secrets as
environment variables into a subprocess (`janus run`). It also provides the
**control plane** — creating/managing projects, environments, configs,
service tokens, cross-environment promotion, and the release pipeline order
— and thin CLI fronts for the Phase 3 engines (`rotation`, `sync`, `dynamic`,
documented in depth in [operations.md](operations.md) and `docs/ops/*.md`).
These commands consume the `/v1/` REST API; there is no separate client
binary.

> **Reading secrets from an application?** For programmatic, in-process reads
> (with an in-memory TTL cache) as an alternative to `janus run`, use the
> [Go client SDK](guides/go-sdk.md) (`sdk/go/`).

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
janus whoami [--json]                                   # show the authenticated principal
janus session list [--json]                             # your active sessions (current marked *)
janus session revoke <id> | --others                    # revoke one session, or sign out everywhere else
janus setup [--project P --env E --config C]            # validate + write ./.janus.yaml

janus secrets list [--json]                             # masked table + ORIGIN (no reveal, no audit)
janus secrets get KEY [--version N] [--raw]             # print one value to stdout (audited)
janus secrets set KEY[=VALUE] [K2=V2 …] [--message M] [--type T]   # batch = one config version
janus secrets delete KEY [K2 …] [--yes] [--message M]   # tombstones → new config version
janus secrets download --format env|json|yaml|files [--output PATH] [--plain] [--raw]
janus secrets diff <vA> <vB> [--json]                   # key names only, no values
janus secrets lock KEY / unlock KEY                     # promotion-protect a key on the bound config
janus run [--preserve-env] [--raw] [--project P --env E --config C] -- <cmd> [args…]

janus project create/list/delete/restore                # project CRUD (soft-delete + restore)
janus project rotate-kek/rewrap/kek-status <project-id> # project KEK lifecycle (owner-only)
janus env create/list/delete/restore                    # environments within a project (alias: environment)
janus config create/list/delete/restore                 # configs within an environment (+ --inherits-from)
janus token mint/list/revoke                            # service tokens, scoped to a config or environment
janus pipeline get/set <project-slug> [env-slug …]      # ordered environment pipeline for promotion
janus promote --to ENV [--all|--key K]                  # promote the bound config's secrets (direct or via request)
janus promote request/requests/approve/reject/cancel    # capability-gap promotion request/approval workflow
janus master-key status/rekey/rotate                    # master-key rotation (Shamir rekey ceremony / KMS)

janus rotation create/list/get/update/delete/rotate     # scheduled secret rotation policies (see operations.md)
janus sync create/list/get/update/delete/sync           # sync targets: GitHub Actions / Kubernetes (see operations.md)
janus dynamic roles …/creds/renew/revoke/leases         # dynamic Postgres credentials + leases (see operations.md)
janus notifications create/list/update/delete/test/deliveries  # outbound alerting channels (see operations.md)

janus import doppler/vault/aws-sm                       # one-shot inbound import → one config version (see guides/importing.md)
```

**Inbound import (`janus import`).** Read secrets from an external system —
Doppler (`doppler`), Vault KV v2 (`vault`), or AWS Secrets Manager (`aws-sm`) —
and write them into a target `--project/--env/--config` as **one config
version**. It is client-side over the existing API: Janus never stores the
source credentials (supplied via flags/env, never logged) and gains no new
endpoints. Runs as a **dry-run by default** — prints the key *names* + count
that would be imported (never a value) — until you pass `--confirm`; `--create`
provisions a missing target tree. Full per-source detail (credentials, mapping,
examples) is in [guides/importing.md](guides/importing.md).

**Resolution (`--raw`).** `get`, `download`, and `run` **resolve** config
inheritance and secret references by default (they consume values). Pass `--raw`
to get the stored value verbatim (unresolved `${...}`, own values only) — mainly
for editing or debugging. `secrets list` shows an `ORIGIN` column
(`own`/`inherited`/`overridden`). See [references.md](references.md) for the
inheritance and reference model.

The address/credential/binding flags — `--address`, `--token`, `--project`,
`--env`, `--config` — are accepted by every secrets/`run` command (and the
relevant subset by `login`/`logout`/`setup`). The control-plane commands
(`project`/`env`/`config`/`token`/`pipeline`/`promote`/`master-key`/`rotation`/
`sync`/`dynamic`) accept `--address`/`--token`, plus `--project`/`--env`/
`--config` where a resource needs disambiguating; they otherwise take
resource ids or slugs as positional arguments — see `--help` on each for the
exact flags. Full flag-by-flag detail for the seal lifecycle, backup/restore,
and the rotation/sync/dynamic engines lives in
[operations.md](operations.md) and `docs/ops/*.md`; this document focuses on
the secrets workflow and the project/env/config/token/promotion control plane.

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

### `janus whoami`

```bash
janus whoami          # principal, type, and any scoping
janus whoami --json   # machine-readable
```

Shows the authenticated principal (user email or service token name),
resolved with the same credential precedence as every other command
(`--token` > `JANUS_TOKEN` > stored session). Useful for confirming which
identity a script or CI job is about to act as before it touches secrets.

### `janus session`

```bash
janus session list                 # active sessions; the current one is marked *
janus session list --json          # machine-readable
janus session revoke <id>          # revoke one session by id
janus session revoke --others      # sign out every other session, keep this one
```

Self-service management of your own login sessions, over
`GET/DELETE /v1/auth/sessions`. `list` shows each session's IP, last-seen time,
and user-agent (non-secret metadata — no cookie or credential material is ever
returned); the session behind the current credential is flagged. `revoke`
deletes one of *your* sessions (another user's id is indistinguishable from a
missing one), and `--others` is the "log out everywhere else" action. The same
surface is available in the web UI under **Settings → Active sessions**.

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
janus secrets list           # KEY  ORIGIN  VERSION  UPDATED table
janus secrets list --json    # machine-readable
```

Masked metadata only — key names, `origin`, per-key value version, and update
time. **No values are shown and the read is not audited** (it hits the masked
endpoint). The `ORIGIN` column reflects config inheritance: `own` (defined only
here), `inherited` (defined only in a base config), or `overridden` (defined
here and in a base, this config's value winning). `--json` emits the raw
envelope for scripting. See [references.md](references.md).

### `janus secrets get`

```bash
DB_URL=$(janus secrets get DATABASE_URL)     # value only → command substitution
janus secrets get API_KEY --version 3        # a historical value version
janus secrets get DB_URL --raw               # stored value verbatim, unresolved
```

Reveals one value (an **audited** `secret.reveal`) and prints the **value only**
to stdout, with no decoration, so `$(…)` capture is exact. By default the value
is **resolved** — config inheritance and `${…}` references are applied. `--raw`
returns the config's own stored value verbatim (unresolved `${…}`), for editing
or debugging. `--version N` fetches a historical value (always raw — a past
version is a stored artifact). See [references.md](references.md).

### `janus secrets set`

```bash
janus secrets set DATABASE_URL=postgres://…                    # inline (argv-visible)
janus secrets set A=1 B=2 C=3 --message "seed dev config"      # batched → one version
janus secrets set DATABASE_URL postgres://…                    # KEY VALUE positional form
janus secrets set TOKEN <<<'s3cr3t'                            # value from stdin (safer)
janus secrets set TOKEN                                        # TTY: echo-off prompt
janus secrets set API_KEY=abc --type password                  # tag the type (display hint only)
```

Value sources, in order: inline `VALUE` / `K=V` → a lone `KEY VALUE` pair of
positional args → piped stdin → echo-off TTY prompt. Inline values are
**visible in the process list and shell history** — a
documented caveat; prefer stdin or the prompt for real secrets. Multiple pairs
batch into a single `PUT …/secrets`, committing **one new config version** (the
versioning-correct unit of diff/rollback). `--message` sets that version's
message. `--type string|password|json|ssh_key|certificate|note` tags every
key=value pair in the call with a display/handling hint (masking behavior,
generator, validation in the web UI); it is **not** a storage or crypto
distinction — omit it (empty) to default to `string`. The `Saved N secret(s)
as vN` confirmation goes to stderr.

Secret keys accept a flat filename-style charset (`[A-Za-z0-9._-]`, e.g.
`config.json` or `id_rsa.pub`), not just valid env-var names. A key that
isn't a valid environment-variable name (contains `.` or `-`, or starts with
a digit) is **skipped with a warning** by `janus run` and by
`secrets download --format env` — it can still be written to disk via
`--format files` (below) or read individually with `secrets get`. Such keys
also cannot be used in `${...}` references and are skipped (not synced) by
the GitHub Actions sync integration.

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
janus secrets download --format env --raw                    # stored values, unresolved
janus secrets download --format files --output ./secrets --plain   # one file per key
```

Bulk-reveals every value (**resolved** by default — inheritance + references
applied; `--raw` returns the config's own stored values verbatim) and
serializes it:

- `env` → `KEY=value` lines; values are single-quoted/escaped for POSIX shell
  safety only when they need it. Keys that aren't valid env-var names are
  skipped with a warning (see `secrets set` above).
- `json` → indented `encoding/json`, keys sorted.
- `yaml` → `gopkg.in/yaml.v3`, keys sorted, values always quoted.
- `files` → materializes each secret to `<output-dir>/<key>` (one file per
  key, mode `0600`), including filename-style keys that `env`/`json`/`yaml`
  would otherwise need no special handling for. `--output` is **required**
  with this format (it names the target directory, not a single file) and
  each resolved path is traversal-guarded to stay inside that directory.

**The `--plain` rule:** streaming to **stdout needs no flag** — a shell
`> file` redirect is your own act, not a file the CLI created. But `--output
PATH` makes the *CLI* write a plaintext file, so it **requires `--plain`**;
without it the command refuses (`refusing to write plaintext to <PATH> without
--plain`) and writes nothing. With `--plain --output`, the file(s) are created
mode `0600`. `--format files` always writes to disk, so it always requires
`--plain`.

### `janus secrets diff`

```bash
janus secrets diff 2 5              # what changed between config version 2 and 5
janus secrets diff 2 5 --json
```

Diffs two config versions **by key name only** (added / removed / changed) —
never prints a value, so it is safe to run against a shared terminal or paste
into a ticket. `--json` emits the machine-readable form.

### `janus secrets lock` / `janus secrets unlock`

```bash
janus secrets lock DATABASE_URL     # promotion-protect this key on the bound config
janus secrets unlock DATABASE_URL   # clear the protection
```

Marks (or clears) a key as promotion-protected on the bound config. A locked
key is skipped by `janus promote`/`promote request` even when selected via
`--all` or `--key`, preventing an environment-specific value (e.g. a prod-only
credential) from being accidentally overwritten by a promotion from a lower
environment. See [Promotion](#janus-promote) below.

### `janus run` (flagship)

```bash
janus run -- ./my-service                    # secrets injected as env vars
janus run -- node server.js --port 3000      # args after -- pass through verbatim
janus run --preserve-env -- printenv DB_URL  # an existing DB_URL wins over the secret
janus run --config prod -- ./my-service      # override just the config for this run
janus run --raw -- ./my-service              # inject stored values, unresolved
```

1. Resolves the bound config (binding precedence above). No binding is a clear
   error pointing at `janus setup`.
2. One **audited** bulk reveal (`GET …/secrets?reveal=true`) fetches every
   value. Values are **resolved** by default — inheritance and `${…}` references
   are applied before injection; `--raw` injects the config's own stored values
   verbatim.
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

## Control plane

The commands below manage the project → environment → config hierarchy
itself, service tokens, cross-environment promotion, and master-key rotation
— as opposed to the secrets workflow above, which operates on the single
config a directory is bound to. All accept `--address` / `--token` (same
credential precedence as the secrets commands); most also accept
`--project` / `--env` / `--config` to disambiguate a target when it isn't
given positionally and isn't picked up from `.janus.yaml`. Run
`janus <command> --help` (and `janus <command> <subcommand> --help`) for the
exact flags — the summaries here cover shape and intent, not every flag.

### `janus project`

```bash
janus project create --slug acme-web --name "Acme Web"
janus project list [--json]
janus project delete <slug> [--yes]      # soft-delete (restorable)
janus project restore <slug>
```

Project CRUD. Delete is a soft-delete; `restore` undoes it. Slugs are
immutable once created (there is no `project update`/rename via this CLI).

```bash
janus project rotate-kek <project-id>    # instant: fresh KEK version
janus project rewrap <project-id>        # re-wrap DEKs onto the current version (resumable)
janus project kek-status <project-id>    # current version + versions still holding DEKs
```

Owner-only project key-encryption-key (KEK) lifecycle. `rotate-kek` is
instant and bumps the KEK version; existing per-secret-version DEKs still
wrapped by the old version are re-wrapped lazily by `rewrap`, which is
resumable/idempotent and **never decrypts a secret value**. `kek-status`
shows the current version and any superseded versions still holding DEKs (so
you know whether a `rewrap` is needed). These take a project **id**, not a
slug.

### `janus env` (alias: `environment`)

```bash
janus env create --slug staging --name Staging --project acme-web
janus env list --project acme-web [--json]
janus env delete <slug> --project acme-web [--yes]   # soft-delete
janus env restore <slug> --project acme-web
```

Environment CRUD within a project. `--project` (or `.janus.yaml` /
`JANUS_PROJECT`) selects the project; the environment slug is a positional
argument for `delete`/`restore`.

### `janus config`

```bash
janus config create --name prod --project acme-web --env prod
janus config create --name feature-x --inherits-from prod --project acme-web --env prod
janus config list --project acme-web --env prod [--json]
janus config delete <name> --project acme-web --env prod [--yes]   # soft-delete
janus config restore <name> --project acme-web --env prod
```

Config CRUD within a project/environment. `--inherits-from` creates a branch
config that inherits from a base config in the **same** environment (see
[references.md](references.md) for the inheritance/merge model). Configs are
matched by **name** (they have no slug).

### `janus token`

```bash
janus token mint --name ci-deploy --env prod --access read [--config default] [--ttl 24h] [--json]
janus token list [--json]
janus token revoke <id> [--yes]
```

Service-token lifecycle. `mint` scopes a token to either an environment
(default) or a single config (`--config`), with `--access read|rw`, an
optional `--ttl` (no expiry if omitted), and prints the raw `janus_svc_…`
token **once** — only its HMAC is stored server-side, so it cannot be
recovered later; mint a new one and revoke the old if it's lost. `list`
shows metadata only (name, scope, access, created/expires) — never the
token value. See [Service tokens](guides/service-tokens.md) for the scoping
model in depth.

### `janus pipeline`

```bash
janus pipeline get <project-slug>                    # ordered env slugs
janus pipeline set <project-slug> dev staging prod    # set the order
```

Reads or sets a project's ordered release pipeline (e.g.
`dev → staging → prod`). The pipeline order is what `janus promote` and the
web UI use to determine valid "promote forward" targets and default
next-environment suggestions.

### `janus promote`

```bash
janus promote --to staging --all                       # promote every added/changed key
janus promote --to staging --key DATABASE_URL --key API_KEY
janus promote --to staging --all --dry-run              # print the diff, apply nothing
janus promote --to staging --all --create-target        # create the target config if missing
janus promote --to staging --all --include-removes       # also propagate deletions
```

Direct promotion of the **bound** config's secrets to a config in another
environment (same project, positioned via the pipeline or an explicit
target). Requires `secret:promote` on the target; keys marked
`secrets lock` are always skipped. `--dry-run` shows the diff without
applying. Values are re-encrypted under the target config's keys (a
same-project promotion never blob-copies ciphertext).

```bash
janus promote request --to staging --key DATABASE_URL --note "rotate creds"
janus promote requests --project acme-web [--mine] [--status pending]
janus promote approve <request-id>
janus promote reject <request-id> [--note "reason"] [--yes]
janus promote cancel <request-id> [--yes]
```

Request/approval workflow for users who lack `secret:promote` on the
**target** (four-eyes promotion): `request` files a value-free request for
specific keys (`--all` is rejected at the request stage — v1 requires
explicit `--key` selection); a holder of `secret:promote` on the target
`approve`s (applies immediately) or `reject`s it; the requester can `cancel`
a still-pending request. `requests` lists requests for a project, optionally
filtered to your own (`--mine`) or by `--status`.

### `janus master-key`

```bash
janus master-key status                       # version, unseal type, rekey progress
janus master-key rekey --share <hex>          # submit a share (repeat --share to threshold)
janus master-key rekey --cancel               # abort an in-progress ceremony
janus master-key rotate                       # single-step rotation (KMS seals only)
```

Online master-key rotation, owner-only. Under a **Shamir** seal this is a
proof-of-possession rekey ceremony: submit unseal shares (prefer piping over
`--share`, which is visible in shell history) until the threshold is met,
which re-wraps every project KEK under a freshly generated master key and
returns a **new** set of shares — the old shares stop working. Under an
**AWS KMS** seal, `rotate` does it in one authenticated call (no shares
involved). `status` shows the current master-key version and, during a
Shamir ceremony, how many shares have been submitted so far.

### `janus rotation`, `janus sync`, `janus dynamic`

Thin CLI fronts for the Phase 3 engines — scheduled secret rotation
(Postgres password / webhook rotators), one-way sync to GitHub Actions /
Kubernetes, and Vault-style dynamic Postgres credentials with a lease
manager. All three follow the same `create/list/get/update/delete` shape
plus an engine-specific action (`rotation rotate`, `sync sync`,
`dynamic creds`/`renew`/`revoke`/`leases`). Full flag reference, the SQL/
webhook templating rules, scheduler tick env vars, and runbooks live in
[operations.md](operations.md) and `docs/ops/rotation.md` /
`docs/ops/sync.md` / `docs/ops/dynamic.md` — this file doesn't duplicate
them.

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

Deliberately absent from the CLI itself, so their absence is not surprising:

- OIDC / browser login and CI JWT exchange for the **CLI** (password +
  `JANUS_TOKEN` only). OIDC human login and OIDC-federated CI machine
  identity exist, but only via the web UI / `POST /v1/auth/oidc/federate` —
  see [oidc.md](oidc.md) / [ci-federation.md](ci-federation.md) — not as a
  `janus login` flow.
- OS keychain storage (the `0600` `auth.json` is the store).
- Parent-directory walk for `.janus.yaml` (cwd only).
- A global path-map directory binding (dropped in favor of the committed
  `.janus.yaml` plus flags/env as the single source of truth).
- `.env` file import (secrets are set one-by-one or scripted with
  `secrets set`, not bulk-imported from a `.env` file, via this CLI).

`janus completion [bash|zsh|fish|powershell]` (shell completion scripts) and
the Phase 3 engines (`rotation`/`sync`/`dynamic`) are implemented — see the
Control plane section above.
