# B3 — Audit Viewer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A real audit page (chain-verify badge, filterable paginated event table, audited export downloads) backed by a new cursor-paginated `GET /v1/audit/events` endpoint.

**Architecture:** Backend first: `store.AuditRepo.ListPage` (seq-descending keyset pagination reusing the existing filter SQL) → `audit.Recorder.ListPage` passthrough → API handler reusing `parseAuditFilter` + `toExportRow`, NOT self-audited (precedent: `/verify`). Then FE: endpoints typed to the REAL wire shapes, and `AuditPage` (badge + filter bar + `useInfiniteQuery` table + export anchors) replacing the placeholder route.

**Tech Stack:** Go (pgx, chi, testcontainers integration tests) + existing web stack. Zero new dependencies.

**Authority:** spec `docs/superpowers/specs/2026-07-06-b3-audit-viewer-design.md` (full API contract). Go house rules from CLAUDE.md: table-driven tests, parameterized SQL only, no secret values in logs. Web: palette gate (tokens only), msw mocks mirror the wire shapes exactly.

**Go-task ground rule:** the reference code below is written against the repo's conventions but the EXISTING files are authoritative for idioms (`auditCols`, `scanAuditRow`, filter WHERE-building in `internal/store/audit.go`'s `List`, e2e helpers in `internal/api/audit_e2e_test.go`). Read them first; if the reference below conflicts with an existing idiom, follow the file and note the deviation in your report.

**Commands: web tasks from `web/`, Go tasks from repo root. Every commit ends with trailer (blank line before): `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`. Never push.**

---

### Task 0: Branch

- [ ] `git checkout -b milestone-16-b3-audit` from up-to-date main; clean tree.

---

### Task 1: Store + recorder pagination (Go)

**Files:**
- Modify: `internal/store/audit.go` (add `ListPage` next to the existing `List`)
- Modify: `internal/audit/*` (thin `Recorder.ListPage` passthrough — put it beside the existing `List` passthrough; find it with `grep -n "func (rec \*Recorder) List" internal/audit`)
- Test: `internal/store/audit_test.go` (append; mirror its existing seeding helpers)

- [ ] **Step 1 (TDD):** Append an integration test to `internal/store/audit_test.go`. Mirror the file's existing setup (testcontainers pool, seeded rows). Behavior to pin (write as a single table-driven test or sequential asserts, matching file style):

```go
func TestAuditListPage(t *testing.T) {
	// seed 5 events via the file's existing insert/record helper, actions
	// "a.1".."a.5" (seq 1..5), one of them with Result "denied".
	// page 1: ListPage(ctx, AuditFilter{}, 0, 2) -> seqs [5,4]
	// page 2: ListPage(ctx, AuditFilter{}, 4, 2) -> seqs [3,2]
	// page 3: ListPage(ctx, AuditFilter{}, 2, 2) -> seqs [1]
	// filter+cursor compose: ListPage(ctx, AuditFilter{Result:"denied"}, 0, 10)
	//   -> exactly the denied row
	// limit respected: ListPage(ctx, AuditFilter{}, 0, 1) -> 1 row (seq 5)
}
```

Fill the body concretely using the file's existing helpers (read them first — do NOT invent new seeding paths). Run: `go test ./internal/store -run TestAuditListPage` → FAIL (method missing).

- [ ] **Step 2:** Implement in `internal/store/audit.go`, mirroring `List`'s filter WHERE-building exactly (same clause order and casts), adding keyset pagination:

```go
// ListPage returns events matching f, newest-first, with seq-keyset pagination:
// rows with seq < beforeSeq (beforeSeq 0 = from head), at most limit rows.
func (r *AuditRepo) ListPage(ctx context.Context, f AuditFilter, beforeSeq int64, limit int) ([]AuditRow, error) {
	// Build WHERE identically to List (from/to/actor/action/result), then:
	//   AND ($n = 0 OR seq < $n)
	// ORDER BY seq DESC LIMIT $m
	// Scan with scanAuditRow; return the slice.
}
```

(The concrete SQL string must reuse `auditCols` and the same parameter style — `$n` placeholders, `::timestamptz` casts if `List` uses them. Copy `List`'s builder, don't re-derive it.)

- [ ] **Step 3:** Add the passthrough in `internal/audit` beside the existing `List` passthrough:

```go
// ListPage exposes paginated reads for the API's events endpoint.
func (rec *Recorder) ListPage(ctx context.Context, f store.AuditFilter, beforeSeq int64, limit int) ([]store.AuditRow, error) {
	return rec.store.ListPage(ctx, f, beforeSeq, limit)
}
```

(Adapt receiver/field names to the actual Recorder struct — read it. If Recorder's store field is an interface, extend the interface where it's defined and update any test fakes that implement it.)

- [ ] **Step 4:** `go test ./internal/store ./internal/audit` → PASS (Docker required for testcontainers). `go vet ./...` clean.
- [ ] **Step 5:** Commit → `feat(store): keyset-paginated audit ListPage`

---

### Task 2: `GET /v1/audit/events` handler (Go)

**Files:**
- Modify: `internal/api/audit_handlers.go` (new handler + response struct)
- Modify: `internal/api/server.go` (one route line inside the existing `/v1/audit` group: `r.Get("/events", s.handleAuditEvents)`)
- Test: `internal/api/audit_e2e_test.go` (append; reuse its helpers — `doAuthed`, seeding via API actions)

- [ ] **Step 1 (TDD):** Append an e2e test mirroring the file's style. Behavior to pin:

```go
func TestAuditEventsPagination(t *testing.T) {
	// boot server + admin cookie via the file's existing helpers; perform ≥3
	// audited actions (e.g. project create + secret writes) to grow the log.
	// GET /v1/audit/events?limit=2 -> 200; body {events:[2 rows newest-first], next_cursor: <seq of 2nd row>}
	//   assert rows carry hex prev_hash/hash and RFC3339Nano occurred_at (same as export rows)
	// GET /v1/audit/events?limit=2&cursor=<next_cursor> -> next page, seqs strictly lower
	// walk to exhaustion -> final page has next_cursor null
	// GET /v1/audit/events?limit=0 -> 400; limit=201 -> 400; limit=abc -> 400; cursor=abc -> 400
	// GET /v1/audit/events?result=bogus -> 400 (parseAuditFilter)
	// unauthenticated -> 401
	// CRITICAL: assert the events endpoint did NOT append audit events itself
	//   (capture head seq before/after a GET /events and require equality)
}
```

Run: `go test ./internal/api -run TestAuditEventsPagination` → FAIL (404).

- [ ] **Step 2:** Implement in `internal/api/audit_handlers.go`:

```go
type auditEventsResponse struct {
	Events     []auditExportRow `json:"events"`
	NextCursor *int64           `json:"next_cursor"`
}

// handleAuditEvents serves the viewer: paginated, filterable, NOT self-audited
// (precedent: verify; audit reads are not in the must-audit set).
func (s *Server) handleAuditEvents(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.AuditRead, authz.Instance(), "audit.events", "audit") {
		return
	}
	if s.audit == nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "audit is not configured")
		return
	}
	filter, _, err := parseAuditFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			writeError(w, http.StatusBadRequest, CodeValidation, "limit must be 1-200")
			return
		}
		limit = n
	}
	var cursor int64
	if v := r.URL.Query().Get("cursor"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, CodeValidation, "cursor must be a positive integer")
			return
		}
		cursor = n
	}
	rows, err := s.audit.ListPage(r.Context(), filter, cursor, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	out := make([]auditExportRow, 0, len(rows))
	for _, a := range rows {
		out = append(out, toExportRow(a))
	}
	var next *int64
	if len(rows) == limit && limit > 0 {
		last := rows[len(rows)-1].Seq
		next = &last
	}
	writeJSON(w, http.StatusOK, auditEventsResponse{Events: out, NextCursor: next})
}
```

IMPORTANT CAVEAT on `authorize`: the existing `authorize` helper self-records an audit event on success (check its body in `internal/api/` — `handleAuditVerify` uses it with action "audit.verify" yet the comment says "Not self-audited"; read how verify achieves that — if `authorize` records only DENIALS, fine as-is; if it records successes, use the same authz-check-without-recording path `s.can(...)` that `handleVersionList` uses, with `writeAuthzError`). Match whatever `/verify` actually does so events reads behave identically; the e2e head-seq assertion in Step 1 is the enforcement.

- [ ] **Step 3:** Route in `server.go` inside the existing `/v1/audit` group: `r.Get("/events", s.handleAuditEvents)`.
- [ ] **Step 4:** `go test ./internal/api -run TestAuditEvents` PASS; full `go test ./...` PASS; `go vet ./...` clean; `go build ./...`.
- [ ] **Step 5:** Commit → `feat(api): cursor-paginated /v1/audit/events for the viewer`

---

### Task 3: Web endpoints + types

**Files:**
- Modify: `web/src/lib/endpoints.ts`
- Test: `web/src/lib/endpoints.test.ts` (append)

- [ ] **Step 1 (TDD):** Append tests (real wire shapes):

```ts
test('listAuditEvents builds params and returns envelope', async () => {
  let url = ''
  server.use(http.get('/v1/audit/events', ({ request }) => {
    url = request.url
    return HttpResponse.json({ events: [
      { seq: 5, occurred_at: '2026-07-06T10:00:00.000000001Z', actor_kind: 'user', actor_id: 'u1',
        actor_name: 'steve@acme.dev', action: 'secret.write', resource: 'configs/c1', detail: null,
        result: 'success', result_code: null, ip: '127.0.0.1', prev_hash: 'aa', hash: 'bb' },
    ], next_cursor: 5 })
  }))
  const r = await endpoints.listAuditEvents({ actor: 'steve', result: 'denied', cursor: 9, limit: 2 })
  expect(r.next_cursor).toBe(5)
  const q = new URL(url).searchParams
  expect(q.get('actor')).toBe('steve')
  expect(q.get('result')).toBe('denied')
  expect(q.get('cursor')).toBe('9')
  expect(q.get('limit')).toBe('2')
  expect(q.get('from')).toBeNull()
})

test('verifyAudit returns the verify result', async () => {
  server.use(http.get('/v1/audit/verify', () =>
    HttpResponse.json({ valid: true, count: 42, head_seq: 42, head_hash: 'ff' })))
  await expect(endpoints.verifyAudit()).resolves.toMatchObject({ valid: true, count: 42 })
})

test('auditExportUrl carries filters and format', () => {
  const u = endpoints.auditExportUrl({ actor: 'steve', result: 'denied' }, 'csv')
  const q = new URL(u, 'http://x').searchParams
  expect(u.startsWith('/v1/audit/export?')).toBe(true)
  expect(q.get('format')).toBe('csv')
  expect(q.get('actor')).toBe('steve')
  expect(q.get('result')).toBe('denied')
})
```

FAIL first.

- [ ] **Step 2:** Implement in `endpoints.ts`:

```ts
export interface AuditEvent {
  seq: number
  occurred_at: string
  actor_kind: string
  actor_id: string | null
  actor_name: string
  action: string
  resource: string
  detail: string | null
  result: 'success' | 'denied' | 'error'
  result_code: string | null
  ip: string
  prev_hash: string
  hash: string
}
export interface VerifyResult {
  valid: boolean
  count: number
  head_seq: number
  head_hash?: string
  broken_at_seq?: number
  reason?: 'hash_mismatch' | 'chain_break'
}
export interface AuditEventFilters {
  from?: string; to?: string; actor?: string; action?: string; result?: string
}

function auditParams(f: AuditEventFilters & { cursor?: number; limit?: number }): string {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(f)) {
    if (v !== undefined && v !== '') q.set(k, String(v))
  }
  return q.toString()
}
```

and in the `endpoints` object:

```ts
  // audit (B3): events/verify reads are NOT audited server-side; export IS.
  verifyAudit: () => api.get<VerifyResult>('/v1/audit/verify'),
  listAuditEvents: (f: AuditEventFilters & { cursor?: number; limit?: number }) =>
    api.get<{ events: AuditEvent[]; next_cursor: number | null }>(`/v1/audit/events?${auditParams(f)}`),
  auditExportUrl: (f: AuditEventFilters, format: 'jsonl' | 'csv') =>
    `/v1/audit/export?${auditParams({ ...f })}&format=${format}`,
```

- [ ] **Step 3:** Verify: tests PASS; full suite green; typecheck clean.
- [ ] **Step 4:** Commit → `feat(web): audit endpoints + wire types`

---

### Task 4: AuditPage

**Files:**
- Create: `web/src/audit/AuditPage.tsx`
- Test: `web/src/audit/AuditPage.test.tsx`

- [ ] **Step 1 (TDD):** `AuditPage.test.tsx` — msw, real shapes, `renderApp(<AuditPage />, { route: '/projects/p1/audit', withAuth: false })`. Helper `EV(seq, over = {})` building a full AuditEvent row (all 13 fields, spread `over`). Tests:

```
1. badge: verify -> {valid:true, count:42, head_seq:42} => text /Chain verified · 42 events/
2. badge broken: {valid:false, count:40, head_seq:40, broken_at_seq:17, reason:'hash_mismatch'} => /Chain broken at #17/
3. table renders events (2 rows: secret.write success => success pill; auth denied row result:'denied' => danger pill); resource in mono cell; detail truncated with title attr
4. load more: first response next_cursor:2 + second response (cursor param asserted = '2') next_cursor:null => click "Load more" appends rows, button disappears
5. filters: type actor 'steve', select result 'denied', click Apply => events request has actor=steve&result=denied (assert via captured URL)
6. export links: after Apply, "Export CSV" anchor href contains format=csv&actor=steve&result=denied
7. 403 on events (HttpResponse.json({error:{code:'forbidden',message:'x'}},{status:403})) => EmptyState "Audit access required"
8. zero events + valid verify => EmptyState "No events match these filters."
```

Write all 8 concretely with the shapes from Task 3's test. FAIL first.

- [ ] **Step 2:** Implement `AuditPage.tsx` per spec §Frontend. Structure (complete component, ~180 lines):

- `useTitle('Audit log')`; header row: h3 "Audit log" + sub "All events across this instance"; right side: chain badge + export anchors.
- Badge from `useQuery({ queryKey: ['audit','verify'], queryFn: endpoints.verifyAudit })`:
  loading → `<Pill tone="muted">Verifying…</Pill>`; error → `<Pill tone="danger">Verify failed</Pill>`;
  `valid` → `<Pill tone="success" dot>Chain verified · {count} events</Pill>`;
  else → `<Pill tone="danger" dot>Chain broken at #{broken_at_seq}</Pill>`.
- Filter state: `draft` (inputs) vs `applied` (committed on Apply; Reset clears both). Inputs: actor, action (text, `rounded border border-line px-2.5 py-1.5 text-[12.5px]`), result `<select>` (All ''/success/denied/error), from/to `datetime-local` → convert non-empty via `new Date(v).toISOString()` at Apply time.
- Events via `useInfiniteQuery({ queryKey: ['audit','events', applied], queryFn: ({ pageParam }) => endpoints.listAuditEvents({ ...applied, cursor: pageParam, limit: 50 }), initialPageParam: undefined as number | undefined, getNextPageParam: (last) => last.next_cursor ?? undefined })`.
- 403 detection: `error instanceof ApiError && error.status === 403` → `<EmptyState title="Audit access required" hint="Ask an instance admin or owner for the audit role." />`. Other errors → inline danger line.
- Table (card, Slice-1 conventions): thead `text-left text-[10.5px] uppercase tracking-[.1em] text-faint`; columns Time (`<span title={e.occurred_at}>{timeAgo(e.occurred_at)}</span>`), Actor (name + faint `actor_kind` sub), Action `font-mono text-[12px]`, Resource `font-mono text-[12px] max-w-[220px] truncate` + title, Result `<Pill tone={e.result === 'success' ? 'success' : e.result === 'denied' ? 'danger' : 'warning'}>{e.result}</Pill>`, Detail `text-[12px] text-faint max-w-[240px] truncate` + title.
- Rows = `data.pages.flatMap(p => p.events)`; loading → 3 aria-hidden skeleton bars; empty → EmptyState per spec; `hasNextPage` → secondary "Load more" button calling `fetchNextPage()` (disabled while `isFetchingNextPage`).
- Export: `<a download href={endpoints.auditExportUrl(applied, 'jsonl')} className="...secondary btn classes...">Export JSONL</a>` + CSV twin + faint note "Exports are audited."
- Token classes only; no hex; never render `prev_hash`/`hash` (noise) — omit them from the table entirely.

- [ ] **Step 3:** Verify: 8/8 PASS; full suite green; typecheck clean.
- [ ] **Step 4:** Commit → `feat(web): audit viewer — chain badge, filters, paginated table, export`

---

### Task 5: Route + gates + tracker

**Files:**
- Modify: `web/src/App.tsx` (audit route only: `<Route path="/projects/:projectId/audit" element={<AuditPage />} />` + import; Placeholder import stays for the other routes)
- Modify: `fe-improvements.md`

- [ ] **Step 1:** Swap the route; `npx vitest run src/App.test.tsx` + full suite green; typecheck clean.
- [ ] **Step 2:** Full gates: web `typecheck`/`vitest`/`build`/`smoke` + root `go build ./...` + `go test ./internal/api ./internal/store ./internal/audit` + `go vet ./...`.
- [ ] **Step 3:** `fe-improvements.md` §8: annotate the audit-viewer mention as shipped *(B3 — chain-verify badge, filterable paginated table over new `/v1/audit/events`, audited JSONL/CSV export downloads)*.
- [ ] **Step 4:** Commit → `feat(web): wire audit route; docs(fe): check off B3 audit viewer`
- [ ] **Step 5 (controller):** Final whole-branch review → PR → merge per standing orders.

## Out of scope

Live tail/auto-refresh · per-project audit filtering · verify scheduling · retention UI · rendering hash columns.
