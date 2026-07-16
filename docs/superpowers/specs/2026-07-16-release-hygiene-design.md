# Release hygiene — design

**Status:** approved (brainstorm 2026-07-16)
**Tracker:** closes `gaps.md` §7.2 (OpenAPI), §7.3 (LICENSE), §7.4 (release machinery), §7.5 (production deployment guide); adds the `/v1/sys/version` endpoint noted in §4.x.
**Scope:** Make the repository releasable. Licensing, a hand-authored OpenAPI spec with a drift-guard test, goreleaser-based release machinery publishing binaries + a GHCR image on tag, and a production deployment guide. One small backend addition (`GET /v1/sys/version`); no migration.

## Overview

Janus is feature-complete across build Phases 1–3 plus the depth passes, but it cannot be released: there is no license (README says "Not yet chosen"), no machine-readable API description for ~90 endpoints, no versioned artifacts, and no production deployment guide. This effort closes those gaps so a self-hoster can pin and run a tagged version, and so the API is discoverable.

The work is four cohesive components toward one goal. The OpenAPI component is the bulk of the effort (endpoint-by-endpoint authoring) and the plan should sequence it accordingly, but all four ship in this spec.

## Goals

- Release Janus under **Apache-2.0**, correctly reconciling the vendored MPL-2.0 Shamir package.
- Publish a hand-authored **OpenAPI 3.1** description of every `/v1/` endpoint, kept in sync with the code by a **drift-guard test**.
- On a `git tag`, build and publish **multi-arch binaries** (GitHub Release) and a **multi-arch server image** (GHCR) via **goreleaser**, with version/commit/date injected into the binary.
- Ship a **production deployment guide**.

## Non-goals (YAGNI)

- **No per-file license headers** across the Go tree — top-level `LICENSE` + `NOTICE` is sufficient. Apache-2.0 recommends but does not require per-file headers, and adding them is ~200 files of churn for no functional gain.
- **No Swagger-UI serving / `/v1/openapi.json`** — the chosen approach is a hand-authored file plus a drift test, not an embedded interactive docs surface.
- **No Prometheus `/metrics`** (§7.6) and **no `JANUS_LOG_LEVEL`/format config** — separate, out of scope here.
- **No `CONTRIBUTING.md`** (§7.8) — separate, out of scope here.
- **No relicensing tooling / CLA** and no per-file copyright rewrites.
- **No changes to endpoint behavior** other than adding `GET /v1/sys/version`.

## Component 1 — Licensing (Apache-2.0)

**Files:**
- Create `/LICENSE` — the verbatim Apache License 2.0 text.
- Create `/NOTICE` — Apache-convention attribution: the project copyright line, plus a note that `internal/crypto/shamir/` is vendored under **MPL-2.0** (its own `internal/crypto/shamir/LICENSE` and per-file headers are retained unchanged; MPL-2.0 is file-level copyleft and compatible with Apache-2.0 distribution).
- Modify `README.md` §License — replace "Not yet chosen…" with an Apache-2.0 statement pointing at `LICENSE` and `NOTICE`, keeping the MPL-2.0 Shamir note.
- Modify `go.mod`/root as needed only if a license field is expected (none required).

**Rules:**
- Do not touch the vendored `internal/crypto/shamir/` license or headers.
- Do not add SPDX headers to other files (non-goal).

**Verification:** a trivial test/`grep` asserting `LICENSE` exists and README no longer says "Not yet chosen"; the Shamir `LICENSE` still present.

## Component 2 — OpenAPI spec + drift guard

**File:** `docs/openapi.yaml` — **OpenAPI 3.1.0**.

**Coverage — every `/v1/` route**, grouped by tag:
- **auth**: password login/logout, `GET /v1/auth/me`, OIDC login/callback/federate.
- **projects / environments / configs / secrets**: full CRUD, soft-delete + restore, `?reveal`, list/get/set/delete.
- **versions**: config version list, `GET /v1/configs/{cid}/versions/diff`.
- **tokens**: mint/list/revoke.
- **audit**: list, `GET /v1/audit/verify`, `GET /v1/audit/export`.
- **transit**: keys CRUD, encrypt/decrypt/sign/verify/rewrap/datakey, versioning.
- **rotation / sync / dynamic**: create/list/get/update/delete/act + runs + leases/roles.
- **oidc admin**: provider + CI federation config.
- **sys**: seal/unseal, backup/restore, health, **version** (new), metrics (reads-24h).
- **trash**: `GET /v1/trash`, restore/destroy.
- **promote / pipeline / kek**: promotion, pipeline config, project KEK rotate/rewrap/status.

**Components:**
- `securitySchemes`: `bearerAuth` (HTTP bearer, service token) and `sessionCookie` (apiKey in cookie) — applied per operation as the code requires.
- Reusable schemas: the error envelope `{ error: { code, message } }`; cursor-pagination query params (`limit`, `cursor`) and `next_cursor` response field; core metadata objects (Project, Environment, Config, SecretMeta, TokenMeta, AuditEvent, RotationPolicy, SyncTarget, DynamicRole, Lease, TransitKey, etc.).
- **Value-safety:** no secret values, tokens, or credentials in any `example`/`default`. Request bodies that carry secrets (e.g. `secrets set`, sync creds) describe the field as write-only with a placeholder, never a realistic value. Token-mint response documents that the raw token is returned exactly once.

**Drift guard:** `internal/api/openapi_drift_test.go`
- Build the same `chi.Mux` the server mounts (reuse the router constructor; if it requires dependencies, use lightweight fakes/nil where the routes don't need them, or a dedicated route-only constructor if one is cleanly extractable — decide during implementation, preferring reuse of the real registration).
- `chi.Walk` to collect `(method, pathTemplate)` pairs; normalize chi `{param}` (and any regex constraints `{id:[0-9]+}` → `{id}`) to OpenAPI path style.
- Parse `docs/openapi.yaml` (paths + methods).
- **Assert every registered route is present in the spec.** Fail with a clear diff listing missing/extra routes. Reverse direction (spec paths that don't exist in code) is also asserted so the spec can't document phantom endpoints.
- YAML parsing uses whatever YAML library is already in the module graph; if none is present, add `gopkg.in/yaml.v3` as a **test-only** dependency (confirm during planning).

This test runs in the existing `go test ./...` CI job — no new CI wiring for the guard itself.

## Component 3 — Release machinery

**Version injection**
- New package `internal/version` with exported `Version = "dev"`, `Commit = ""`, `Date = ""` (overridden at build via `-ldflags "-X internal/version.Version=… -X …Commit=… -X …Date=…"`).
- `janus version` prints `version commit date` (reads the compiled-in values; no server call). Reconcile with any existing `version` command — extend it, don't duplicate.
- **New endpoint `GET /v1/sys/version`** → `{ "version", "commit", "date" }`. **Requires authentication** (any valid principal) to avoid anonymous version fingerprinting; mounted next to the other `/v1/sys` routes. No secret content; returns build metadata only. Documented in the OpenAPI spec and covered by a handler test.

**`.goreleaser.yaml`**
- `builds`: `main: ./cmd/janus`, `goos: [linux, darwin, windows]`, `goarch: [amd64, arm64]`, `ldflags` injecting the three version vars (`{{.Version}}`, `{{.Commit}}`, `{{.Date}}`), `CGO_ENABLED=0`.
- `archives`: tar.gz (unix) / zip (windows); include `LICENSE`, `NOTICE`, `README.md`.
- `checksum`: `checksums.txt` (sha256).
- `dockers` + `docker_manifests`: build per-arch images from a slim **`Dockerfile.release`** that `COPY`s the goreleaser-built binary into a minimal base (distroless/static or alpine), tagged and pushed to `ghcr.io/steveokay/janus:{{.Version}}` and `:latest`, combined into a multi-arch manifest.
- `changelog`: generated from conventional-commit history, grouped (feat/fix/docs/…).

**Web-asset embedding (critical ordering):** the binary embeds `web/dist` via `go:embed`. goreleaser builds the Go binary directly (not via the multi-stage dev Dockerfile), so `web/dist` **must exist before** goreleaser runs. The release workflow builds web assets first (`npm ci && npm run build`), then runs goreleaser, which builds a binary that embeds the freshly-built assets and copies it into `Dockerfile.release`. The existing multi-stage `Dockerfile` (web→go) is unchanged and remains what `docker compose up --build` uses for local/dev.

**`CHANGELOG.md`** — Keep a Changelog format; an `## [Unreleased]` section plus a seeded `## [0.1.0]` entry summarizing Phases 1–3 (core secrets/versioning/RBAC/audit/CLI; transit + SPA + OIDC + metrics; rotation + sync + dynamic) and the recent hardening/UI-depth/CLI-control-plane work.

**`.github/workflows/release.yml`** — trigger `push: tags: ['v*']`.
- `permissions: { contents: write, packages: write, id-token: write }`.
- Steps: checkout (full history for changelog) → setup-go (pinned toolchain 1.26.5) → setup-node → `npm ci && npm run build` in `web/` → docker login to ghcr.io via `GITHUB_TOKEN` → `goreleaser/goreleaser-action` `release --clean`, env `GITHUB_TOKEN`.
- **PR CI addition** (`ci.yml`): a `goreleaser check` step (validates the config without building) so a broken `.goreleaser.yaml` fails PRs early. Optionally a `goreleaser build --snapshot --clean` smoke on one target — decide during planning (keep CI time reasonable).

## Component 4 — Production deployment guide

**File:** `docs/guides/production-deployment.md` (linked from `docs/README.md` and `README.md`).

Sections:
- **TLS termination** — the server is intentionally TLS-less; put a reverse proxy in front. Concrete Caddy and nginx examples terminating TLS and forwarding to Janus.
- **Configuration reference** — consolidated table of `JANUS_*` env vars (address/db/unseal/HTTP timeouts/tick intervals/etc.), drawn from the codebase.
- **Unseal in production** — Shamir (n-of-m ceremony) vs cloud-KMS auto-unseal; operational trade-offs; the server boots sealed and returns 503 until unsealed.
- **Running the image** — pull `ghcr.io/steveokay/janus`, compose/standalone example, Postgres alongside.
- **Sizing** — rough CPU/memory/connection guidance for the single-node + Postgres model.
- **Backup & Postgres durability** — points at `docs/ops/backup-restore.md` for the app-level key-preserving dump, plus a note on Postgres-level backups/WAL archiving for the underlying store.
- **Upgrades** — migrations run on startup; pin an image tag, back up first, roll forward; note there is no HA/rolling story (single node by design).
- **Monitoring** — health (`/v1/sys/health`) and version (`/v1/sys/version`) endpoints, reads-24h metric; explicit note that Prometheus `/metrics` is not yet available (§7.6).

## Testing

- **License:** existence + README content assertion.
- **OpenAPI:** the drift-guard test (routes ⇄ spec, both directions). No behavioral tests for the doc itself beyond structural parse.
- **Version:** `internal/version` default-value test; `GET /v1/sys/version` handler test (authenticated → 200 with the three fields; unauthenticated → 401/403); `janus version` CLI test asserting it prints the compiled-in value.
- **Release config:** `goreleaser check` in CI validates `.goreleaser.yaml`. No attempt to actually publish from tests.
- **Value-safety:** the existing leak test remains green; the new version endpoint and OpenAPI examples carry no secret values (reviewed).

## Security / value-safety

- `GET /v1/sys/version` returns only build metadata and is authenticated (no anonymous fingerprinting).
- The OpenAPI spec documents write-only secret-bearing fields with placeholders only — no real values, tokens, or credentials in examples.
- The release workflow authenticates to GHCR with the ephemeral `GITHUB_TOKEN` (OIDC-backed) — no long-lived registry secret stored in the repo.
- No secret material is embedded in binaries or images; the image ships the server + embedded static web assets only.

## Files summary

**Create:** `LICENSE`, `NOTICE`, `docs/openapi.yaml`, `internal/api/openapi_drift_test.go`, `internal/version/version.go` (+ test), `.goreleaser.yaml`, `Dockerfile.release`, `CHANGELOG.md`, `.github/workflows/release.yml`, `docs/guides/production-deployment.md`.

**Modify:** `README.md` (§License + deployment link), `docs/README.md` (link the new guide + openapi), `.github/workflows/ci.yml` (`goreleaser check`), `cmd/janus` version command (wire to `internal/version`), `internal/api` sys routes (mount `GET /v1/sys/version`).

**Do not touch:** `internal/crypto/shamir/` license/headers; the existing multi-stage dev `Dockerfile`; any endpoint behavior beyond adding the version route.
