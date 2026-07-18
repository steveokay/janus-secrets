# Audit Viewer Depth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an event-count histogram (new value-free `GET /v1/audit/histogram` aggregate), saved filter presets (built-ins + localStorage), and a page-size control to the audit page (gaps.md §2.3).

**Architecture:** A store aggregate (`AuditRepo.Histogram`) GROUP-BYs a time bucket over `audit_events` using the SAME `AuditFilter` as `/events`; a thin Recorder passthrough + API handler pivots the grouped rows into per-bucket success/denied/error counts. The frontend renders a hand-rolled stacked bar chart (no charting dep), plus localStorage presets and a page-size selector, all driven by the audit page's existing applied filters.

**Tech Stack:** Go (pgx, chi), React+TS+Tailwind, Vitest.

---

## Reference facts (verified against the code)

- Store: `AuditRepo` in `internal/store/audit.go`; column is **`occurred_at`** (not `timestamp`). `ListPage` (`audit.go:133`) builds a WHERE from `AuditFilter` via an `add(cond, val)` closure over `where []string`/`args []any`, placeholders `$N` via `itoa(len(args)+1)`. `AuditFilter` (`internal/store/audit_models.go:32`): `From *time.Time`, `To *time.Time`, `Action string`, `Result string`, `Actor string` (actor matches `actor_id` OR `actor_name`). Result values: `success`|`denied`|`error`. `r.s.pool.Query`, `mapError`, `itoa` are the in-file helpers.
- Recorder: `internal/audit/recorder.go` — `Store` interface (line 11) has `Iterate`/`List`/`ListPage`; `Recorder` has thin passthroughs (e.g. `ListPage` at line 59). A `memStore` test double implements the interface in `internal/audit/audit_test.go`.
- API: `internal/api/audit_handlers.go` — `parseAuditFilter(r) → (store.AuditFilter, detailStr, error)` reads `from`/`to` (RFC3339) `actor`/`action`/`result` (result validated to success/denied/error). `handleAuditEvents` (limit default 50, cap 200) calls `s.audit.ListPage`. `s.audit == nil` guard returns early (unit-test servers). Routes registered in the `/v1/audit` group (`internal/api/server.go:373`, gated `if s.audit != nil`). Reads are NOT audited server-side. `writeError(w, http.StatusBadRequest, CodeValidation, msg)`, `writeJSON(w, status, v)`.
- Frontend: `web/src/audit/AuditPage.tsx` — `Draft {actor,action,result,from,to}` → `applied: AuditEventFilters` → `useInfiniteQuery(listAuditEvents, {...applied, cursor, limit: 50})` (limit hardcoded). `resultTone` (`web/src/audit/resultTone.ts`) maps result→Pill tone. `AuditEventFilters`, `auditParams`, `listAuditEvents` in `web/src/lib/endpoints.ts`.
- Toolchain: Docker + testcontainers for store/api tests; go.mod pins `toolchain go1.26.5`. Web: `npm test` is WATCH → `npm test -- --run`; tsconfig ES2020; token classes only; guards `web/src/test/no-raw-palette.test.ts` + `npm run smoke`.

---

## Task 1: store `AuditRepo.Histogram` + `AuditBucketCount`

**Files:**
- Modify: `internal/store/audit.go`, `internal/store/audit_models.go`
- Test: `internal/store/audit_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/store/audit_test.go` (reuse the file's harness — `appendConst`/`appendResult` helpers seed rows; check how the top of the file builds an `AuditRepo` against the testcontainer store, and mirror it):

```go
func TestAuditRepo_Histogram(t *testing.T) {
	repo, cleanup := newAuditRepo(t)   // use whatever constructor the existing tests use
	defer cleanup()
	ctx := context.Background()
	// seed: two success + one denied (all "now"-ish, same day)
	appendConst(t, repo, "token.mint")            // success
	appendConst(t, repo, "secret.reveal")         // success
	appendResult(t, repo, "secret.write", "denied")

	buckets, err := repo.Histogram(ctx, store.AuditFilter{}, "day")
	if err != nil {
		t.Fatalf("Histogram: %v", err)
	}
	// Collapse to a result->count map for the single day bucket.
	counts := map[string]int{}
	for _, b := range buckets {
		counts[b.Result] += b.Count
	}
	if counts["success"] != 2 || counts["denied"] != 1 {
		t.Errorf("counts = %v, want success:2 denied:1", counts)
	}

	// Filter by result reduces the set.
	only, _ := repo.Histogram(ctx, store.AuditFilter{Result: "denied"}, "day")
	total := 0
	for _, b := range only {
		total += b.Count
	}
	if total != 1 {
		t.Errorf("denied-filtered total = %d, want 1", total)
	}
}
```

> Adapt `newAuditRepo`/constructor + `appendConst`/`appendResult` to the real helpers in `audit_test.go`. If `appendResult` doesn't exist, use whichever helper seeds a row with an explicit result (the file references both).

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/store/ -run TestAuditRepo_Histogram -v`
Expected: FAIL — `repo.Histogram undefined`.

- [ ] **Step 3: Implement**

In `internal/store/audit_models.go`, add:
```go
// AuditBucketCount is one (time-bucket, result) group with its event count.
type AuditBucketCount struct {
	Start  time.Time
	Result string
	Count  int
}
```
(Ensure `time` is imported in that file.)

In `internal/store/audit.go`, add a shared filter-WHERE helper (used only by the new code — do NOT refactor List/ListPage) and the `Histogram` method:
```go
// auditWhere builds the shared filter predicates ($1..$N) for audit reads.
func auditWhere(f AuditFilter) (where []string, args []any) {
	add := func(cond string, val any) { args = append(args, val); where = append(where, cond) }
	if f.From != nil {
		add("occurred_at >= $"+itoa(len(args)+1), *f.From)
	}
	if f.To != nil {
		add("occurred_at <= $"+itoa(len(args)+1), *f.To)
	}
	if f.Action != "" {
		add("action = $"+itoa(len(args)+1), f.Action)
	}
	if f.Result != "" {
		add("result = $"+itoa(len(args)+1), f.Result)
	}
	if f.Actor != "" {
		n := itoa(len(args) + 1)
		args = append(args, f.Actor)
		where = append(where, "(actor_id = $"+n+" OR actor_name = $"+n+")")
	}
	return where, args
}

// Histogram returns per-(time-bucket, result) event counts matching f, ordered
// by bucket ascending. bucket MUST be "hour" or "day" (caller-validated;
// injected as a bound text param, never interpolated). Empty buckets are omitted.
func (r *AuditRepo) Histogram(ctx context.Context, f AuditFilter, bucket string) ([]AuditBucketCount, error) {
	where, args := auditWhere(f)
	args = append(args, bucket)
	bucketN := itoa(len(args))
	sql := `SELECT date_trunc($` + bucketN + `, occurred_at) AS b, result, count(*) FROM audit_events`
	if len(where) > 0 {
		sql += ` WHERE ` + strings.Join(where, " AND ")
	}
	sql += ` GROUP BY b, result ORDER BY b`
	rows, err := r.s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []AuditBucketCount
	for rows.Next() {
		var b AuditBucketCount
		if err := rows.Scan(&b.Start, &b.Result, &b.Count); err != nil {
			return nil, mapError(err)
		}
		out = append(out, b)
	}
	return out, mapError(rows.Err())
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/store/ -run TestAuditRepo_Histogram -v`
Expected: PASS. Then `go test ./internal/store/` (full package) → green.

- [ ] **Step 5: Commit**

```bash
git add internal/store/audit.go internal/store/audit_models.go internal/store/audit_test.go
git commit -m "feat(store): AuditRepo.Histogram (by-bucket, by-result counts)"
```

---

## Task 2: Recorder passthrough + `GET /v1/audit/histogram` endpoint

**Files:**
- Modify: `internal/audit/recorder.go` (interface + passthrough), `internal/audit/audit_test.go` (memStore double)
- Modify: `internal/api/audit_handlers.go` (handler), `internal/api/server.go` (route), `docs/openapi.yaml`
- Test: `internal/api/audit_e2e_test.go` (or the existing audit handler test file — find it), extend the audit leak test

- [ ] **Step 1: Write the failing tests**

Find the existing audit API test (search `internal/api` for a test hitting `/v1/audit/events`). Add a test for `GET /v1/audit/histogram?from=<>&to=<>&bucket=day`: seed a few events (via the server's audit recorder / by performing audited actions the harness already uses), then GET with a range covering them and assert the response `buckets` has an entry with the expected `success`/`denied` counts. Add: `bucket=week` → 400; missing `from`/`to` → 400; a range that yields > 1000 buckets (e.g. `bucket=hour`, from 2000-01-01 to 2030-01-01) → 400.

Extend the audit leak test (search `internal/api` for the test asserting no secret value appears in audit output) to also GET `/histogram` and assert no secret sentinel appears (it's counts-only; prove it).

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run 'AuditHistogram|Histogram|Leak' -v`
Expected: FAIL — route not found.

- [ ] **Step 3: Recorder passthrough**

In `internal/audit/recorder.go`, add to the `Store` interface (next to `ListPage`):
```go
Histogram(ctx context.Context, f store.AuditFilter, bucket string) ([]store.AuditBucketCount, error)
```
Add the passthrough method (next to `ListPage`'s):
```go
// Histogram exposes bucketed event counts for the API's histogram endpoint.
func (rec *Recorder) Histogram(ctx context.Context, f store.AuditFilter, bucket string) ([]store.AuditBucketCount, error) {
	return rec.store.Histogram(ctx, f, bucket)
}
```
In `internal/audit/audit_test.go`, add a `Histogram` method to the `memStore` double so it still satisfies the interface — a minimal in-memory grouping is fine:
```go
func (m *memStore) Histogram(_ context.Context, f store.AuditFilter, bucket string) ([]store.AuditBucketCount, error) {
	// Minimal double: one bucket per (truncated-time, result). Truncation
	// granularity doesn't matter for Recorder passthrough tests; group by result
	// at a fixed zero time is sufficient for interface conformance.
	counts := map[string]int{}
	for _, r := range m.rows {
		// apply the same filters the real store would (reuse existing memStore filter logic if factored; else inline the action/result checks)
		if f.Action != "" && r.Action != f.Action { continue }
		if f.Result != "" && r.Result != f.Result { continue }
		counts[r.Result]++
	}
	var out []store.AuditBucketCount
	for res, n := range counts {
		out = append(out, store.AuditBucketCount{Start: time.Time{}, Result: res, Count: n})
	}
	return out, nil
}
```
(Match the memStore's existing field names — read it. Import `time` if needed.)

- [ ] **Step 4: API handler + route**

In `internal/api/audit_handlers.go`, add:
```go
type histBucket struct {
	Start   string `json:"start"`
	Success int    `json:"success"`
	Denied  int    `json:"denied"`
	Error   int    `json:"error"`
}

func (s *Server) handleAuditHistogram(w http.ResponseWriter, r *http.Request) {
	if s.audit == nil {
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "audit not configured")
		return
	}
	filter, _, err := parseAuditFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
		return
	}
	if filter.From == nil || filter.To == nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "from and to are required")
		return
	}
	bucket := r.URL.Query().Get("bucket")
	if bucket == "" {
		bucket = "day"
	}
	if bucket != "hour" && bucket != "day" {
		writeError(w, http.StatusBadRequest, CodeValidation, "bucket must be hour or day")
		return
	}
	// Guard absurd ranges: cap the number of buckets.
	step := time.Hour
	if bucket == "day" {
		step = 24 * time.Hour
	}
	if filter.To.Sub(*filter.From)/step > 1000 {
		writeError(w, http.StatusBadRequest, CodeValidation, "range too large for bucket")
		return
	}
	rows, err := s.audit.Histogram(r.Context(), filter, bucket)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	// Pivot (start,result,count) rows into per-bucket success/denied/error.
	byStart := map[string]*histBucket{}
	var order []string
	for _, row := range rows {
		key := row.Start.UTC().Format(time.RFC3339)
		b := byStart[key]
		if b == nil {
			b = &histBucket{Start: key}
			byStart[key] = b
			order = append(order, key)
		}
		switch row.Result {
		case "denied":
			b.Denied += row.Count
		case "error":
			b.Error += row.Count
		default:
			b.Success += row.Count
		}
	}
	out := make([]histBucket, 0, len(order))
	for _, k := range order {
		out = append(out, *byStart[k])
	}
	writeJSON(w, http.StatusOK, map[string]any{"buckets": out})
}
```
Confirm `CodeUnavailable`/`CodeInternal`/`CodeValidation` constant names against the file (use the ones the sibling handlers use; if `CodeUnavailable` doesn't exist, use whatever `handleAuditEvents`/others use for a 503, or return 500 — match the codebase).

In `internal/api/server.go`, add to the `/v1/audit` group (next to `/events`):
```go
r.Get("/histogram", s.handleAuditHistogram)
```

- [ ] **Step 5: OpenAPI**

Add `GET /v1/audit/histogram` to `docs/openapi.yaml` (query params from/to/bucket/actor/action/result; 200 `{buckets:[{start,success,denied,error}]}`; 400). The route-drift test must stay green (new route added to the walker + yaml).

- [ ] **Step 6: Run to verify pass**

Run: `go test ./internal/audit/ ./internal/api/ -run 'AuditHistogram|Histogram|Leak|Drift|OpenAPI' -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/audit/recorder.go internal/audit/audit_test.go internal/api/audit_handlers.go internal/api/server.go internal/api/*_test.go docs/openapi.yaml
git commit -m "feat(api): GET /v1/audit/histogram (value-free bucketed counts)"
```

---

## Task 3: Web endpoint + `histogram.ts` helper

**Files:**
- Modify: `web/src/lib/endpoints.ts`
- Create: `web/src/audit/histogram.ts` + `web/src/audit/histogram.test.ts`

- [ ] **Step 1: Write the failing test** (`web/src/audit/histogram.test.ts`)

```ts
import { describe, it, expect } from 'vitest'
import { pickBucket, zeroFill } from './histogram'

describe('histogram helpers', () => {
  it('pickBucket: <=48h span → hour, else day', () => {
    expect(pickBucket('2026-01-01T00:00:00Z', '2026-01-02T00:00:00Z')).toBe('hour')
    expect(pickBucket('2026-01-01T00:00:00Z', '2026-01-10T00:00:00Z')).toBe('day')
  })
  it('zeroFill inserts empty buckets across the range at the given granularity', () => {
    const filled = zeroFill(
      [{ start: '2026-01-02T00:00:00Z', success: 5, denied: 0, error: 0 }],
      '2026-01-01T00:00:00Z', '2026-01-03T00:00:00Z', 'day',
    )
    // days: 01, 02, 03 → 3 buckets; 02 keeps its count, 01/03 are zeros
    expect(filled).toHaveLength(3)
    expect(filled[1]).toEqual({ start: '2026-01-02T00:00:00Z', success: 5, denied: 0, error: 0 })
    expect(filled[0].success + filled[0].denied + filled[0].error).toBe(0)
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd web && npm test -- --run src/audit/histogram.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

In `web/src/lib/endpoints.ts`: add types + the endpoint (near the other audit endpoints):
```ts
export type HistBucket = { start: string; success: number; denied: number; error: number }
```
Add to the `endpoints` object:
```ts
auditHistogram: (f: AuditEventFilters & { bucket: 'hour' | 'day' }) =>
  api.get<{ buckets: HistBucket[] }>(`/v1/audit/histogram?${auditParams(f)}`).then((r) => r.buckets),
```
Confirm `auditParams` serializes an extra `bucket` key (it takes `AuditEventFilters & {...}` and iterates set fields — verify it includes arbitrary extra params like `bucket`; if it whitelists keys, extend it to pass `bucket`).

Create `web/src/audit/histogram.ts`:
```ts
import type { HistBucket } from '../lib/endpoints'

export type Bucket = 'hour' | 'day'

/** Auto granularity: spans up to 48h use hourly buckets, longer spans daily. */
export function pickBucket(fromISO: string, toISO: string): Bucket {
  const span = new Date(toISO).getTime() - new Date(fromISO).getTime()
  return span <= 48 * 3600_000 ? 'hour' : 'day'
}

function truncate(d: Date, bucket: Bucket): Date {
  const c = new Date(d)
  c.setUTCMinutes(0, 0, 0)
  if (bucket === 'day') c.setUTCHours(0)
  return c
}

/** Fill empty buckets across [from,to] at the given granularity, merging counts. */
export function zeroFill(buckets: HistBucket[], fromISO: string, toISO: string, bucket: Bucket): HistBucket[] {
  const byStart = new Map(buckets.map((b) => [new Date(b.start).getTime(), b]))
  const stepMs = bucket === 'hour' ? 3600_000 : 24 * 3600_000
  const out: HistBucket[] = []
  let t = truncate(new Date(fromISO), bucket).getTime()
  const end = truncate(new Date(toISO), bucket).getTime()
  for (; t <= end; t += stepMs) {
    const hit = byStart.get(t)
    out.push(hit ?? { start: new Date(t).toISOString(), success: 0, denied: 0, error: 0 })
  }
  return out
}
```
(Note: server bucket starts are `date_trunc` UTC boundaries; `truncate` here mirrors that so keys align. If a server `start` doesn't land exactly on a JS-truncated boundary in the test, adjust `truncate` to match `date_trunc` semantics — hour/day floor in UTC, which the code does.)

- [ ] **Step 4: Run to verify pass + typecheck**

Run: `cd web && npm test -- --run src/audit/histogram.test.ts` → PASS; `npm run typecheck` → clean.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/endpoints.ts web/src/audit/histogram.ts web/src/audit/histogram.test.ts
git commit -m "feat(web): audit histogram endpoint + zero-fill/bucket helpers"
```

---

## Task 4: `AuditHistogram` component + wire into AuditPage

**Files:**
- Create: `web/src/audit/AuditHistogram.tsx` + `web/src/audit/AuditHistogram.test.tsx`
- Modify: `web/src/audit/AuditPage.tsx` (render the histogram above the list; pass applied filters + an `onRange` callback)
- Test: `web/src/audit/AuditPage.test.tsx` (append a smoke assertion)

- [ ] **Step 1: Write the failing test** (`AuditHistogram.test.tsx`)

Render `<AuditHistogram filters={{ from, to }} onRange={spy} />` inside a QueryClientProvider, mocking `endpoints.auditHistogram` (via `vi.spyOn`) to return two buckets (one with denied>0). Assert: bars render (one per bucket) with an accessible name including the count; a bar segment uses a result tone class; clicking a bar calls `onRange(from,to)` for that bucket's span. Add an empty-state test (endpoint returns `[]` → a muted "No activity in range" strip).

- [ ] **Step 2: Run to verify it fails**

Run: `cd web && npm test -- --run src/audit/AuditHistogram.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `AuditHistogram.tsx`**

A component that:
- Props: `{ filters: AuditEventFilters; onRange: (fromISO: string, toISO: string) => void }`.
- Computes the effective range: `from = filters.from ?? (now - 7d)`, `to = filters.to ?? now` (ISO strings). Local `bucket` state defaults to `pickBucket(from, to)`, with a small **Hour | Day** segmented toggle.
- `useQuery({ queryKey: ['audit','histogram', filters, bucket], queryFn: () => endpoints.auditHistogram({ ...filters, from, to, bucket }), retry: false })`.
- `zeroFill(data, from, to, bucket)` → bars. Each bar is a `<button aria-label="Jul 15 — 142 events (3 denied, 1 error)">` containing stacked segments (success/denied/error) heights proportional to counts over the max total; segment colors via `resultTone` tokens (or the same soft/solid tokens Pill uses). Click → `onRange(bucketStart, bucketStart+step)`.
- Container `overflow-x-auto`; a small y-max label + legend (success/denied/error swatches). Token classes only.
- On error/empty → a muted "No activity in range" strip (never throws).

In `AuditPage.tsx`: render `<AuditHistogram filters={applied} onRange={(from,to) => setApplied((a) => ({ ...a, from, to }))} />` above the events list (below the filter form / verify badge). Keep the list/filters intact.

- [ ] **Step 4: Run to verify pass + guards**

Run: `cd web && npm test -- --run src/audit/AuditHistogram.test.tsx src/audit/AuditPage.test.tsx` → PASS; `npm run typecheck` → clean; `npm test -- --run src/test/no-raw-palette.test.ts` → PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/audit/AuditHistogram.tsx web/src/audit/AuditHistogram.test.tsx web/src/audit/AuditPage.tsx web/src/audit/AuditPage.test.tsx
git commit -m "feat(web): audit event-count histogram (stacked bars, click-to-zoom)"
```

---

## Task 5: Presets (built-ins + localStorage) + page-size control

**Files:**
- Create: `web/src/audit/presets.ts` + `web/src/audit/presets.test.ts`
- Modify: `web/src/audit/AuditPage.tsx` (preset chip bar + page-size selector)
- Test: `web/src/audit/AuditPage.test.tsx` (append)

- [ ] **Step 1: Write the failing tests**

`presets.test.ts`:
```ts
import { describe, it, expect, beforeEach } from 'vitest'
import { BUILTIN_PRESETS, loadPresets, savePreset, removePreset } from './presets'

describe('audit presets', () => {
  beforeEach(() => localStorage.clear())
  it('built-ins produce filters (Failures 24h → result=error + a from)', () => {
    const f = BUILTIN_PRESETS.find((p) => /failures/i.test(p.name))!.filters()
    expect(f.result).toBe('error')
    expect(typeof f.from).toBe('string')
  })
  it('save/load/remove round-trips localStorage', () => {
    savePreset('Mine', { actor: 'alice' })
    expect(loadPresets().map((p) => p.name)).toContain('Mine')
    removePreset('Mine')
    expect(loadPresets().map((p) => p.name)).not.toContain('Mine')
  })
  it('corrupt storage degrades to empty list', () => {
    localStorage.setItem('janus.audit.presets', '{not json')
    expect(loadPresets()).toEqual([])
  })
})
```

`AuditPage.test.tsx` (append): clicking a built-in preset chip ("Failures · 24h") applies `result=error` (assert the events query is called with `result: 'error'` via the mock, or that the result filter control reflects it); the page-size `<select>` changing to `100` makes the events query use `limit: 100`.

- [ ] **Step 2: Run to verify it fails**

Run: `cd web && npm test -- --run src/audit/presets.test.ts src/audit/AuditPage.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement `presets.ts`**

```ts
import type { AuditEventFilters } from '../lib/endpoints'

const KEY = 'janus.audit.presets'
const hoursAgo = (h: number) => new Date(Date.now() - h * 3600_000).toISOString()

export interface Preset { name: string; filters: AuditEventFilters }
export interface BuiltinPreset { name: string; filters: () => AuditEventFilters }

export const BUILTIN_PRESETS: BuiltinPreset[] = [
  { name: 'Failures · 24h', filters: () => ({ result: 'error', from: hoursAgo(24) }) },
  { name: 'Denied · 24h', filters: () => ({ result: 'denied', from: hoursAgo(24) }) },
  { name: 'Last 7 days', filters: () => ({ from: hoursAgo(24 * 7) }) },
]

export function loadPresets(): Preset[] {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) ?? '[]')
    return Array.isArray(v) ? (v as Preset[]) : []
  } catch {
    return []
  }
}
export function savePreset(name: string, filters: AuditEventFilters): void {
  const next = loadPresets().filter((p) => p.name !== name).concat({ name, filters })
  localStorage.setItem(KEY, JSON.stringify(next))
}
export function removePreset(name: string): void {
  localStorage.setItem(KEY, JSON.stringify(loadPresets().filter((p) => p.name !== name)))
}
```

In `AuditPage.tsx`:
- A preset chip bar: built-in chips + saved chips (each saved chip has a × to `removePreset` + refresh local state); clicking a chip sets `draft`/`applied` from its filters (built-in: call `.filters()`). A "Save current…" button prompts (`window.prompt`) for a name → `savePreset(name, applied)`.
- A page-size `<select>` (25/50/100/200) with `pageSize` state (default 50) passed into `useInfiniteQuery`'s `limit` (replace the hardcoded `50`).
- Token classes only.

- [ ] **Step 4: Run to verify pass + guards**

Run: `cd web && npm test -- --run src/audit/presets.test.ts src/audit/AuditPage.test.tsx` → PASS; `npm run typecheck` → clean; `npm test -- --run src/test/no-raw-palette.test.ts` → PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/audit/presets.ts web/src/audit/presets.test.ts web/src/audit/AuditPage.tsx web/src/audit/AuditPage.test.tsx
git commit -m "feat(web): audit filter presets (built-ins + localStorage) + page-size"
```

---

## Task 6: Full gate + smoke + gaps.md

**Files:**
- Modify: `gaps.md`

- [ ] **Step 1: Backend gate**

Run: `go build ./... && go test ./... -race` → all packages pass, no races. Then `gosec ./...` / `govulncheck ./...` — only pre-existing vendored-Shamir gosec findings + the known local-toolchain `GO-2026-5856` are acceptable; flag anything new.

- [ ] **Step 2: Web gate**

Run (from `web/`): `npm test -- --run && npm run typecheck && npm run smoke && npm test -- --run src/test/no-raw-palette.test.ts` (+ `no-legacy-alias.test.ts` if present).
Expected: all PASS; smoke light + dark OK.

- [ ] **Step 3: Update gaps.md §2.3**

Mark the histogram/timeline, saved filter presets, and page-size control as DONE (dated 2026-07-18, matching the file's ~~strikethrough~~/**[DONE …]** style). §2.3 becomes fully done (expand + grouping + sticky were already done).

- [ ] **Step 4: Commit**

```bash
git add gaps.md
git commit -m "docs(gaps): mark §2.3 audit histogram + presets + page-size done"
```

---

## Self-review checklist (author)

- **Spec coverage:** A (histogram endpoint) → Tasks 1–2; B (frontend histogram) → Tasks 3–4; C (presets + page-size) → Task 5; testing/gate → Task 6. All covered.
- **Type consistency:** `AuditBucketCount {Start,Result,Count}` (T1) → Recorder/handler pivot to `histBucket {start,success,denied,error}` (T2) → web `HistBucket` (T3) → `zeroFill`/`pickBucket` (T3) consumed by `AuditHistogram` (T4). `endpoints.auditHistogram(f & {bucket})`. `presets.ts` (`Preset`, `BUILTIN_PRESETS`, load/save/remove) (T5).
- **Value-free:** histogram is counts + bucket timestamps + result category only; leak test extended (T2). No migration.
- **Filter parity:** histogram reuses `parseAuditFilter` + the same `auditWhere` predicates as the list, so chart and list agree.
- **Open verification points flagged inline** (audit_test harness helpers, memStore field names, `Code*` constant names, `auditParams` extra-key handling, `date_trunc` vs JS truncate alignment) — implementer confirms against the code.
