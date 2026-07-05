# Config Inheritance + Secret References Design

**Status:** approved (brainstorming), pending implementation plan
**Package:** `internal/resolve` (new) + `internal/secrets` (raw-read port) +
`internal/api` (authorizer + audit + surface) + `cmd/janus` (CLI `--raw`)
**Phase:** 1 — the final open Phase-1 line item. M1–M9 are merged and the
CLAUDE.md finish line works; this adds the two deferred data-model features:
config inheritance resolution and read-time secret references.

## 1. Goal

Two read-time resolution features over the existing project → environment →
config → secret hierarchy:

1. **Config inheritance** — a config may inherit from a base config **within the
   same environment** (root config + branch configs, like Doppler). The
   `configs.inherits_from` column exists (added in M2) but resolution was never
   implemented. A branch's effective values are its base's values overlaid with
   its own (**child wins** per key).
2. **Secret references** — a secret value may embed
   `${projects.<project>.<env>.<config>.KEY}` (absolute) or `${KEY}` (same-config
   local), resolved at read time, transitively, with cycle detection.

Both are read-time composition over the config/secret graph. They compose:
inheritance is applied first (building a config's merged key set), then
references are expanded over the merged values.

## 2. Locked design decisions (from brainstorming)

- **Reference grammar:** absolute `${projects.<project>.<env>.<config>.KEY}`
  (all four coordinates explicit — no inference); local `${KEY}` (same config,
  post-inheritance key set). Nothing implicit.
- **Reference authorization:** **caller-authorized (strict)** — every referenced
  target requires the caller to independently hold `secret:read` on that target's
  config; a forbidden reference fails closed. No transitive privilege escalation.
- **Inheritance authorization:** **transparent** — reading a branch config does
  **not** require a separate grant on its base config. See §6.
- **Read default:** reveal reads **resolve by default**; `?raw=true` opts out and
  returns stored values verbatim.
- **Failure mode:** **atomic** — any unresolvable reference (missing, forbidden,
  cycle, depth) fails the whole read; no values are returned.
- **Audit:** **each dereferenced target** emits its own `secret.reveal` event, in
  addition to the primary read.

## 3. Architecture

A new `internal/resolve` package: a pure resolution engine — no HTTP, no crypto,
no direct `store`/`authz`/`audit` imports. It composes over two ports.

```go
// Coord addresses a config by human names, as written in a reference.
type Coord struct { Project, Env, Config string }

// RawReader returns a config's raw decrypted key→value map (values verbatim,
// ${...} intact) plus identity and inheritance parent. Implemented by
// internal/secrets (which owns crypto) using the store's slug/name lookups.
type RawReader interface {
    ReadRaw(ctx context.Context, coord Coord) (RawConfig, error)
    ReadRawByID(ctx context.Context, configID string) (RawConfig, error)
}

type RawConfig struct {
    ProjectID, EnvID, ConfigID string
    Project, Env, Config       string   // canonical names, for provenance/errors
    InheritsFrom               *string  // parent config id, if any
    Values                     map[string][]byte
}

// Authorizer answers the strict per-target check for references. Implemented by
// the API via authz.Engine + resolveScopeResource. nil at trusted call sites.
type Authorizer interface {
    CanReadSecrets(ctx context.Context, target RawConfig) error // ErrForbidden if denied
}

// Provenance records a distinct target config read via a reference (for audit).
type Provenance struct { ProjectID, EnvID, ConfigID, Path string }

type Resolver struct { reader RawReader; authz Authorizer /* nil ok */ }

func (r *Resolver) Resolve(ctx, rootConfigID string) (map[string][]byte, []Provenance, error)
func (r *Resolver) ResolveKey(ctx, rootConfigID, key string) ([]byte, []Provenance, error)
```

**Data flow (resolved reveal):**

```
API handler (has principal)
  → resolve.Resolver.Resolve(rootConfigID)
      → RawReader.ReadRaw           (internal/secrets: existing decrypt path)
      → inheritance merge up the InheritsFrom chain (child wins)
      → reference expansion: parse ${...}, ReadRaw target,
        Authorizer.CanReadSecrets(target), recurse (transitive)
      → cycle detection (inheritance chain + reference frames) + depth cap
      → atomic failure on any break
  ← (values, provenance)
  → API writes audit: secret.reveal (primary) + one per distinct provenance entry
  → API zeroizes plaintext, responds
```

`internal/secrets` gains `ReadRaw`/`ReadRawByID` (reuse its existing decrypt path
+ the store's `ProjectRepo.GetBySlug` / `EnvironmentRepo.GetBySlug` /
`ConfigRepo.GetByName` coordinate lookups). `internal/api` implements `Authorizer`
over `s.can` + `resolveScopeResource` and writes the audit events. The resolver
is unit-tested with fakes for both ports.

## 4. Resolution algorithm

**Inheritance (key-set merge).** Walk the `inherits_from` chain from the root
config up to its deepest ancestor, then merge deepest→child with **child wins**
per key. Constraints/failures:

- The base must be in the **same environment** as the child (CLAUDE.md).
- Cycle guard: track visited config IDs while walking `inherits_from`; a repeat →
  `ErrInheritanceCycle`.
- A base that is missing or soft-deleted → `ErrBrokenInheritance`.

Inheritance is computed **before** references, so a `${KEY}` in a branch config
can resolve against an inherited key.

**References (value expansion).** After the merge, scan each value string for
tokens:

- **Absolute** `${projects.<project>.<env>.<config>.KEY}` → the target config's
  **fully resolved** value for `KEY` (the target's own inheritance + references
  are resolved first — transitive).
- **Local** `${KEY}` → the *same* config's merged key set.
- Multiple tokens and literal text interleave and each is substituted in place,
  e.g. `postgres://${DB_USER}:${DB_PASS}@${projects.infra.prod.db.HOST}/app`.
- **Escape:** `$${` emits a literal `${`.

**Composition & termination.**

- Resolution is recursive: expanding an absolute reference re-enters the full
  resolve (inheritance-merge + reference-expand) for the target config.
- **Reference cycle detection** spans configs: a resolution stack of
  `(configID, key)` frames; revisiting a frame → `ErrReferenceCycle` (e.g.
  `A.X → B.Y → A.X`). Independent of the inheritance-chain guard.
- A **depth cap** (32 frames) is a backstop → `ErrReferenceDepth`.
- Unresolvable target (missing project/env/config/key) → `ErrUnresolvedReference`
  (message names the offending key + target path). Forbidden target →
  `ErrForbiddenReference`. Malformed token → `ErrBadReferenceSyntax`.

**Value/byte handling.** Values are treated as UTF-8 strings for scanning and
substitution. A value that is *exactly* one `${...}` token takes the target's
exact bytes (a binary/opaque secret passes through unchanged); a token embedded
in surrounding text splices the target's bytes into the string.

## 5. Authorization

- **References — strict.** For every config a reference dereferences
  (transitively), `Authorizer.CanReadSecrets(target)` verifies the caller holds
  `secret:read` on that target's project/env/config chain (API implements it via
  `authz.Engine` + `resolveScopeResource`). Denied → `ErrForbiddenReference` →
  atomic fail. Every successful deref is therefore an authorized read.
- **Inheritance — transparent.** Inheritance ancestors are **not** separately
  authorized; reading a branch reads its composed content under the caller's
  authority on the root config. See §6 for the rationale and its one visible
  consequence.
- `Authorizer` is an interface; trusted internal call sites (no principal) pass
  `nil` → checks skipped (mirrors the M7 nil-recorder seam). The API always
  supplies a real authorizer.

## 6. Why inheritance is transparent but references are strict

`inherits_from` is set at **config-create time** (requires `config` write in that
environment) and the base is always in the **same environment** as the branch.
Under Janus's RBAC scope-inheritance, an environment- or project-level grant
already covers both base and branch, so the only case a separate base-check would
bite is a **config-scoped grant on just the branch** — exactly the case where
inherited values *should* resolve (a service token scoped to the branch config
reading its own composed content). The reader does not choose the base; an admin
wired it structurally.

References are the opposite: **anyone with write** can author a
`${projects.other...}` token, so that path is caller-influenced and must be
checked strictly to prevent transitive privilege escalation.

**Recorded consequence:** a service token scoped to *only* a branch config can
read values inherited from its base config (same environment) without a grant on
the base. This is intended.

## 7. Audit

The resolver returns **provenance** = the set of **distinct** target configs read
via references (deduped per reveal). The API writes:

- one primary `secret.reveal` on the root config, and
- one `secret.reveal` per distinct referenced target config
  (`detail = "via reference from <root path>"`, resource = the target path).

Inheritance ancestors are **not** separately audited — they are part of the root
reveal. Recording stays synchronous and fail-closed: an audit-write failure fails
the request. No secret value ever enters an audit row (unchanged M7 invariant).

## 8. API & CLI surface

- **Masked list** `GET /v1/configs/{cid}/secrets` (no reveal): shows own **and
  inherited** keys, each with an `origin` marker (`own` / `inherited` /
  `overridden`), values masked. Metadata-only inheritance merge (no decryption),
  **not audited** — matches the existing masked-list rule.
- **Resolved reveal** `GET /v1/configs/{cid}/secrets?reveal=true` (default):
  merged + dereferenced values. Audited (primary + per-ref).
- **Raw reveal** `?reveal=true&raw=true`: the config's *own* stored values,
  verbatim `${...}`, unmerged/unexpanded — the editable truth for the SPA.
  Audited as a single primary reveal (no deref).
- **Single key** `GET /v1/configs/{cid}/secrets/{key}` resolves by default;
  `?raw=true` returns the stored value verbatim.
- **CLI:** `janus run`, `secrets download`, `secrets get` resolve by default
  (they consume values); a `--raw` flag returns verbatim. `secrets list` shows
  the `origin` markers.

## 9. Error handling

New `internal/resolve` sentinels, mapped to HTTP at the API boundary via
`writeServiceError`:

| Sentinel | HTTP | Cause |
|---|---|---|
| `ErrInheritanceCycle` | 409 | `inherits_from` chain loops |
| `ErrBrokenInheritance` | 409 | base config missing / soft-deleted |
| `ErrReferenceCycle` | 409 | `${...}` resolution revisits a `(config,key)` frame |
| `ErrUnresolvedReference` | 422 | target project/env/config/key not found |
| `ErrForbiddenReference` | 403 | caller lacks `secret:read` on a referenced target |
| `ErrReferenceDepth` | 422 | depth cap exceeded (backstop) |
| `ErrBadReferenceSyntax` | 400 | malformed `${...}` token |

All are **atomic**: the reveal returns the error and no values. Error messages
carry key names and target *paths* only — never a secret value. Zeroization
discipline is preserved on every error path (partial plaintexts wiped before
returning), matching `RevealConfig` today.

## 10. Testing

- **`internal/resolve` (pure, table-driven, fakes for both ports):** inheritance
  merge + child-wins override; multi-level chains; inheritance cycle; broken
  base; local `${KEY}`; absolute cross-config ref; transitive ref (ref→ref);
  reference cycle across configs; depth cap; `$${` escape; interleaved
  literal+refs; forbidden ref via a denying `Authorizer`; provenance correctness
  + dedup; atomic-failure + zeroization on each error.
- **`internal/api` (e2e, testcontainers):** resolved vs `?raw=true`; masked-list
  origin markers; audit trail shows primary + per-ref `secret.reveal` rows; a
  forbidden cross-project ref → 403 and the whole read fails; `janus run` injects
  a resolved value end-to-end.
- **Leak test:** a value containing a reference, plus the referenced target's
  value, never appear in logs or error bodies on the failure paths.
- **Full gate sweep** unchanged: `go build`/`go vet`/`go test ./...` (Docker),
  `gosec` (shamir excluded), `govulncheck` — all as build failures;
  `internal/crypto` + `internal/authz` + `internal/audit` stay 100%.

## 11. Non-goals (this milestone)

- Cross-environment or cross-project inheritance (inheritance stays
  same-environment; cross-boundary sharing is what references are for).
- Reference defaults / fallback syntax (e.g. `${KEY:-default}`) — YAGNI for now.
- Write-time reference validation UI / linting (references are validated at read
  time; a bad reference surfaces on reveal, not on write).
- Config-authorized (Doppler-like) transitive reads — explicitly rejected in
  favor of strict per-target authorization.
- Caching / memoization of resolved configs across requests (each reveal resolves
  fresh; add memoization only if a real hot path appears).
