# Contributing to Janus

Thanks for your interest in Janus — a self-hosted, single-tenant secrets
manager (one Go binary + PostgreSQL). This document covers how to build, test,
and submit changes. Please also read [CLAUDE.md](CLAUDE.md), which is the
authoritative description of the architecture, crypto rules, and non-goals.

## Prerequisites

- **Go** — the toolchain is pinned in [`go.mod`](go.mod) (`toolchain
  go1.26.5`). Use that version; CI builds and the security gates run against
  it (`GOTOOLCHAIN=go1.26.5`).
- **Node 20** — for the Svelte web UI under [`web/`](web/).
- **Docker** — the Go integration tests use
  [testcontainers](https://golang.org/x/) to spin up real Postgres, and the
  full stack runs via docker-compose.

## Build & run

```sh
make build          # build the web bundle, embed it, and build the janus binary
make dev            # prints the two-terminal hot-reload dev workflow
docker compose up   # full local stack: app on :8210, Postgres on :5433
make migrate        # apply migrations to a local db (server also auto-migrates on boot)
```

`make build` runs `npm ci && npm run build` in `web/`, copies the output into
`internal/web/dist/`, and compiles `./cmd/janus` with the assets embedded via
`go:embed`. There is **no Node server in production** — the SPA is served from
the Go binary.

The single `janus` binary is both the server (`janus server`) and the CLI
(`janus run`, `janus secrets …`). Only `JANUS_DATABASE_URL` is strictly
required to boot the server; see
[docs/operations.md](docs/operations.md) and
[docs/guides/production-deployment.md](docs/guides/production-deployment.md)
for the full environment-variable reference.

## Testing & the CI gates

Run the full suite locally before opening a PR:

```sh
make test           # go test -race ./...  +  web tests (npm run test -- --run)
```

Your change must pass **every** gate in
[`.github/workflows/ci.yml`](.github/workflows/ci.yml). All of these are
treated as build failures — a red gate blocks merge:

- **Build & vet** — `go build ./...` and `go vet ./...` are clean.
- **Tests** — `go test -race ./...` is green. Integration tests need Docker
  (they skip cleanly when it's absent, but CI has it). Web: `npm run check`
  (svelte-check + tsc) and `npm run build` succeed.
- **`internal/crypto` 100% coverage** — the crypto package requires **100.0%**
  statement coverage, including nonce-reuse and tamper (modified-ciphertext)
  cases. CI fails the build on anything less.
- **govulncheck** — `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`
  reports 0 findings.
- **gosec** — `go run github.com/securego/gosec/v2/cmd/gosec@v2.27.1
  -exclude-dir=internal/crypto/shamir ./...` exits 0.
- **No secret values in logs or errors** — a dedicated grep-based leak test
  asserts that no plaintext secret value ever appears in captured log output
  or error strings. Never log, wrap, or format a secret value; the audit log
  records key **names**/paths, never values.

Prefer **table-driven** unit tests. Add tests with the code — features and
bug fixes without tests will be asked to add them.

## Crypto rules (do not deviate without discussion)

- Symmetric encryption is **AES-256-GCM**; signing is **Ed25519**; password
  hashing is **Argon2id**; token hashing is **HMAC-SHA256** (store hashes,
  never raw tokens). Use **constant-time** comparison for all token/MAC checks.
- **Standard library `crypto/*` + `golang.org/x/crypto` ONLY.** Never
  implement crypto primitives yourself, and never add a third-party crypto
  library. The sole approved exception is the OIDC/JOSE stack
  (`github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2`,
  `github.com/go-jose/go-jose/v4`) for JWT/JWKS verification — see CLAUDE.md.
- When in doubt on a security decision, **stop and ask** rather than guessing.

## Database migrations

- Migrations live in [`migrations/`](migrations/) as
  `NNNNNN_name.up.sql` + `NNNNNN_name.down.sql` pairs, applied with
  `golang-migrate`. Every `up` needs a matching `down`.
- Numbers are **zero-padded, six digits, strictly increasing**. The latest is
  `000030`; **the next migration number is `000031`.**
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
