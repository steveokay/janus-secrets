# CLI control plane — design

**Status:** approved (brainstorm 2026-07-16)
**Tracker:** closes `gaps.md` §6 (CLI gaps)
**Scope:** CLI-only. New/extended `cmd/janus` command groups over **existing** REST endpoints. No server/API/migration changes.

## Overview

Today the `janus` CLI can operate secrets (`get`/`set`/`list`/`delete`/`run`)
and the Phase-3 engines (`rotation`/`sync`/`dynamic` all have their full
verb sets), but it **cannot bootstrap or manage the control plane**:
creating projects/environments/configs and minting service tokens are
web-UI-or-`curl`-only (as the `managing-secrets.md` guide notes). This
feature makes the CLI self-sufficient: you can stand up a project, add
environments and configs, and mint a scoped CI token entirely from the
command line, plus a few ergonomics (`whoami`, shell `completion`,
`secrets diff`).

Every backing endpoint already exists; this is a CLI surface layered over
them. No new backend code, no migration.

## Goals

- Bootstrap and manage projects, environments, and configs from the CLI.
- Full service-token lifecycle from the CLI (mint / list / revoke).
- `whoami`, shell `completion`, and a value-free `secrets diff`.
- Consistent with existing CLI conventions: slug addressing, binding-aware
  parent resolution, `--json` output, TTY confirmation on destructive ops.

## Non-goals (YAGNI)

- No new/changed server endpoints, handlers, or migrations.
- No changes to `rotation`/`sync`/`dynamic` commands — already complete.
- **Hard destroy** — `delete` maps to the soft-delete endpoints (restore-able).
  Hard destroy stays out of this round.
- No interactive wizards / prompts beyond the existing password/echo-off
  and TTY-confirm patterns.
- `secrets diff` shows **key names only**, never secret values.

## Conventions (apply to every new command)

- **Addressing:** humans reference projects/environments/configs by
  **slug/name**; the CLI resolves to the UUIDs the REST paths need, reusing
  the existing resolver (`resolveConfigID` / `resolveBinding`). Tokens are
  referenced by their `id` (from `token list`).
- **Parent resolution precedence:** `--project` / `--env` / `--config`
  flags win, else `JANUS_PROJECT` / `JANUS_ENV` / `JANUS_CONFIG`, else
  `.janus.yaml` — the same `resolveBinding` chain `secrets`/`run` use. For
  `create`, the parent must resolve (flag or binding) or the command errors
  pointing at `--project`/`janus setup`.
- **Address/auth:** every command takes `--address` (`JANUS_ADDR`) and
  `--token` (`JANUS_TOKEN`) via the existing `apiClient`; credentials are
  never logged.
- **Output:** default is a compact human table to stdout; `--json` emits
  the raw API object(s) for scripting. Confirmation/summary text goes to
  stderr so stdout stays pipeable.
- **Destructive safety:** `delete` and `token revoke` confirm on a TTY
  unless `--yes` is passed (mirrors `secrets delete`).

## Command surface

### `janus project` (extend the existing group)

The existing `janus project` group (KEK lifecycle: `rotate-kek`,
`rewrap`, `kek-status`) gains CRUD subcommands:

```sh
janus project create --slug acme --name "Acme"      # POST /v1/projects {slug,name}
janus project list                                   # GET  /v1/projects
janus project get acme                               # GET  /v1/projects/{pid}
janus project delete acme [--yes]                    # DELETE /v1/projects/{pid}  (soft)
janus project restore acme                           # POST /v1/projects/{pid}/restore
```

`get`/`delete`/`restore` accept a **slug**, resolved to `pid` via the
project list.

### `janus env` (new group; alias `environment`)

An environment is addressed by slug within a project.

```sh
janus env create --project acme --slug prod --name "Production"
   # POST /v1/projects/{pid}/environments {slug,name}
janus env list --project acme                        # GET  …/environments
janus env delete --project acme prod [--yes]         # DELETE …/environments/{eid} (soft)
janus env restore --project acme prod                # POST  …/environments/{eid}/restore
```

`--project` falls back to the binding when omitted.

### `janus config` (new group)

```sh
janus config create --project acme --env prod --name prod [--inherits-from base]
   # POST /v1/projects/{pid}/environments/{eid}/configs {name, inherits_from?}
janus config list --project acme --env prod          # GET  …/configs
janus config get   [--project --env] prod            # GET  /v1/configs/{cid}
janus config delete [--project --env] prod [--yes]   # DELETE /v1/configs/{cid} (soft)
janus config restore [--project --env] prod          # POST  /v1/configs/{cid}/restore
```

`--inherits-from <name>` is an optional base config **in the same
environment** (root/branch inheritance). `--project`/`--env` fall back to
the binding.

### `janus token` (new group)

Maps to `/v1/tokens`. Exactly one of `--config`/`--env` sets the scope.

```sh
janus token mint --name ci-deploy --config prod --access rw --ttl 24h
   # POST /v1/tokens
   #   { name, scope:{kind:"config"|"environment", id}, access:"read"|"readwrite", ttl_seconds? }
janus token list                                     # GET    /v1/tokens
janus token revoke tok_… [--yes]                     # DELETE /v1/tokens/{id}
```

Flag mapping for `mint`:
- `--config <slug>` → `scope.kind = "config"`, `scope.id = <cid>` (resolved).
- `--env <slug>` (with `--project`) → `scope.kind = "environment"`, `scope.id = <eid>`.
- `--access read|rw` → `"read"` | `"readwrite"`.
- `--ttl <duration>` (Go duration, e.g. `24h`) → `ttl_seconds` (omit for no expiry).
- `--name <string>` → `name`.

**Output (mint):** the raw token (`janus_svc_…`) prints **once to stdout**
so it is capturable (`TOKEN=$(janus token mint …)`); the id/scope/expiry
summary goes to stderr. `--json` emits the full response object
(`token`, `id`, `name`, `scope`, `access`, `expires_at`). This is the
only time the token is retrievable — same as the API/UI.

### `janus whoami` (top-level)

```sh
janus whoami            # GET /v1/auth/me → prints  kind  id  name
janus whoami --json     # {"kind":"user"|"service","id":"…","name":"…"}
```

### `janus completion` (cobra built-in)

```sh
janus completion bash|zsh|fish|powershell
```

Wired via cobra's generator; near-zero code. Root command gets
`ValidArgs`/completion enabled where cheap.

### `janus secrets diff <vA> <vB>` (extend the `secrets` group)

```sh
janus secrets diff 3 4                # GET /v1/configs/{cid}/versions/diff?a=3&b=4
janus secrets diff 3 4 --json
```

Config resolved from `--project/--env/--config` or the binding (like the
other `secrets` verbs). Renders a **value-free** summary — added / removed
/ changed **key names** only (the diff endpoint is value-free by design).
`--json` passes the raw diff object through.

## Resolution helpers (reuse, don't duplicate)

- Project slug → `pid`: fetch `GET /v1/projects`, match `slug`. (Add a
  small `resolveProjectID(slug)` helper if one doesn't already exist,
  mirroring `resolveConfigID`.)
- Env slug → `eid`: `GET …/environments`, match `slug`, within the
  resolved `pid`.
- Config name → `cid`: the existing `resolveConfigID(project, env, config)`.
- All resolvers surface a clear "no such project/env/config" error rather
  than passing an unresolved slug into a URL.

## Files

New in `cmd/janus/`:
- `env_commands.go` (+ `_test.go`)
- `config_commands.go` (+ `_test.go`)
- `token_commands.go` (+ `_test.go`)
- `whoami.go` (+ `_test.go`)
- `completion.go` (+ `_test.go`)
- `secrets_diff.go` (+ `_test.go`) — or fold into `secrets_cmd.go` if small.

Modified:
- `project_commands.go` — add the CRUD subcommands next to the KEK verbs.
- `main.go` — register `newEnvCmd`, `newConfigCmd`, `newTokenCmd`,
  `newWhoamiCmd`, `newCompletionCmd` (and `secrets diff` under the existing
  secrets group).
- A shared resolver file (e.g. extend `binding.go`/`secrets_cmd.go`) for
  `resolveProjectID`/`resolveEnvID` if not already present.

## Error handling

- Unresolved slug → a specific error (`no project "x"`, `no environment
  "y" in project "x"`, etc.), never a 404 from a malformed URL.
- API errors surface the server's `{error:{code,message}}` message via the
  existing `apiClient` error path; internals are not leaked.
- `token mint` requires exactly one of `--config`/`--env`; both or neither
  is a validation error before any network call.
- Destructive commands abort if a TTY confirmation is declined.

## Testing

- Table-driven unit tests per command file, matching the existing
  `*_commands_test.go` style: flag wiring, request body shape, output
  formatting (table + `--json`), and error cases (missing parent,
  bad `--access`, both/neither scope flag).
- Reuse the existing httptest-server harness used by `cli_e2e_test.go`
  where an end-to-end path adds value (e.g. `project create` → `env
  create` → `config create` → `token mint`).
- The existing `cli_leak_test.go` must continue to pass — assert `secrets
  diff` and `whoami` emit no secret values.
- `token mint` test: the raw token appears on **stdout**, the summary on
  **stderr**, and `--json` yields the full object.

## Security / value-safety

- Tokens are shown once (mint), never re-listable; `list` returns metadata
  only (id/name/scope/access/expiry) — the API never returns the raw token
  again.
- `secrets diff` is key-names-only; no plaintext values in output.
- Credentials (`--token`/`JANUS_TOKEN`, minted tokens) are never written
  to logs; only mint's stdout intentionally emits the token once.
- All authorization stays server-side (the endpoints enforce RBAC —
  e.g. `token.mint` needs `TokenMint` at the scope); the CLI adds no
  privilege.
