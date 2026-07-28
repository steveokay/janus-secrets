# Contributing to Janus

Thanks for your interest in Janus — a self-hosted, single-tenant secrets
manager (one Go binary + PostgreSQL). This document covers how to build, test,
and submit changes. Please also read [CLAUDE.md](CLAUDE.md), which is the
authoritative description of the architecture, crypto rules, and non-goals.

## Prerequisites

- **Go** — the toolchain is pinned in [`go.mod`](go.mod) (`toolchain
  go1.26.5`). Use that version; CI builds and the security gates run against
  it (`GOTOOLCHAIN=go1.26.5`).
- **Node 24** — for the Svelte web UI under [`web/`](web/). **22.18 is the
  floor**: the web unit tests are plain `node --test` over TypeScript and rely
  on native type stripping, so an older Node fails to run them at all.
- **Docker** — the Go integration tests use
  [testcontainers](https://golang.org/x/) to spin up real Postgres, and the
  full stack runs via docker-compose.
- **helm** (optional) — only needed for `make helm-test`, the chart gate.

## Build & run

`make help` lists every target. The common ones:

```sh
make build          # build the web bundle, embed it, and build the janus binary
make build-fast     # same, skipping 'npm ci' (only when node_modules is in step)
make dev            # prints the two-terminal hot-reload dev workflow
docker compose up   # full local stack: app on :8210, Postgres on :5433
make migrate        # apply migrations to a local db (server also auto-migrates on boot)
```

`make build` runs `npm ci && npm run build` in `web/`, copies the output into
`internal/web/dist/`, and compiles `./cmd/janus` with the assets embedded via
`go:embed`. There is **no Node server in production** — the SPA is served from
the Go binary. The binary is **version-stamped** with the same
`internal/version` ldflags goreleaser uses, so `janus version` on a local build
names the commit it came from instead of reporting `dev`. On Windows the output
is `bin/janus.exe` (Git Bash resolves `bin/janus` to it, so the scripts under
`scripts/` are unaffected).

The single `janus` binary is both the server (`janus server`) and the CLI
(`janus run`, `janus secrets …`). Only `JANUS_DATABASE_URL` is strictly
required to boot the server; see
[docs/operations.md](docs/operations.md) and
[docs/guides/production-deployment.md](docs/guides/production-deployment.md)
for the full environment-variable reference.

## Testing & the CI gates

Run the full suite locally before opening a PR:

```sh
make test           # root module + the nested modules + web typecheck + web tests
make ci             # every gate below, in CI's order — run this before pushing
```

`make test` covers **every** module, not just the root one: `go test ./...` does
not descend into a directory that has its own `go.mod`, so `sdk/go` and
`terraform-provider-janus` need their own runs (as do the TypeScript and Python
SDKs). `make test-modules` runs just those four if you only touched an SDK or
the provider.

Your change must pass **every** gate in
[`.github/workflows/ci.yml`](.github/workflows/ci.yml). Each has a Make target
that runs the identical command, so nothing has to wait for a push to fail —
`make ci` runs the lot. All of these are treated as build failures — a red gate
blocks merge:

- **Build & vet** (`make lint`) — `go build ./...` and `go vet ./...` are clean,
  in the root module **and** in each nested module.
- **Tests** (`make test`) — `go test -race -timeout 30m ./...` is green. The
  30-minute timeout is deliberate: `go test` defaults to 10 minutes and
  `internal/api` under `-race` sits right on that line, so on a slow machine the
  default produces a timeout panic that looks nothing like the clock it is.
  Integration tests need Docker
  (they skip cleanly when it's absent, but CI has it). Web: `npm run check`
  (svelte-check + tsc) and `npm run build` succeed.
- **The nested modules** (`make test-modules`) — `go-module (sdk/go)` and
  `go-module (terraform-provider-janus)` build, vet and test each own-`go.mod`
  module separately, because the root `./...` never reaches them. `sdk-ts`
  typechecks and tests the TypeScript SDK; `sdk-python` runs the Python SDK on
  **both 3.9 and 3.13** — 3.9 is the floor `requires-python` advertises, and
  testing only a modern interpreter would let a 3.10-only construct through and
  quietly break the compatibility the package promises.
- **`internal/crypto` 100% coverage** (`make cover`) — the crypto package
  requires **100.0%** statement coverage, including nonce-reuse and tamper
  (modified-ciphertext) cases. CI fails the build on anything less, and so does
  the target.
- **govulncheck** (`make vuln`) — `go run
  golang.org/x/vuln/cmd/govulncheck@latest ./...` reports 0 findings. The target
  sets `GOTOOLCHAIN` from go.mod, because `go run <tool>@latest` resolves in its
  own module context — go.mod's `toolchain` line does not reach it, so without
  the pin the scan runs against whatever stdlib your local `go` happens to be
  and a laptop one patch release behind reports a stdlib CVE this repo does not
  have.
- **gosec** (`make sec`) — `go run github.com/securego/gosec/v2/cmd/gosec@v2.27.1
  -exclude-dir=internal/crypto/shamir ./...` exits 0.
- **The Helm chart** (`make helm-test`) — `helm lint`, every seal mode renders
  its configured value, and an invalid or incomplete seal config fails at
  template time. The chart had no CI at all until three defects survived to a
  real deployment; each render **greps** for the value it set, because piping a
  render to `/dev/null` is exactly what hid a validator checking a values key
  that did not exist.
- **No secret values in logs or errors** — a dedicated grep-based leak test
  asserts that no plaintext secret value ever appears in captured log output
  or error strings. Never log, wrap, or format a secret value; the audit log
  records key **names**/paths, never values.

Prefer **table-driven** unit tests. Add tests with the code — features and
bug fixes without tests will be asked to add them.

The browser E2E suite (Playwright, `web/tests/e2e/`) is **not** part of CI —
`make e2e` brings up a throwaway stack on `:8231` under its own compose project,
runs the suite, and tears it down with its volume. It is **destructive by
design** (it runs the one-shot init ceremony and hard-destroys projects), so
never point it at your dev instance on `:8210`. First run needs the browser:
`cd web && npx playwright install --with-deps chromium`. See
[`web/tests/e2e/README.md`](web/tests/e2e/README.md).

## Crypto rules (do not deviate without discussion)

- Symmetric encryption is **AES-256-GCM**; signing is **Ed25519**; password
  hashing is **Argon2id**; token hashing is **HMAC-SHA256** (store hashes,
  never raw tokens). Use **constant-time** comparison for all token/MAC checks.
- **Standard library `crypto/*` + `golang.org/x/crypto` ONLY.** Never
  implement crypto primitives yourself, and never add a third-party crypto
  library. There are exactly **two** approved exceptions, both recorded in
  CLAUDE.md: the OIDC/JOSE stack (`github.com/coreos/go-oidc/v3`,
  `golang.org/x/oauth2`, `github.com/go-jose/go-jose/v4`) for JWT/JWKS
  verification, and `github.com/go-webauthn/webauthn` for passkey COSE-key
  parsing and attestation/assertion verification. The envelope, transit, and
  unseal crypto remain stdlib + `x/crypto`.
- When in doubt on a security decision, **stop and ask** rather than guessing.

## Database migrations

- Migrations live in [`migrations/`](migrations/) as
  `NNNNNN_name.up.sql` + `NNNNNN_name.down.sql` pairs, applied with
  `golang-migrate`. Every `up` needs a matching `down`.
- Numbers are **zero-padded, six digits, strictly increasing**. The latest is
  `000050`; **the next migration number is `000051`.** (Check `migrations/`
  rather than trusting this line — it is the one thing here that goes stale
  every time a migration lands.)
- A `CHECK` constraint that enumerates values the Go code also enumerates (sync
  providers, rotator types, notification channels) is guarded by
  `internal/api/schema_enums_test.go`, which introspects the live constraint and
  fails if the two ever diverge. Widen the constraint in the same migration that
  adds the value.
- SQL is executed only via **parameterized queries** in Go code — never
  string-concatenate user input into SQL. Validate all inputs at the API
  boundary.
- The server **auto-applies** embedded migrations at boot (golang-migrate
  takes a Postgres advisory lock, so concurrent boots are safe); `janus
  migrate` remains for explicit/CI use.

## Web UI (`web/`)

The SPA is the "Atrium" design system (Svelte 5 runes + TypeScript, hand-
written CSS, no Tailwind / component library). For any change under `web/`:

- **All** colors come from the CSS-variable tokens in
  `web/src/styles/tokens.css`; every change must render correctly in **both**
  themes (`daylight` and `nightwatch`). Never hardcode hex/palette values.
- No native browser dialogs — use `web/src/lib/dialog.svelte.ts` +
  `DialogHost`. Data flows through the typed client `web/src/lib/api.ts` and
  the rune stores. Revealed plaintext lives only in component state, never
  persisted.

## Commit & PR conventions

- **Branch** off `main`; don't commit directly to `main`.
- Use **Conventional Commit** style subjects, matching the existing history:
  `feat(scope): …`, `fix(scope): …`, `docs: …`, `chore: …`, `refactor: …`,
  `test: …`. Keep the subject imperative and under ~72 characters.
- Keep PRs focused; describe **what** changed and **why**, and call out any
  new environment variables, migrations, or security-relevant behavior.
- Respect the **non-goals** in CLAUDE.md (no HA/Raft, no PKI/CA, no
  multi-tenancy, no dynamic-secret backends beyond Postgres, etc.) — scope
  creep in those directions will be rejected.
- Ensure `make test` and every CI gate above are green before requesting
  review.

## License

By contributing you agree that your contributions are licensed under the
project's [Apache License 2.0](LICENSE).
