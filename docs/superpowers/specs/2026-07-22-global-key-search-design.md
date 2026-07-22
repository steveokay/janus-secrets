# Global key search — design

**Date:** 2026-07-22
**Status:** approved (all recommendations accepted 2026-07-22)

## Problem

"Where is `STRIPE_KEY` set?" is a daily question with no answer today. The
command palette indexes projects/envs/configs (structure the web client already
holds), but **secret key names** live server-side per config version and are
loaded on demand, so they aren't searchable. Add a cross-config search over key
**names** (metadata — never values), surfaced in the palette.

## Decisions

- **Names only, never values.** Key names are metadata (the same class as masked
  list views, which per CLAUDE.md read metadata and emit no audit event). Values
  are never read, returned, or logged. No audit event is emitted.
- **Deny-by-default.** A result is returned only for a config the caller can
  read (`SecretRead`). This is the security crux: without it, search would leak
  the existence of keys in projects/configs the caller can't see. Enforced
  server-side per config.
- **Substring, case-insensitive** match on the key name.
- **Bounded.** The SQL match set is capped (e.g. 200) before authz filtering, and
  at most 50 visible results are returned, so a broad query can't do unbounded
  work. When results are truncated the response says so.

## Backend

### Store: `SecretRepo.SearchKeys`

`SearchKeys(ctx, q string, limit int) ([]KeyMatch, error)` returns
`{ConfigID, Key}` for the **latest live config version of each config** whose key
matches `%q%` case-insensitively, excluding soft-deleted configs and keys removed
in the latest version. Mirror the "latest version per config" logic already in
`GetLatest`, but across all configs in one query (join the max-version config
version per config to its `secret_values`, `ILIKE` on `key`, `ORDER BY key,
config_id`, `LIMIT`). Parameterised; `q` escaped for `LIKE` (escape `%`/`_`).
Empty/whitespace `q` → no query, empty result.

### API: `GET /v1/search/keys?q=<substr>&limit=<n>`

- Requires auth (any authenticated principal; results are authz-filtered).
- Rejects `q` shorter than 2 chars (400) to bound fan-out.
- Calls `SearchKeys` (raw cap 200), then for each **distinct** `config_id` in the
  matches, resolves its scope (`resolveScopeResource("config", id)`) and checks
  `SecretRead` (deny-by-default; a config that errors or is denied is silently
  dropped — no oracle). Enriches surviving matches with project/env/config names
  + ids for navigation. Returns up to 50, plus `truncated: bool`.
- Response:
  ```json
  { "results": [
      { "key": "STRIPE_KEY",
        "project_id": "…", "project_name": "acme-web", "project_slug": "acme-web",
        "environment_id": "…", "environment_slug": "prod",
        "config_id": "…", "config_name": "prod" }
    ],
    "truncated": false }
  ```
- **No audit event** (metadata list view). No secret value anywhere in the path.
- Distinct-config authz results may be cached within the request to avoid
  re-checking the same config for multiple key hits.

Mounted at `GET /v1/search/keys` (new `/v1/search` group). Documented in
`docs/openapi.yaml` (drift test).

## Frontend

Extend `web/src/components/CommandPalette.svelte`:

- Add `api.searchKeys(q)` to `web/src/lib/api.ts` returning the typed result.
- When the palette query is ≥2 chars, **debounced** (~150 ms), call
  `searchKeys` and merge hits into a new **"Secret keys"** group in the palette
  (alongside the existing local Projects/Configs/Navigate/Actions groups). Each
  item: label = the key, sublabel = `project · env / config`; `run()` navigates
  to `/projects/{project_id}/configs/{config_id}` with the key preselected (a
  `?key=<name>` query param the config editor reads to pre-filter/scroll to the
  row — reuse the editor's existing key filter if present, else add a light
  read-through of the param).
- Async state handled cleanly: ignore stale responses (guard by the latest
  query), show nothing on error (palette must never break), never render a
  value. The local synchronous items remain instant; key results stream in.

## Testing

- **store** — `SearchKeys` matches substring case-insensitively; excludes
  soft-deleted configs and keys not in the latest version; respects the limit;
  `%`/`_` in `q` are treated literally; empty `q` → empty.
- **api** — `q<2` → 400; a reader sees only keys in configs they can read and
  NOT keys in configs they can't (the deny-by-default core, proven with two
  users at different scopes); no value in the response; `truncated` set when
  capped; no audit event written for a search.
- **leak** — a sentinel secret value never appears in any `/v1/search/keys`
  response or logs.

## Non-goals

- No value search (never).
- No fuzzy/ranked relevance beyond substring + simple ordering (YAGNI for S).
- No new index migration unless `EXPLAIN` shows a scan problem — `secret_values`
  already has key/config indexes; a trigram index is a follow-up only if needed.
- No CLI (`janus secrets` already lists per-config; global search is a UI affordance).
