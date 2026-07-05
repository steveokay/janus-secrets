# Milestone 7 — Hash-chained audit log — design

**Package:** `internal/audit` (new), `internal/store` (repo + migration `000004`),
`internal/api` (endpoints + retrofit). **Status:** design approved, pre-plan.

## Goal

An append-only, tamper-evident audit log. Every authenticated request that
performs a sensitive action writes an immutable event carrying actor, action,
resource path, result, IP, and timestamp, plus the SHA-256 of the previous
event (a hash chain). `GET /v1/audit/verify` walks the chain and reports
integrity; `GET /v1/audit/export` streams events as JSONL or CSV, filterable.
Audit-write failure fails the request. No secret value ever enters an event.

## Locked decisions (brainstorming)

1. **Recording mechanism — explicit per-handler.** Handlers call the audit
   recorder explicitly with a semantic action + precise resource, mirroring
   RBAC's explicit `Can()` checks. Only this can honor "revealing a secret
   audits, but a masked list view does not" and record real action names.
2. **Scope — retrofit every sensitive endpoint that exists today.** Token
   mint/revoke, user create/disable, member grant/revoke, `sys.seal`, and auth
   login/logout/password, plus denied (403) authz decisions. Establishes the
   `audit.Recorder` seam that future secret routes drop into.
3. **Atomicity — synchronous post-action.** The handler records the event right
   after the action succeeds, in its own advisory-locked transaction; if the
   audit write fails, the request returns `500`. Services (`auth`/`authz`/
   `secrets`) stay audit-blind (layering preserved). Residual risk: a process
   crash in the gap between the mutation commit and the audit insert leaves that
   one action unaudited — a documented caveat, not solved by same-transaction
   coupling (which would thread audit awareness through the service layer).
4. **Read auditing — none for masked reads; export self-audits.** Metadata
   reads (token/user/member LIST, `/v1/auth/me`) emit no events. `GET
   /v1/audit/export` IS recorded (bulk read of sensitive data). `GET
   /v1/audit/verify` is not.
5. **Chain serialization — Postgres transaction advisory lock (Approach 1).**
   Each append runs in one tx: `pg_advisory_xact_lock(<audit key>)` serializes
   appenders, read the head, compute in Go, insert; the lock releases at commit.
   No extra head table; Postgres is the chain's authority.

## Architecture & package layout

`internal/audit` sits beside `internal/authz`: it depends on a store
`AuditRepo` interface and `auth.Principal`, and is HTTP-free.

- **`internal/audit`** — `Event` type, `Recorder` (the seam handlers call), the
  canonical hash function, and `Verify` (the chain walk). The pure hash/verify
  logic is unit-testable without a DB.
- **`internal/store/audit.go` + migration `000004`** — crypto-blind `AuditRepo`:
  `Append` (the advisory-lock transaction + closure), and cursor-based
  iteration for verify and filtered export.
- **`internal/api/audit_handlers.go`** — `GET /v1/audit/verify`, `GET
  /v1/audit/export`, gated on `audit:read`. `s.record(...)` retrofitted into the
  existing token/user/member/seal/auth handlers. Denials captured centrally via
  an `s.authorize(...)` helper.
- **Wiring** — `Server` gains `audit *audit.Recorder`; `Boot` constructs it
  (`audit.New(store.NewAuditRepo(st))`) and passes it into `New`. Only the API
  layer records; everything below stays audit-blind.

The `audit:read` action already exists in the RBAC matrix (admin/owner), so the
endpoints' permission is in place.

## Data model — migration `000004`, table `audit_events`

Append-only: the repo exposes no update or delete.

| Column | Type | Notes |
|---|---|---|
| `seq` | `bigint PRIMARY KEY` | chain position, assigned by the engine as `head.seq + 1` inside the locked append (not a bare identity) |
| `occurred_at` | `timestamptz NOT NULL` | set in Go (`time.Now()`) so the hashed value equals the stored value |
| `actor_kind` | `text NOT NULL` | `user` / `service_token` / `anonymous` (anon only for a failed login) |
| `actor_id` | `text` (nullable) | `Principal.ID`; null for anonymous |
| `actor_name` | `text NOT NULL` | email or token name — non-secret display (`""` allowed) |
| `action` | `text NOT NULL` | namespaced verb (below) |
| `resource` | `text NOT NULL` | path/identifier; never a value; `""` for `sys.seal` |
| `detail` | `text` (nullable) | non-secret specifics: `role=developer`, `format=jsonl` |
| `result` | `text NOT NULL` | `success` / `denied` / `error` |
| `result_code` | `text` (nullable) | envelope code for denied/error (`forbidden`, `validation`, `invalid_credentials`) |
| `ip` | `text NOT NULL` | from `r.RemoteAddr` |
| `prev_hash` | `bytea NOT NULL` | 32 bytes; genesis (`seq=1`) = 32 zero bytes |
| `hash` | `bytea NOT NULL` | SHA-256 of this event (below) |

Indexes: PK on `seq`; a btree on `occurred_at` and on `action`/`result` to
support export filters. Action vocabulary: `token.mint`, `token.revoke`,
`user.create`, `user.disable`, `member.grant`, `member.revoke`, `sys.seal`,
`auth.login`, `auth.logout`, `auth.password_change`, `audit.export`.

### Hash canonicalization

```
hash = SHA256( "janus:audit:v1"
             ‖ prev_hash
             ‖ F(seq) ‖ F(occurred_at) ‖ F(actor_kind) ‖ F(actor_id)
             ‖ F(actor_name) ‖ F(action) ‖ F(resource) ‖ F(detail)
             ‖ F(result) ‖ F(result_code) ‖ F(ip) )
```

- Every string field is **length-prefixed**: a 4-byte big-endian length
  followed by its UTF-8 bytes — so no delimiter or field-boundary ambiguity
  (defeats injection of `‖`-like content). The three **nullable** fields
  (`actor_id`, `detail`, `result_code`) additionally carry a 1-byte presence
  flag before the length prefix (`0x00` = NULL, `0x01` = present), so `NULL` and
  `""` never hash to the same bytes.
- `seq` is 8-byte big-endian; `occurred_at` is unix-nanoseconds as 8-byte
  big-endian.
- The `"janus:audit:v1"` domain tag versions the scheme.

`Verify` recomputes with this exact function, so any field mutation, reorder, or
deletion is detected.

### Append mechanics (Approach 1 + the M3 closure pattern)

`store.AuditRepo.Append(ctx, compute)` opens a tx, runs
`pg_advisory_xact_lock(<audit key>)`, reads the head `(seq, hash)` (or
`(0, zeros)` when empty), then calls `compute(head) → row`. The closure lives in
`internal/audit`: it sets `seq = head.seq+1`, `prev_hash = head.hash`,
`occurred_at = now`, computes `hash`, and returns the row; the repo inserts it
and commits (releasing the lock). This mirrors the store's existing
encrypt-closure in `internal/secrets`, keeping the store logic-blind. Serialized,
deterministic, DB-authoritative.

## Write path

### The seam

`audit.Recorder.Record(ctx, entry) error` — `entry = {Actor, Action, Resource,
Result, ResultCode, Detail, IP}` — builds the compute-closure, calls
`AuditRepo.Append`, and returns any error synchronously. A `Server` helper
`s.record(r, action, resource, result, code, detail) error` pulls the
`Principal` (via `PrincipalFrom`) and `RemoteAddr` off the request and calls the
recorder. In unit-test servers `s.audit` may be nil (Boot always wires a real
recorder); `s.record` no-ops and returns nil when `s.audit == nil`, and the
`/v1/audit/*` routes register only when `s.audit != nil` — the same nil-seam
pattern `internal/authz` uses. The advisory-lock key is a single fixed
application constant (an arbitrary `int64` chosen at implementation).

### Fail-closed

After an action succeeds, the handler calls `s.record(...)`; a non-nil return
makes the handler write `500 {"error":{"code":"internal"}}`. The underlying
mutation may already have committed (the Q3 crash-window) — the request still
fails, so a client never sees success for an unrecorded action.

### Centralized denial capture

`s.authorize(w, r, action, resource, detail) bool` runs `s.can(...)`; on
`ErrForbidden` it records a `denied` event **and** writes the 403 and returns
false; on allow it returns true (the success event is recorded later, after the
action). Handlers move from `if err := s.can(...); err != nil { … }` to `if
!s.authorize(...) { return }` — one call site each, so every 403 is audited from
one place while successes remain explicit. The seal middleware records its own
`sys.seal` denial the same way. (A denial's own audit write failing still fails
the request with `500`.)

### Retrofit map

| Endpoint | action | success resource / detail | audited |
|---|---|---|---|
| `POST /v1/tokens` | `token.mint` | `tokens/{id}` | ✅ + denial |
| `DELETE /v1/tokens/{id}` | `token.revoke` | `tokens/{id}` | ✅ + denial |
| `POST /v1/users` | `user.create` | `users/{id}` | ✅ + denial |
| `POST /v1/users/{id}/disable` | `user.disable` | `users/{id}` | ✅ + denial |
| `PUT …/members/{uid}` | `member.grant` | `{scope}/members/{uid}`, `role=…` | ✅ + denial |
| `DELETE …/members/{uid}` | `member.revoke` | `{scope}/members/{uid}` | ✅ + denial |
| `POST /v1/sys/seal` | `sys.seal` | `""` | ✅ + denial |
| `POST /v1/auth/login` | `auth.login` | success (user actor) / **denied** (anonymous, attempted email as `actor_name`, `invalid_credentials`) | ✅ |
| `POST /v1/auth/logout` | `auth.logout` | — | ✅ |
| `POST /v1/auth/password` | `auth.password_change` | — | ✅ |
| `GET /v1/audit/export` | `audit.export` | `format=…,filters=…` | ✅ self-audit |
| `GET` list / `me` / `verify` | — | — | ❌ masked / meta |

Failed logins are audited (`denied`, anonymous actor, attempted email as
`actor_name`) for brute-force visibility — the attempted email is an input, not
a secret, and the separate audit log does not affect M5's byte-identical login
responses. Login success records the now-authenticated user as actor.

## `verify` & `export`

Both under `r.Route("/v1/audit", …)` behind `RequireAuth` + `audit:read`,
registered inside the existing `if s.auth != nil && s.authz != nil` block.

### `GET /v1/audit/verify`

Walks the chain in `seq` order via a batched cursor (never loads the whole log
into memory). For each event it recomputes `hash` with the canonical function
and checks: stored `hash` matches the recomputation (no field tampering) **and**
`prev_hash` equals the previous event's `hash` (no reorder/deletion).

- OK → `{"valid": true, "count": N, "head_seq": N, "head_hash": "<hex>"}`
- Broken → `{"valid": false, "count": N, "broken_at_seq": K, "reason": "hash_mismatch" | "chain_break"}`

Not self-audited. A future UI renders this as a "chain verified" badge.

### `GET /v1/audit/export`

Streams matching events, **chunked**, written row-by-row from a DB cursor so a
large log never buffers in memory.

- `?format=jsonl` (default) → one JSON object per line, `application/x-ndjson`;
  `?format=csv` → header + rows, `text/csv`. Both set `Content-Disposition` with
  a filename.
- Filters, AND-combined: `?from=` / `?to=` (RFC3339 on `occurred_at`), `?actor=`
  (matches `actor_id` OR `actor_name`), `?action=`, `?result=` (e.g.
  `result=denied` → denied-only).
- Each exported row includes `prev_hash` and `hash` (hex) so an exported file is
  independently verifiable offline.
- **Self-audit ordering:** `authorize` → `record(audit.export, detail=format +
  filter summary)` **before** streaming the body, so the export is recorded even
  if the client aborts mid-download; if that audit write fails, respond `500`
  before streaming any bytes. Then stream.

Invalid `format` / `from` / `to` / `result` → `400 validation`. Everything is a
full scan (admin operation, no pagination) — fine for single-tenant scale.

## Security, error handling & testing

### Value redaction (structural)

The `Event` type has **no value field** — a secret value cannot be recorded even
by accident. `resource` holds only paths/ids/key-names; `detail` is a fixed
vocabulary of non-secret specifics. The request logger stays body-free.

### Error handling

Audit-internal failures → `500 internal` (never leak DB or chain internals);
authz denials on `/v1/audit/*` → `403 forbidden` (recorded as a denial); bad
filter/format → `400 validation`. The project-wide `{"error":{code,message}}`
envelope throughout.

### Testing

- **Chain + verify happy path**: N appends → `valid`, `count == N`, contiguous
  seqs.
- **Tamper detection**: mutate a stored field → `valid:false`, `broken_at_seq`,
  `hash_mismatch`. Delete/reorder a middle event → `chain_break` at its
  successor. Genesis + single-event case.
- **Concurrency**: N goroutines append under the advisory lock → the chain is
  contiguous and `verify` passes (proves serialization).
- **Fail-closed**: an `AuditRepo` stub that errors on `Append` → the handler
  returns `500`.
- **Retrofit e2e**: login → mint token → grant member → seal → export → verify —
  the export contains the expected rows with correct actor/result, denied
  attempts show as `denied`, `verify` returns `valid`, and masked reads (token
  list, `/me`) produce no events.
- **Leak test**: a mutating flow asserts no secret-value string appears in any
  audit row or in export output.
- **Coverage**: `internal/audit`'s pure hash/verify logic held to **100%**
  statement coverage (like `internal/crypto` and `internal/authz`).
- **Gates**: `go build`/`go vet`/`go test ./...`, `gosec`
  (`-exclude-dir=internal/crypto/shamir`), `govulncheck`, testcontainers
  Postgres.

## Non-goals (scope discipline)

- Retention / pruning / rotation — append-only forever; pruning would break the
  chain (and require re-anchoring), out of scope.
- External log shipping / SIEM integration.
- Per-event digital signatures — hash chain only, per the project spec.
- Multi-tenant separation (single-tenant product).
- Secret-access audit events for the secret REST API — those routes don't exist
  yet; this milestone builds the seam and retrofits every sensitive endpoint
  that exists today, so those events are a one-line `s.record(...)` when the
  routes land.
