# Audit viewer depth — design

_Date: 2026-07-18. Closes gaps.md §2.3 remaining — event-count **histogram**, **saved filter presets**, and a **page-size control** on the audit page. (Row-expand + hash chain, date grouping, and sticky header already shipped in PR #81 / #91.)_

## Problem

The audit page (`web/src/audit/AuditPage.tsx`) is a filterable, infinitely-scrolling list with a chain-verify badge, row-expand, and date grouping. It gives no **shape-of-activity** view: you can't see at a glance when events spiked, or when denials/errors clustered. There's no way to save a recurring filter combo, and the page size is hardcoded. gaps.md §2.3: "No event-count timeline/histogram; no saved filter presets; no page-size control."

## Verified starting facts

- **Backend audit routes** (`internal/api/server.go`): `GET /v1/audit/verify` (hash chain), `/export` (streaming JSONL/CSV, filterable), `/events` (cursor list, filterable). Handlers in `internal/api/audit_handlers.go`.
- **Shared filter** `parseAuditFilter(r)` → `store.AuditFilter` from query params `from` (RFC3339), `to` (RFC3339), `actor`, `action`, `result` (`success`|`denied`|`error`). Used by `/events` and `/export`.
- **`/events`**: `limit` default 50, validated 1–200; `cursor` int64; returns `{ events, next_cursor }`. Reads are NOT audited server-side (comment in code).
- **No aggregate/count endpoint** exists. Precedent for aggregating `audit_events`: `/v1/metrics/reads-24h` (`handleMetricsReads`).
- **Frontend** `AuditPage.tsx`: a `Draft` filter form (`{actor, action, result, from, to}`) → `applied: AuditEventFilters` → `useInfiniteQuery` with a hardcoded `limit: 50`; `resultTone` (`web/src/audit/resultTone.ts`), `dayLabel` (date grouping), `Pill`, `relativeTime`. `AuditEventFilters` + `listAuditEvents`/`auditExportUrl` in `web/src/lib/endpoints.ts`.
- Codebase is deliberately minimal-dependency; there is no charting library.

## Approach — backend aggregate endpoint + hand-rolled bars

Add a small `GET /v1/audit/histogram` that GROUP-BYs a time bucket over `audit_events` using the **same** `store.AuditFilter` as `/events`, returning per-bucket counts split by result. The frontend renders a lightweight **hand-rolled SVG/div stacked bar chart** (no new dependency). Presets live in `localStorage`; the page-size control reuses the existing `limit` param.

Rejected: computing the histogram client-side from loaded events (only reflects the paginated slice — misleading) or paging all events to count (scales poorly); adding a charting library (heavy for a bar chart).

## Section A — Backend: histogram endpoint

- **Route**: `GET /v1/audit/histogram?from&to&bucket=hour|day&actor&action&result`, registered in the existing `/v1/audit` group (RequireAuth). `s.handleAuditHistogram`.
- **Response**: `{ "buckets": [ { "start": "<RFC3339>", "success": n, "denied": n, "error": n } ] }`, ordered by `start` ascending. Empty buckets in the range MAY be omitted (the frontend fills gaps); document which — **omit empty buckets** (simpler query; frontend zero-fills).
- **Store**: new `audit.Recorder`/store method (mirror where `ListPage` lives) running
  `SELECT date_trunc($1, timestamp) AS b, result, count(*) FROM audit_events WHERE <filter> GROUP BY b, result ORDER BY b` with `$1 ∈ {'hour','day'}` (whitelist-validated, never interpolated). Reuse the existing filter-to-SQL used by `ListPage` so the WHERE clause is identical to `/events`.
- **Handler**: reuse `parseAuditFilter(r)` for from/to/actor/action/result. Parse `bucket` (default `day`; only `hour`|`day`, else 400). Guard absurd ranges: if `to-from` divided by the bucket would exceed **1000** buckets, return 400 `CodeValidation` ("range too large for bucket"). Not audited (consistent with `/events`). Value-free: only timestamps + result category + counts.
- **OpenAPI**: document the route + response; the route-drift test must stay green (1 new route added to the walker + yaml).

## Section B — Frontend: the histogram

- New `web/src/audit/AuditHistogram.tsx` + a `web/src/audit/histogram.ts` helper (zero-fill buckets across the range, pick bucket granularity, map result→tone).
- **Endpoint**: `endpoints.auditHistogram(f: AuditEventFilters & { bucket })` → typed `{ buckets: HistBucket[] }`; `HistBucket = { start: string; success: number; denied: number; error: number }`. Query keyed on `[applied filters, bucket]` so it moves with the list's filters.
- **Range**: uses the applied `from`/`to`; when unset, defaults to **last 7 days** (`to=now`, `from=now-7d`). Bucket auto-picks: span ≤ 48h → `hour`, else `day`; a small **Hour | Day** segmented toggle overrides.
- **Render**: a stacked bar per bucket (success/denied/error segments) using `resultTone` tokens (green/amber/red); hand-rolled with flex divs or inline SVG, token classes only, in an `overflow-x-auto` container. Accessible: each bar is a `<button>` (or has an `aria-label`) like "Jul 15 — 142 events (3 denied, 1 error)". A y-axis max label + a small legend.
- **Click-to-zoom**: clicking a bar sets the applied `from`/`to` to that bucket's `[start, start+bucket)` span, drilling both the histogram and the events list into that window (and flipping the bucket toggle to the finer grain if it was daily).
- **403/empty tolerant**: on error or zero buckets, render a muted "No activity in range" strip; never crash the page.

## Section C — Frontend: presets + page-size

- **Presets** (`web/src/audit/presets.ts` + a small chip bar in `AuditPage`):
  - **Built-ins** (always available, computed relative to now at click time): "Failures · 24h" (`result=error`, from=now-24h), "Denied · 24h" (`result=denied`, from=now-24h), "Last 7 days" (from=now-7d). A built-in is a function producing a `Draft`/`AuditEventFilters`.
  - **User presets**: "Save current as…" prompts for a name and stores `{ name, filters }` in `localStorage` (key e.g. `janus.audit.presets`); saved presets render as removable chips (× to delete). Clicking any preset populates the filter form and applies it. Pure client-side; value-free (filters carry actor/action/result/time only).
- **Page-size**: a `<select>` (25 / 50 / 100 / 200) beside the filters, replacing the hardcoded `limit: 50` in `AuditPage`'s `useInfiniteQuery`; the backend already validates 1–200. Remembered in component state (session).

## Data flow

Applied filters (form/preset/bar-click) drive BOTH `useInfiniteQuery(listAuditEvents, {..filters, limit})` and `useQuery(auditHistogram, {..filters, bucket})`, keyed on the same filters, so the chart and list always agree. A bar click narrows `from`/`to`; a preset click sets the whole filter set; the page-size selector changes `limit` only.

## Error handling

- `bucket` not hour/day → 400; range exceeding the bucket cap → 400 (`CodeValidation`).
- Histogram query error / 403 → muted empty strip, list still works.
- `from`/`to` malformed → existing `parseAuditFilter` 400 path (unchanged).
- localStorage unavailable/corrupt → presets degrade to built-ins only (guarded parse).

## Testing

- **Backend**: store histogram query (hour vs day bucketing, by-result counts, filter applied identically to ListPage, empty range → no rows); handler (bucket whitelist, absurd-range 400, filter reuse); OpenAPI drift green; extend the audit leak test to hit `/histogram` and assert no secret value in output (it's counts-only, but prove it).
- **Frontend**: `histogram.ts` (zero-fill, bucket pick); `AuditHistogram` (stacked segments per result, aria labels, click-to-zoom sets range); presets (`presets.ts` built-ins produce right filters; save/load/remove round-trips localStorage; corrupt-storage fallback); page-size selector changes the query `limit`. Token classes only; dual-theme smoke; no-raw-palette guard.

## Non-goals

- Backend-persisted / cross-device presets (localStorage only).
- A charting library (hand-rolled bars).
- New audit event fields or a migration (histogram is a read-only aggregate over existing `audit_events`).
- Real-time streaming / auto-refresh of the histogram (fetch on filter change only).
- Per-actor or per-action breakdowns beyond the result split (the histogram's segments are success/denied/error; finer breakdowns stay in the filtered list).

## Rollout

One new backend read endpoint + frontend; **no migration**. After merge: rebuild dev containers (`docker compose up -d --build`) + `dev-unseal.sh` (rebuild embeds new assets + the new route). Update gaps.md §2.3.
