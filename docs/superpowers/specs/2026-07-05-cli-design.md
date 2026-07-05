# Milestone 9 — Secrets CLI Design

**Status:** approved (brainstorming), pending implementation plan
**Package:** `cmd/janus` (same binary as the server)
**Consumes:** the Milestone 8 `/v1/` REST API
**Phase:** 1 (final Phase-1 milestone — ends with `docker compose up`, create project, set
secrets, `janus run` works)

## 1. Goal

Ship the operator-facing secrets CLI on the existing `janus` binary: authenticate,
bind a directory to a project/env/config, read/write secrets, and — the flagship —
inject a config's secrets as environment variables into a subprocess (`janus run`).
No new binary; new cobra subcommands alongside the existing server/sys commands.

## 2. Command surface

All commands hang off the existing `newRootCmd()` cobra root in `cmd/janus/main.go`.

```
janus login [--email E] [--address URL]        # password prompt → store session
janus logout                                    # server logout (best-effort) + clear session
janus setup [--project P --env E --config C]    # validate + write ./.janus.yaml
janus secrets list [--json]                     # masked table (no reveal, no audit)
janus secrets get KEY [--version N]             # print one value to stdout (audited)
janus secrets set KEY [VALUE] [K2=V2 …] [--message M]   # batch = one config version
janus secrets delete KEY [K2 …] [--yes]         # tombstones → new config version
janus secrets download --format env|json|yaml [--output PATH] [--plain]
janus run [--preserve-env] [--config C] -- <cmd> [args…]   # flagship
```

Global/shared flags (persistent where sensible): `--address`, `--token`,
`--project`, `--env`, `--config`.

## 3. Credential model & storage

Two credential tiers, mirroring Doppler/Vault:

- **Session** (interactive humans): `janus login` prompts for email (or `--email`)
  and password (echo-off, reusing the existing `term.ReadPassword` pattern from
  `sys_commands.go:readShare`), calls `POST /v1/auth/login`, and stores the returned
  `janus_session` cookie value. Sessions are 24h TTL server-side, so interactive
  users re-login daily.
- **Service token** (CI/machine): `JANUS_TOKEN=janus_svc_…` (or `--token`), sent as
  `Authorization: Bearer …`. Service tokens are long-lived and scoped to a single
  config/environment.

**Storage:** `~/.config/janus/auth.json`, file mode `0600` (dir `0700`):

```json
{ "address": "http://127.0.0.1:8200", "session": "<janus_session value>", "email": "me@corp.io" }
```

The base is resolved once in a `configDir()` helper as `os.UserConfigDir()/janus`,
which is `~/.config/janus` on Linux (honoring `XDG_CONFIG_HOME`), `%AppData%\janus`
on Windows, and `~/Library/Application Support/janus` on macOS. Documented so it is
not surprising that Windows differs from the `~/.config/janus/` shorthand.

**Credential precedence per request:** `--token` flag → `JANUS_TOKEN` env → stored
session cookie. `--token`/`JANUS_TOKEN` are sent as `Authorization: Bearer`; the
session is sent as `Cookie: janus_session=<value>`. If none is present and the route
requires auth, the client returns a friendly "run `janus login`" error before making
the call.

**Address precedence:** `--address` → `JANUS_ADDR` → `auth.json.address` →
`http://127.0.0.1:8200`.

`logout` calls `POST /v1/auth/logout` best-effort (ignores network/expiry errors)
and removes `session` from `auth.json`.

## 4. Directory binding & config resolution

`.janus.yaml` in the working directory records the binding as human slugs (no
secrets — safe to commit; teammates who clone inherit it):

```yaml
project: acme-web
environment: dev
config: dev
```

`setup` resolves each slug against the server (confirming it exists) before writing,
so a typo fails immediately rather than at first secret read. When flags are omitted
it is interactive (prompts for each, listing available options).

**Binding precedence:** explicit `--project/--env/--config` flags → `JANUS_PROJECT`/
`JANUS_ENV`/`JANUS_CONFIG` env → `.janus.yaml` in the **cwd only** (no parent-dir walk
in v1 — documented). Partial override is allowed (e.g. `--config prod` with project/env
from the file).

**Slug → config uuid (`cid`) resolution** (secrets routes are keyed by `cid`):

1. `GET /v1/projects` → find the project whose slug matches → `pid`
2. `GET /v1/projects/{pid}/environments` → match env slug → `eid`
3. `GET /v1/projects/{pid}/environments/{eid}/configs` → match config slug → `cid`

Resolved once per command invocation. Each level emits a distinct
"project/environment/config `<slug>` not found" error on a miss.

## 5. Authenticated API client

New `cmd/janus/apiclient.go`. Like the existing unauthenticated `sysCall`, but:

- attaches credentials per §3 precedence,
- decodes the `{"error":{code,message}}` envelope into the existing `*apiError`,
- rewrites auth failures into actionable CLI messages (see §8).

Reused by every authenticated command. The existing `sysCall` stays unchanged for
the unauthenticated sys commands (`init`/`unseal`/`seal-status`/`seal`).

## 6. `janus run` (flagship)

1. Resolve config (binding precedence, §4) → `cid`. A `run` with no config bound is a
   clear error pointing at `janus setup`.
2. `GET /v1/configs/{cid}/secrets?reveal=true` → `{version, secrets:{KEY:value}}` —
   **one audited reveal** fetches every value.
3. Build the child environment: start from `os.Environ()`, overlay config secrets so
   the **secret wins** on a name collision. `--preserve-env` flips the overlay so an
   existing env var wins. Non-secret vars (`PATH`, etc.) always pass through.
4. Exec `<cmd> args…` via `os/exec` with the built env and the child inheriting our
   stdin/stdout/stderr. **`--` is required** to separate the command (cobra
   `ArgsLenAtDash`); its absence is a usage error.
5. **Signal + exit-code fidelity:** forward received signals to the child;
   propagate the child's exit code (`janus run -- false` exits 1). Windows signal
   forwarding is limited to platform support (documented); exit-code propagation is
   cross-platform.
6. Secret values live only in the child's environment — never written to disk, never
   emitted in the CLI's own log/error output.

## 7. `secrets` read/write verbs

- **`list`** — `GET …/secrets` (masked). Table to stdout: `KEY  VERSION  UPDATED`.
  Values never shown. `--json` for machine output. No audit (metadata only).
- **`get KEY [--version N]`** — `GET …/secrets/{key}` (audited reveal). Prints the
  **raw value only** to stdout (no decoration) so `$(janus secrets get KEY)` works.
  `--version N` fetches a historical value.
- **`set KEY [VALUE] [K2=V2 …]`** — value sources in order: inline `VALUE`/`K=V`
  (documented as argv-visible — a caveat, stdin/prompt is the safe path) → stdin when
  piped → echo-off TTY prompt. Multiple pairs batch into **one** `PUT …/secrets`
  (one config version — versioning-correct). `--message` sets the config-version
  message.
- **`delete KEY [K2 …]`** — one `DELETE …/secrets/{key}` per key (tombstones in new
  config versions). Confirms on a TTY unless `--yes`.

## 8. Error handling & output conventions

- **stdout is data only** (values, tables, downloads — pipeable). **All diagnostics
  and prompts go to stderr.** This makes `$(janus secrets get KEY)` and
  `janus secrets download > .env` clean.
- Envelope errors surface as `janus: <message> (<code>)`, with actionable rewrites:
  - `401`/unauthenticated → "session expired or missing — run `janus login`"
  - `403` → "you don't have access to `<resource>`"
  - `503`/sealed → "server is sealed — unseal it first"
- Missing binding → points at `janus setup`. Slug-not-found names the failing level.
- Any error is a non-zero exit.

## 9. `secrets download` + `--plain` guard

- `--format env|json|yaml` from the bulk reveal (`GET …/secrets?reveal=true`):
  - `env` → `KEY=value` lines, values quoted/escaped for shell safety
  - `json` → stdlib `encoding/json`
  - `yaml` → `gopkg.in/yaml.v3` (accepted minimal serialization dep — not a crypto
    library; the only stdlib-free path to YAML)
- Default streams to **stdout** — no `--plain` needed (not a persistent file the CLI
  created; a shell `> file` redirect is the user's own act).
- `--output PATH` makes the CLI write a file → **requires `--plain`**, else refuse:
  `refusing to write plaintext to <PATH> without --plain`. With `--plain --output`,
  the file is created `0600`.

## 10. Testing strategy

- **Unit (no server):** env-overlay merge (both override directions),
  `env`/`json`/`yaml` formatting + shell escaping, credential/address/binding
  precedence resolution, `.janus.yaml` read/write, `--plain` gate logic, `run` arg
  parsing (`--` handling).
- **E2E against a real server** (reuse the M8 testcontainers harness — server +
  Postgres): `login → setup → secrets set → get → list → run` round-trip; `run`
  injects into a real child process and propagates its exit code; `download` in each
  format; `--plain` refusal without the flag.
- **Leak test:** capture CLI stdout+stderr across `run`/`get`/error paths and assert
  no secret value appears in diagnostics. A value may appear only in the child env or
  on explicit stdout data — never in a log or error string. Mirrors the server-side
  leak-test discipline.
- Gates unchanged: `go build`/`go vet`/`go test ./...`, `gosec`
  (`-exclude-dir=internal/crypto/shamir`), `govulncheck` — all as build failures.

## 11. Non-goals (this milestone)

- OIDC / browser login and CI JWT exchange (deferred; password + `JANUS_TOKEN` only).
- OS keychain storage (file `0600` is the store; keychain is later hardening).
- Parent-directory walk for `.janus.yaml` (cwd only in v1).
- Global path-map directory binding (dropped in favor of the committed `.janus.yaml`
  plus flags/env — single source of truth).
- Sync / rotation / `.env` import (Phase 3).
- Shell completions.
