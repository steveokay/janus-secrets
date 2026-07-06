# B3 — Audit Viewer (design)

- **Status:** APPROVED 2026-07-06 (queue order + autonomy per Steve: "push merge
  and start next tasks"; scope follows CLAUDE.md's audit-viewer definition).
- **Visual authority:** locked visual system; audit table follows the mockup's
  table conventions (Slice-1 editor treatment).
- **Tracker:** `fe-improvements.md` §8 (audit viewer with chain-verify badge and
  export) — the placeholder screen becomes real.

## Why a new backend endpoint

The server exposes only `GET /v1/audit/verify` and `GET /v1/audit/export`.
Export **self-audits every call** and streams the full filtered set — right for
downloads, wrong for a viewer (every page view would append an audit event and
refetch all history). CLAUDE.md's API conventions require cursor pagination on
list endpoints. So B3 adds **`GET /v1/audit/events`** — paginated, authz
`audit:read` (instance scope), **not self-audited** (precedent: `/verify` is
explicitly not self-audited; audit reads are not in CLAUDE.md's must-audit
enumeration, which covers secrets/keys/tokens/policies).

## API contract

### Existing (verified against Go source; FE mocks must mirror exactly)

- `GET /v1/audit/verify` → `internal/audit.VerifyResult`:
  `{"valid":bool,"count":int64,"head_seq":int64,"head_hash":hex?,"broken_at_seq":int64?,"reason":"hash_mismatch"|"chain_break"?}`
  (omitempty on the optional fields).
- `GET /v1/audit/export?format=jsonl|csv&from=&to=&actor=&action=&result=` —
  streams; self-audited; `from`/`to` RFC3339; `result ∈ success|denied|error`.
  Row shape (`auditExportRow`): `{seq, occurred_at, actor_kind, actor_id,
  actor_name, action, resource, detail, result, result_code, ip, prev_hash,
  hash}` — `actor_id`/`detail`/`result_code` nullable, hashes hex.

### New: `GET /v1/audit/events`

- Query: the same five filter params (reuse `parseAuditFilter`), plus
  `cursor` (int64 seq; return events with `seq < cursor`; absent/0 = from head)
  and `limit` (default 50, max 200; 400 on non-integer or out-of-range).
- Response: `{"events":[auditExportRow...], "next_cursor": <int64|null>}` —
  newest-first (seq DESC); `next_cursor` = last returned seq when the page is
  full, else null. Reuses `toExportRow` so hashes stay hex and the shape is
  identical to export rows.
- Authz `audit:read` on instance; NOT self-audited; 503-sealed semantics
  inherited from existing middleware.

### Store layer

`internal/store` gains `(*AuditRepo) ListPage(ctx, f AuditFilter, beforeSeq
int64, limit int) ([]AuditRow, error)` — same WHERE building as the existing
streaming `List` (mirror its SQL/idioms: parameterized, `auditCols`,
`scanAuditRow`), plus `AND (cursor=0 OR seq < cursor)`, `ORDER BY seq DESC
LIMIT`. `internal/audit.Recorder` gains a thin `ListPage` passthrough.
Integration test via testcontainers in the existing `audit_test.go` style
(seed rows, page size 2 across 5 rows, filters compose with cursor).

## Frontend

### `web/src/lib/endpoints.ts`

Types `AuditEvent` (= export row, nullable fields as `string | null`) and
`VerifyResult` (optional fields `?`). Endpoints: `verifyAudit()`;
`listAuditEvents(params: {from?, to?, actor?, action?, result?, cursor?, limit?})`
→ `{events: AuditEvent[]; next_cursor: number | null}` (builds a
URLSearchParams, omits empty); `auditExportUrl(params, format)` → string (for
download anchors — navigating keeps export's audited semantics).

### `web/src/audit/AuditPage.tsx` (replaces the Placeholder at `/projects/:projectId/audit`)

Instance-wide data (audit is instance-scoped); header "Audit log" with sub
"All events across this instance" + `useTitle('Audit log')`.

- **Chain badge** (query `['audit','verify']`): verifying → muted Pill
  "Verifying…"; valid → success Pill dot `Chain verified · {count} events`;
  invalid → danger Pill dot `Chain broken at #{broken_at_seq}`; verify error →
  danger Pill "Verify failed".
- **Filter bar** (card row): actor text input, action text input, result select
  (All/success/denied/error), From/To `datetime-local` inputs (converted to
  RFC3339 UTC when set), Apply button commits to filter state (fetch only on
  Apply). Reset link clears.
- **Events table** (Slice-1 table treatment): columns Time (`timeAgo`, `title`
  = full ISO), Actor (`actor_name` + `actor_kind` faint subtext), Action (mono
  12px), Resource (mono, truncated, `title` full), Result Pill (success→
  success, denied→danger, error→warning), Detail (faint, truncated, `title`
  full). Uses `useInfiniteQuery` keyed `['audit','events',filters]`,
  `initialPageParam: undefined`, `getNextPageParam: (last) => last.next_cursor
  ?? undefined`; **Load more** secondary button while `hasNextPage`.
- **Export**: two secondary anchor-buttons "Export JSONL" / "Export CSV" with
  `href={auditExportUrl(activeFilters, fmt)}` + `download`; faint note
  "Exports are audited."
- **States:** loading skeleton rows (aria-hidden); list error inline danger;
  403 from events/verify → `EmptyState` title "Audit access required", hint
  "Ask an instance admin or owner for the audit role."; zero events →
  `EmptyState` "No events match these filters."
- **Security:** rows render metadata only (the audit log never contains secret
  values by design); no reveal endpoints touched; viewing writes no events.

### Route

`web/src/App.tsx`: audit route element → `<AuditPage />` (import swap only).

## Testing

- Go: store ListPage integration (pagination boundaries, filter+cursor
  compose); handler e2e (page walk with next_cursor, limit validation 400,
  bad result 400, unauthenticated 401; shape assertion incl. hex hashes).
- Web (msw, REAL shapes): badge valid/broken/error branches; filter Apply →
  query-param assertions; two-page load-more walk; result pill mapping; 403 →
  EmptyState; export hrefs carry active filters + format; empty-list state.
- Gates: full go + web suites, build, `npm run smoke`, palette gate.

## Out of scope

Live tail/auto-refresh · per-project filtered views · verify-on-schedule ·
denied-only quick presets beyond the result select · CSV/JSONL client-side
parsing (export = browser download) · audit retention UI.
