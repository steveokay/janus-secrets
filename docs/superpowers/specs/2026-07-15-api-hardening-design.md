# API Hardening — Pagination, Idempotency, Server Timeouts (gaps.md §4.2–4.4)

**Date:** 2026-07-15
**Status:** Approved (design)
**Closes:** gaps.md §4.2 (list pagination), §4.3 (idempotency), §4.4 (server hardening)

## Goal

Bring the REST surface up to the conventions CLAUDE.md already promises but the
code only partially delivers:

- **§4.2 — Cursor pagination** on the table-backed list endpoints (audit already
  has it; the rest return unbounded arrays).
- **§4.3 — Idempotency** on all mutating verbs via a client-supplied
  `Idempotency-Key`, generalizing the one-off promotion implementation.
- **§4.4 — Server hardening**: `http.Server` timeouts and a global request-body
  cap, both configurable via `JANUS_*` env vars.

All three are additive and backward-compatible. No web-client change is required
by any part of this work.

---

## Part 1 — Cursor pagination (opt-in)

### Behavior

Seven **table-backed** list endpoints gain optional keyset pagination:

| Endpoint | Handler | Store method | Owning table |
|---|---|---|---|
| `GET /v1/projects` | `handleProjectList` | `projects.List` | `projects` |
| `GET …/environments` | `handleEnvList` | `environments.ListByProject` | `environments` |
| `GET …/configs` | `handleConfigList` | `configs.ListByEnvironment` | `configs` |
| `GET /v1/tokens` | `handleTokenList` | `auth.ListTokens` | `service_tokens` |
| `GET /v1/users` | `handleUserList` | `auth.ListUsers` | `users` |
| `GET …/members` (×3 scopes) | `membersList` | `authz.ListMembers` | `role_bindings` |
| `GET /v1/transit/keys` | `handleTransitList` | `transit.List` | `transit_keys` |

**The merged secrets list (`handleSecretsList` / `ListSecretsMerged`) is
explicitly excluded** — it is a computed inheritance view, not a single-table
scan, and paginating a merged/overlaid keyspace is a separate concern (recorded
as a follow-up in gaps.md, not built here).

### Contract

- Query params: `?limit=<1-200>&cursor=<opaque>`.
- **No params → return every row, exactly as today.** This is the
  backward-compatible path: existing callers that send neither param observe no
  change whatsoever.
- `limit` present → return at most `limit` rows plus a `next_cursor`.
- Response envelope is unchanged except for one added optional field:
  `{"projects": [...], "next_cursor": "<opaque>|null"}`. Existing web callers
  that do `.then(r => r.projects)` ignore `next_cursor` — non-breaking.
- `next_cursor` is `null` when the page was not full (no more rows); otherwise it
  encodes the last returned row's keyset position.

### Keyset design

Unlike audit (which has a monotonic `seq`), these tables key on
`created_at DESC, id DESC` (id as a stable tiebreak for rows sharing a
timestamp). Both columns descend so a single SQL row-value comparison
(`(created_at, id) < (cur, cur)`) is a correct strict keyset — a mixed
`DESC, ASC` ordering would need a hand-written boolean and is avoided. The
cursor is an **opaque base64url token** encoding `{created_at, id}` — not a bare
integer — because the sort key is a composite. A shared codec keeps this
uniform:

```go
// internal/api/pagination.go
type pageParams struct {
	limit  int   // 0 = unbounded (no limit param supplied)
	cursor *pageCursor // nil = first page
}
type pageCursor struct {
	CreatedAt time.Time
	ID        string
}
// parsePageParams reads ?limit & ?cursor, validating limit ∈ [1,200] and
// decoding the opaque cursor. Missing limit → limit=0 (unbounded).
func parsePageParams(r *http.Request) (pageParams, error)
// encodeCursor / decodeCursor: base64url(JSON) round-trip, tamper → 400.
```

Store methods gain `(limit int, cur *pageCursor)`:

```sql
SELECT ... FROM projects
WHERE deleted_at IS NULL
  AND ($cursorSet = false OR (created_at, id) < ($curCreatedAt, $curID))
ORDER BY created_at DESC, id DESC
LIMIT $limit  -- omitted entirely when limit<=0
```

`limit<=0` builds the query with **no `LIMIT` clause** (the unbounded/legacy
path). Because both sort columns descend, the row-value comparison
`(created_at, id) < ($curCreatedAt, $curID)` is exactly the set of rows that
fall after the cursor in `ORDER BY created_at DESC, id DESC` (Postgres row
comparison is lexicographic ascending, and "after" in a fully-descending order
means strictly smaller). No hand-written boolean is needed.

> **Ordering note.** Current store methods use assorted `ORDER BY` (e.g.
> `created_at DESC`, or name). Switching to `created_at DESC, id DESC` is a
> deliberate, uniform stabilization. Any test asserting a specific order is
> updated to the new deterministic order.

### Indexes

Add a covering btree per table to keep keyset scans index-only-ish:
`(created_at DESC, id DESC)` (partial `WHERE deleted_at IS NULL` where the table
soft-deletes). One migration, additive.

---

## Part 2 — Generic idempotency middleware

### Scope

All mutating verbs — `POST`, `PUT`, `DELETE` (and `PATCH`) under `/v1` — honor a
client-supplied `Idempotency-Key` header via a single chi middleware. This
generalizes the promotion-only `PromotionIdempotencyRepo` into a shared
mechanism and **retires the bespoke promotion path** (the promote handler stops
doing its own claim/complete/release; the middleware covers it).

### The value-safety keystone: status-only, never store bodies

Several mutation responses contain **once-shown secrets**:

- `POST /v1/tokens` returns the plaintext `janus_svc_…` token, shown once.
- `POST …/dynamic/.../creds` returns a generated DB password, shown once.
- The master-key rekey path returns fresh Shamir shares, shown once.

A middleware that cached response **bodies** for replay would persist those
secrets into the idempotency table — a direct violation of the value-free rule.
Therefore the middleware **captures only the HTTP status code** (via the
existing `statusWriter`), never the body. The response body streams to the
client normally on the first call — so the caller still gets their token — but
**no body is ever written to storage.** Idempotency is value-free by
construction: no current or future endpoint can leak a value through it.

### Flow

```
if method not in {POST,PUT,DELETE,PATCH} OR no Idempotency-Key header:
    passthrough (zero overhead)
actor := principal from context   // authN already ran; nil principal → passthrough (will 401)
body := read + buffer request body; restore r.Body for the handler
hash := sha256(body)
endpoint := method + " " + routePattern      // e.g. "POST /v1/tokens"

claimed, existing := store.Claim(key, actor, endpoint, hash)
if claimed:
    sw := statusWriter{w}
    next.ServeHTTP(sw, r)                     // body streams to client
    if sw.status in 2xx:  store.Complete(key, actor, sw.status)
    else:                 store.Release(key, actor)   // allow retry of a failed op
else:
    if existing.endpoint != endpoint OR existing.request_hash != hash:
        409 conflict  "Idempotency-Key reused for a different request"
    else if existing.status == 0 (pending):
        409 in-progress  "a request with this Idempotency-Key is still in flight"
    else:  // completed
        header Idempotency-Replayed: true
        writeJSON(existing.status, {"idempotent_replay": true})
```

**Confirmed tradeoff:** a replayed (retried) request receives a minimal
`{"idempotent_replay": true}` body with the original status — **not** the
original payload (not the created project's id, and — correctly — not the token
again). The load-bearing guarantee (the operation executes at most once) holds.
Callers that need the resource read it back with a `GET`. This is the honest,
uniformly-safe behavior and is why promotion's richer applied-keys replay is
dropped.

### Claim-after-authN, not after-authz

The promotion handler claims *after* its authz check. A middleware runs before
the handler's per-resource authz. This is acceptable because:

1. The actor is scoped by **authenticated identity** (authN middleware already
   populated the principal); an unauthenticated request passes through and 401s
   without ever touching the table.
2. A request that reaches the handler and fails authz returns non-2xx →
   `Release` → the row is deleted → the key is immediately reusable. No
   poisoning.

### Storage

New generic table (migration 000020):

```sql
CREATE TABLE idempotency (
    idempotency_key text        NOT NULL,
    actor           text        NOT NULL,
    endpoint        text        NOT NULL,   -- "METHOD routePattern"
    request_hash    text        NOT NULL,   -- sha256(body) hex
    status_code     integer     NOT NULL DEFAULT 0,  -- 0 = pending
    created_at      timestamptz NOT NULL DEFAULT now(),
    completed_at    timestamptz,
    PRIMARY KEY (idempotency_key, actor)
);
```

`store.IdempotencyRepo` — `Claim(key, actor, endpoint, hash) (claimed, existing, err)`,
`Complete(key, actor, status)`, `Release(key, actor)` — mirrors the promotion
repo but stores `endpoint` + `status_code` instead of a response blob. The old
`promotion_idempotency` table + `PromotionIdempotencyRepo` are removed; the
migration drops the table (promotion is now covered generically).

> **Retention.** Rows accumulate. Out of scope for this spec (matches promotion,
> which never pruned): noted as a future follow-up (a periodic sweep of rows
> older than N days). Volume is bounded by client key reuse and is low for a
> single-tenant instance.

### Concurrency note

`Claim` is a single `INSERT … ON CONFLICT DO NOTHING`, so two concurrent
requests with the same key race cleanly: exactly one wins the claim and runs;
the loser reads the pending row and returns `409 in-progress`. Identical to the
promotion precedent.

---

## Part 3 — Server hardening

### Timeouts

`internal/api/server.go` `ListenAndServe` currently sets only
`ReadHeaderTimeout: 10s`. Add:

```go
srv := &http.Server{
	Addr:              s.cfg.ListenAddr,
	Handler:           s.router,
	ReadHeaderTimeout: 10 * time.Second,
	ReadTimeout:       s.cfg.ReadTimeout,   // default 30s
	IdleTimeout:       s.cfg.IdleTimeout,   // default 120s
	WriteTimeout:      s.cfg.WriteTimeout,  // default 0 (disabled)
}
```

- `ReadTimeout` (default **30s**) — bounds slow request-body delivery; combined
  with the body cap below, closes slow-body / slowloris-on-request vectors.
- `IdleTimeout` (default **120s**) — keep-alive hygiene.
- `WriteTimeout` (default **0 = disabled**) — **deliberately off by default**
  because `GET /v1/audit/export` streams JSONL/CSV and a fixed write timeout
  would truncate a large export mid-stream. Operators behind a proxy that
  already bounds response time can opt in. Documented in the config reference.

All three configurable via env, parsed in `cmd/janus/server.go` into
`api.BootConfig` (following the existing `JANUS_*` duration convention, e.g. the
idle-timeout precedent from the ops-hardening batch):

- `JANUS_HTTP_READ_TIMEOUT` (Go duration, default `30s`)
- `JANUS_HTTP_IDLE_TIMEOUT` (Go duration, default `120s`)
- `JANUS_HTTP_WRITE_TIMEOUT` (Go duration, default `0`)

### Request-body cap

A `bodyLimit` middleware wraps the request body in `http.MaxBytesReader` so
oversized bodies are rejected with `413`:

- Default **10 MiB**, configurable via `JANUS_HTTP_MAX_BODY_BYTES` (bytes;
  `0` = disabled).
- **`POST /v1/sys/restore` is exempt.** Restore streams a full instance backup
  (arbitrarily large) and enforces its own per-record 64 MiB bound. The
  middleware checks the route pattern and skips the wrap for that path.
- Slots into the middleware stack **after** `RequireUnsealed` and before route
  handlers, so it applies to every `/v1` write. `MaxBytesReader` only trips when
  the body is actually read, so GETs are unaffected.

---

## Testing

- **Pagination (store, testcontainers, table-driven):** first page, mid-cursor
  continuation, exact-boundary last page (full page → non-null cursor whose
  follow-up returns empty), empty table, and the **no-limit unbounded path
  returns all rows in the new deterministic order**. One test per store method
  (7). Handler tests assert the `next_cursor` field and 400 on bad
  limit/cursor.
- **Idempotency (api e2e):** first-call executes + persists status; replay
  returns `{"idempotent_replay":true}` + `Idempotency-Replayed: true`;
  same-key/different-body → 409 conflict; pending → 409 in-progress;
  non-2xx → Release → key reusable; concurrent same-key → exactly one executes.
- **Idempotency value-free (leak test):** issue a service token (and a dynamic
  cred) **with** an `Idempotency-Key`, then assert the plaintext token/password
  string appears in **no** column of the `idempotency` row — reinforces the
  status-only guarantee with a regression test.
- **Hardening:** `bodyLimit` returns 413 over cap and passes under; `/v1/sys/restore`
  is exempt (large body accepted); timeout values flow from env → `BootConfig`
  → `http.Server` (unit-assert the constructed server fields).
- **Gates:** `go test ./... -race`, `gosec` (CI excludes vendored
  `internal/crypto/shamir`), `govulncheck` under `GOTOOLCHAIN=go1.26.5`. No web
  changes expected; web build/smoke unchanged.

## Migrations

- **000020** — `idempotency` table (create) + drop `promotion_idempotency`;
  pagination covering indexes on the 7 tables. Down reverses both.
- Next free migration number after 000019 (master-key rotation).

## Out of scope (explicit)

- Paginating the merged secrets view (`ListSecretsMerged`).
- Idempotency-record retention/pruning sweep.
- Any web-client change (all three parts are server-side and backward-compatible).
- Per-endpoint response-body replay (rejected in favor of value-safe
  status-only replay).
