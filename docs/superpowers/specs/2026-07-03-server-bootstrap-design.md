# Server Bootstrap (unseal-at-startup + sys API + janus CLI) — Design Spec

**Milestone 4 (Phase 1).** Date: 2026-07-03.

## Goal

Turn Janus from a set of Go packages into a runnable server. `janus server`
boots against Postgres, auto-migrates, and serves an HTTP sys API through which
an operator initializes the seal (Shamir shares or AWS KMS) and unseals the
in-memory keyring. All non-sys routes return 503 while sealed. `janus init`,
`janus unseal`, `janus seal-status`, and `janus seal` are thin CLI wrappers over
the sys API.

After this milestone: `docker compose up` + `make dev-up` yields a running,
unsealed server whose `secrets.Service` is live — ready for the auth and REST
API milestones to add routes.

## Scope

**In scope:**

- New package `internal/api`: chi router, `/v1/sys/*` handlers,
  `RequireUnsealed` middleware, request logger, project-wide JSON error
  envelope.
- `cmd/janus` restructured onto cobra: `server`, `init`, `unseal`,
  `seal-status`, `seal`, `migrate`, `version`.
- Server startup: env config, auto-migrate, seal-config discovery, unsealer
  construction (Shamir + AWS KMS), KMS auto-unseal at boot, graceful shutdown.
- Sys endpoints: health, seal-status, init, unseal, unseal/reset, seal.
- Dev workflow: compose wiring (healthcheck, `command: server`),
  `scripts/dev-unseal.sh` + `make dev-up` using 1-of-1 Shamir.
- New dependencies: `github.com/go-chi/chi/v5`, `github.com/spf13/cobra`,
  `golang.org/x/term` (echo-off share prompt). All sanctioned by CLAUDE.md's
  tech-stack section (chi, cobra) or the `golang.org/x` family.

**Out of scope (deferred, documented):**

- TLS (terminate at a reverse proxy; single-host compose for now).
- Rate limiting on sys endpoints (CLAUDE.md requires it for auth endpoints,
  which arrive with the auth milestone; Shamir share brute-force is
  cryptographically infeasible regardless).
- Auth-gating `POST /v1/sys/seal` (see Security posture).
- Any secret-facing HTTP routes (REST API milestone, after auth/RBAC/audit).
- `kh` CLI (later milestone).
- Master-key or KEK rotation.

## Architecture

Three units:

### `internal/api` (new)

The HTTP surface. Owns the chi router, sys handlers, and middleware.

```go
type Config struct {
    ListenAddr string // JANUS_LISTEN_ADDR, default ":8200"
}

type Server struct {
    keyring  *crypto.Keyring   // sealed at construction
    unsealer crypto.Unsealer   // long-lived; Shamir or KMS
    service  *secrets.Service  // constructed once; seal-aware via keyring
    // internal: router, seal-config store handle, config
}

func New(cfg Config, kr *crypto.Keyring, u crypto.Unsealer,
    seals crypto.SealConfigStore, svc *secrets.Service) *Server // wires router + deps
func (s *Server) ListenAndServe(ctx context.Context) error // graceful shutdown on ctx cancel
```

Middleware, both established here and reused by every later milestone:

- `RequireUnsealed` — 503 + `{"error":{"code":"sealed",...}}` for every route
  **except** `/v1/sys/*`. (No non-sys routes exist yet; tests mount a probe
  route through it.)
- Request logger — logs method, path, status, duration ONLY. Never bodies:
  shares and (later) secrets transit request bodies.

Plus the JSON error envelope helper: `{"error":{"code":"...","message":"..."}}`
per CLAUDE.md API conventions.

### `cmd/janus` (restructured onto cobra)

- `janus server` — the long-running process; the only subcommand (besides
  `migrate`) that touches Postgres.
- `janus init` / `janus unseal` / `janus seal-status` / `janus seal` — thin
  HTTP clients against the sys API. Address resolution: `--address` flag >
  `JANUS_ADDR` env > default `http://127.0.0.1:8200`.
- `janus migrate` — existing direct-DB migration runner, now a cobra command.
- `janus version` — carried over.

### Existing packages

`internal/store` and `internal/secrets` are composed, not modified: the keyring
is created sealed, `secrets.Service` is constructed at boot (it already returns
`ErrSealed` pre-unseal), and the unsealer feeds the keyring on successful
unseal.

`internal/crypto` gains two small additions (with tests preserving its 100%
branch-coverage bar):

- `ShamirUnsealer.SubmittedShares() int` — read-only count of submitted shares, needed
  by `GET /v1/sys/seal-status` (today the count is only observable as a
  side-effect of `SubmitShare`).
- **1-of-1 seal support**: the vendored shamir library requires
  `threshold >= 2` (and `Combine` needs two parts), so the dev-workflow's
  1-of-1 seal is special-cased in `ShamirUnsealer` exactly as Vault does it —
  `Init` with shares=1/threshold=1 returns the master key itself as the single
  share (no polynomial split), and `Unseal` with a stored threshold of 1 takes
  the submitted share as the master-key candidate directly. The KCV check still
  runs in both paths, so a wrong share is still rejected.

## Configuration (env-only, 12-factor)

| Variable | Required | Meaning |
|---|---|---|
| `JANUS_DATABASE_URL` | yes (server, migrate) | Postgres DSN |
| `JANUS_LISTEN_ADDR` | no (default `:8200`) | HTTP listen address |
| `JANUS_SEAL_TYPE` | before first init | `shamir` or `awskms`; after init the stored type is authoritative and a conflicting env value is a fatal boot error |
| `JANUS_AWS_KMS_KEY_ARN` | for `awskms` | KMS key id/ARN (plus standard AWS SDK env for credentials/region) |
| `JANUS_ADDR` | no | CLI default server address |

No config file this milestone.

## Startup sequence (`janus server`)

1. Load env config; fail fast on missing `JANUS_DATABASE_URL`.
2. `store.Open` + ping; **auto-migrate** (embedded golang-migrate; its Postgres
   advisory lock makes concurrent boots safe). `janus migrate` remains for
   explicit/CI use.
3. Read stored seal config via `PostgresSealConfigStore`:
   - **Exists** → stored `Type` is authoritative. If `JANUS_SEAL_TYPE` is set
     and disagrees → fatal boot error (never guess around misconfiguration).
   - **Absent** → *uninitialized* state; `JANUS_SEAL_TYPE` (required then)
     selects which unsealer to build for the eventual init.
4. Build the sealed `crypto.Keyring` and the long-lived unsealer:
   `NewShamirUnsealer(store, 0, 0)` or `NewKMSUnsealer(store, awsClient)`.
   Shamir note: the ctor's shares/threshold only matter for `Init`, and those
   numbers arrive in the init *request* — so the **init handler constructs a
   short-lived `ShamirUnsealer` with the requested parameters** just for the
   `Init` call. No crypto-package changes.
5. **KMS auto-unseal:** if initialized and type `awskms`, attempt `Unseal` now;
   on success `keyring.Unseal(master)` then zeroize master. On failure (KMS
   unreachable, IAM): log the failure class only, stay sealed, keep serving —
   `POST /v1/sys/unseal` (empty body) retries.
6. Construct `secrets.Service(store, keyring)`.
7. Serve; SIGTERM/SIGINT → `http.Server.Shutdown` with ~10s drain.

## Seal lifecycle

`uninitialized → sealed → unsealed`, with the `Keyring` as the single source of
truth for sealed-ness (`Sealed()`; its mutex + `ErrAlreadyUnsealed` make the
unseal transition race-safe). Shamir share accumulation lives in the
`ShamirUnsealer` (already mutex'd). A bad submitted share poisons
reconstruction (Combine consumes all submitted shares); recovery is
`ShamirUnsealer.Reset()`, exposed as `POST /v1/sys/unseal/reset`.

`POST /v1/sys/seal` re-seals a running server (`keyring.Seal()` wipes the
master key) — an incident-response lever, included deliberately (see Security
posture for its interim auth story).

## Sys endpoint contract

All under `/v1/sys/`, all JSON, all exempt from `RequireUnsealed`. Shamir
shares travel as **hex strings**.

| Route | Request → Response |
|---|---|
| `GET /v1/sys/health` | → `200 {"status":"ok","initialized":b,"sealed":b}` — pure liveness, always 200 while the process is up (compose healthcheck) |
| `GET /v1/sys/seal-status` | → `200 {"initialized","sealed","type","threshold","shares","progress":{"submitted","required"}}` — `threshold`/`shares`/`progress` only for Shamir; `progress` only while sealed |
| `POST /v1/sys/init` | Shamir: `{"shares":5,"threshold":3}` (omitted → 3-of-5 default) → `200 {"type":"shamir","shares":["<hex>",...]}` — returned **exactly once**, never persisted/logged; server **stays sealed**. KMS: empty body → `200 {"type":"awskms"}` and the server **auto-unseals immediately**. Repeat init → `409 already_initialized`. `shares`/`threshold` present under KMS type → `400 validation` |
| `POST /v1/sys/unseal` | Shamir: `{"share":"<hex>"}` (one per call) → `200 {"sealed":b,"progress":{...}}`. Threshold reached → reconstruction + KCV check → keyring unsealed, master zeroized. Combine/KCV failure → `400 key_check_failed`, shares retained. KMS: empty body → retry auto-unseal |
| `POST /v1/sys/unseal/reset` | → `200 {"sealed":true,"progress":{"submitted":0,"required":k}}` — wraps `Reset()` |
| `POST /v1/sys/seal` | → `200 {"sealed":true}` |

**Error envelope** (project-wide from here on):
`{"error":{"code":"...","message":"..."}}`. Codes: `sealed`,
`not_initialized`, `already_initialized`, `invalid_share`, `duplicate_share`,
`not_enough_shares`, `key_check_failed`, `validation`, `internal`. Status
mapping: 400 share/validation errors and `not_initialized` · 409
`already_initialized` · 503 `sealed` (middleware) · 500 `internal` with a
generic message (internals never leak).

Handlers are thin translation layers; all seal logic stays in `internal/crypto`.

## CLI behavior

- `janus init [--shares N] [--threshold K] [--json]` — human output prints each
  share once with a "store these separately; they will not be shown again"
  warning; `--json` emits the raw response. Never writes shares anywhere.
- `janus unseal [--share <hex>]` — first queries `seal-status` to learn the
  seal type. `awskms` → sends the empty-body retry (no share input). `shamir` →
  takes the share from the flag, or from stdin: echo-disabled prompt when stdin
  is a TTY (`golang.org/x/term`), plain read when piped. Prints progress
  (`2/3 shares`) or `unsealed`.
- `janus unseal --reset` — wraps `POST /v1/sys/unseal/reset` (the recovery
  path the server's `key_check_failed` message points at).
- `janus seal-status` — pretty-prints status. `janus seal` — wraps sys/seal.
- `janus migrate`, `janus version` — carried over onto cobra.

## Dev workflow

- `docker-compose.yml`: app service gets `command: server`, healthcheck on
  `GET /v1/sys/health`, `depends_on` Postgres healthy.
- `scripts/dev-unseal.sh` (+ `make dev-up`): if uninitialized →
  `janus init --shares 1 --threshold 1 --json`, extract the single share to
  `.dev/janus-share` (gitignored, `chmod 600`), unseal with it; else unseal
  from the saved file. One command from cold start to unsealed.
- Documented dev-only: a share on disk is effectively the master key. Prod and
  dev share every code path; the only "dev mode" is a share count of 1 plus a
  helper script.

## Security posture

- **Zero key material in logs**: logger records method/path/status/duration
  only; handlers never log payloads. Enforced by an API-layer leak test.
- **Zeroization at the boundary**: master key zeroized immediately after
  `keyring.Unseal` (which copies); `InitResult.Shares` zeroized after hex
  encoding into the one-time response. Same best-effort discipline as
  `internal/secrets`.
- **`sys/seal` is unauthenticated until the auth milestone** — anyone with
  network reach can seal the server. Availability-only and fail-closed
  (confidentiality unaffected); acceptable for single-tenant compose; will be
  auth-gated when auth lands. Documented in code and README.
- Init/unseal being unauthenticated matches the Vault model: init races are
  guarded by `ErrAlreadyInitialized`, and unsealing requires valid shares.

## Testing

`internal/api` tests: `httptest` against the real stack (testcontainers
Postgres, real keyring/unsealers) plus a fake `KMSClient`. CLI tests run
against an `httptest` server.

1. **Full Shamir lifecycle (e2e):** boot in-process → health 200 → status
   uninitialized → init 3-of-5 (shares returned once) → 2 submissions still
   sealed with progress → duplicate share 400 → 3rd share unseals →
   `sys/seal` re-seals.
2. **Middleware both directions:** probe route behind `RequireUnsealed` → 503
   `sealed` before unseal, 200 after, 503 again after `sys/seal`.
3. **Poisoned-set recovery:** bad share among threshold → `key_check_failed` →
   `unseal/reset` → clean resubmission succeeds.
4. **KMS path:** fake client — init auto-unseals; pre-initialized boot
   auto-unseals; failing KMS at boot → sealed, empty-body retry succeeds after
   the fake recovers.
5. **Config/boot errors:** double init 409; stored type ≠ `JANUS_SEAL_TYPE`
   fatal; missing `JANUS_DATABASE_URL` fail-fast; auto-migrate on fresh DB.
6. **CLI plumbing:** init prints shares + warning (and `--json`); unseal via
   flag and via piped stdin; status formatting.
7. **Graceful shutdown:** context cancel → clean `ListenAndServe` return.
8. **Leak test:** drive init/unseal with captured logs; assert no share hex in
   logs or error responses.

## Verification gates

`go build`, `go vet`, `go test ./...` (all packages, testcontainers-backed),
`gosec` v2.27.1 with `-exclude-dir=internal/crypto/shamir` (0 issues),
`govulncheck` (0) — the same bar as Milestones 2–3. Toolchain stays pinned at
`go1.26.4`.
