import { useState } from 'react'
import { useQuery, useInfiniteQuery } from '@tanstack/react-query'
import { endpoints, AuditEvent, AuditEventFilters } from '../lib/endpoints'
import { ApiError } from '../lib/api'
import { Pill } from '../ui/Pill'
import { EmptyState } from '../ui/EmptyState'
import { useTitle } from '../lib/title'
import { timeAgo } from '../lib/time'

// Draft (inputs) vs applied (committed on Apply) — the events query only
// refetches when applied changes, not on every keystroke.
type Draft = { actor: string; action: string; result: string; from: string; to: string }
const EMPTY_DRAFT: Draft = { actor: '', action: '', result: '', from: '', to: '' }

function toApplied(d: Draft): AuditEventFilters {
  const f: AuditEventFilters = {}
  if (d.actor) f.actor = d.actor
  if (d.action) f.action = d.action
  if (d.result) f.result = d.result
  if (d.from) f.from = new Date(d.from).toISOString()
  if (d.to) f.to = new Date(d.to).toISOString()
  return f
}

const resultTone: Record<AuditEvent['result'], 'success' | 'danger' | 'warning'> = {
  success: 'success',
  denied: 'danger',
  error: 'warning',
}

export function AuditPage() {
  useTitle('Audit log')
  const [draft, setDraft] = useState<Draft>(EMPTY_DRAFT)
  const [applied, setApplied] = useState<AuditEventFilters>({})

  const verify = useQuery({ queryKey: ['audit', 'verify'], queryFn: endpoints.verifyAudit })

  const events = useInfiniteQuery({
    queryKey: ['audit', 'events', applied],
    queryFn: ({ pageParam }: { pageParam: number | undefined }) =>
      endpoints.listAuditEvents({ ...applied, cursor: pageParam, limit: 50 }),
    initialPageParam: undefined as number | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  })

  function applyFilters() {
    setApplied(toApplied(draft))
  }
  function resetFilters() {
    setDraft(EMPTY_DRAFT)
    setApplied({})
  }

  let badge
  if (verify.isLoading) {
    badge = <Pill tone="muted">Verifying…</Pill>
  } else if (verify.isError) {
    badge = <Pill tone="danger">Verify failed</Pill>
  } else if (verify.data?.valid) {
    badge = <Pill tone="success" dot>Chain verified · {verify.data.count} events</Pill>
  } else {
    badge = <Pill tone="danger" dot>Chain broken at #{verify.data?.broken_at_seq}</Pill>
  }

  const forbidden =
    (events.error instanceof ApiError && events.error.status === 403) ||
    (verify.error instanceof ApiError && verify.error.status === 403)
  const rows: AuditEvent[] = events.data?.pages.flatMap((p) => p.events) ?? []

  return (
    <div>
      <div className="mb-3 flex items-start justify-between gap-3">
        <div>
          <h3 className="text-[15px] font-semibold text-ink">Audit log</h3>
          <p className="text-[12.5px] text-faint">All events across this instance</p>
        </div>
        <div className="flex items-center gap-2">{badge}</div>
      </div>

      <div className="mb-3 flex flex-wrap items-end gap-2 rounded-card border border-line bg-card p-3">
        <label className="flex flex-col gap-1 text-[10.5px] uppercase tracking-[.08em] text-faint">
          Actor
          <input
            aria-label="actor filter"
            value={draft.actor}
            onChange={(e) => setDraft((d) => ({ ...d, actor: e.target.value }))}
            className="rounded border border-line px-2.5 py-1.5 text-[12.5px] normal-case tracking-normal text-ink"
          />
        </label>
        <label className="flex flex-col gap-1 text-[10.5px] uppercase tracking-[.08em] text-faint">
          Action
          <input
            aria-label="action filter"
            value={draft.action}
            onChange={(e) => setDraft((d) => ({ ...d, action: e.target.value }))}
            className="rounded border border-line px-2.5 py-1.5 text-[12.5px] normal-case tracking-normal text-ink"
          />
        </label>
        <label className="flex flex-col gap-1 text-[10.5px] uppercase tracking-[.08em] text-faint">
          Result
          <select
            aria-label="result filter"
            value={draft.result}
            onChange={(e) => setDraft((d) => ({ ...d, result: e.target.value }))}
            className="rounded border border-line px-2.5 py-1.5 text-[12.5px] normal-case tracking-normal text-ink"
          >
            <option value="">All</option>
            <option value="success">success</option>
            <option value="denied">denied</option>
            <option value="error">error</option>
          </select>
        </label>
        <label className="flex flex-col gap-1 text-[10.5px] uppercase tracking-[.08em] text-faint">
          From
          <input
            aria-label="from filter"
            type="datetime-local"
            value={draft.from}
            onChange={(e) => setDraft((d) => ({ ...d, from: e.target.value }))}
            className="rounded border border-line px-2.5 py-1.5 text-[12.5px] normal-case tracking-normal text-ink"
          />
        </label>
        <label className="flex flex-col gap-1 text-[10.5px] uppercase tracking-[.08em] text-faint">
          To
          <input
            aria-label="to filter"
            type="datetime-local"
            value={draft.to}
            onChange={(e) => setDraft((d) => ({ ...d, to: e.target.value }))}
            className="rounded border border-line px-2.5 py-1.5 text-[12.5px] normal-case tracking-normal text-ink"
          />
        </label>
        <button
          type="button"
          onClick={applyFilters}
          className="rounded bg-brand px-3 py-1.5 text-[12.5px] font-semibold text-white shadow-card"
        >
          Apply
        </button>
        <button
          type="button"
          onClick={resetFilters}
          className="text-[12.5px] font-semibold text-faint hover:text-ink"
        >
          Reset
        </button>
        {!forbidden && (
          <div className="ml-auto flex flex-col items-end gap-1">
            <div className="flex gap-2">
              <a
                download
                href={endpoints.auditExportUrl(applied, 'jsonl')}
                className="rounded border border-line bg-card px-3 py-1.5 text-[12.5px] font-semibold text-ink"
              >
                Export JSONL
              </a>
              <a
                download
                href={endpoints.auditExportUrl(applied, 'csv')}
                className="rounded border border-line bg-card px-3 py-1.5 text-[12.5px] font-semibold text-ink"
              >
                Export CSV
              </a>
            </div>
            <p className="text-[11px] text-faint">Exports are audited.</p>
          </div>
        )}
      </div>

      {forbidden ? (
        <EmptyState
          title="Audit access required"
          hint="Ask an instance admin or owner for the audit role."
        />
      ) : events.isError ? (
        <p role="alert" className="text-[12.5px] text-danger">Couldn't load audit events.</p>
      ) : events.isLoading ? (
        <div className="flex flex-col gap-1.5" aria-hidden="true">
          {[0, 1, 2].map((i) => <div key={i} className="h-8 animate-pulse rounded bg-line-soft" />)}
        </div>
      ) : rows.length === 0 ? (
        <EmptyState title="No events match these filters." />
      ) : (
        <>
          <table className="w-full overflow-hidden rounded-card border border-line bg-card text-sm shadow-card">
            <thead>
              <tr className="text-left text-[10.5px] uppercase tracking-[.1em] text-faint">
                <th className="py-1.5">Time</th>
                <th className="py-1.5">Actor</th>
                <th className="py-1.5">Action</th>
                <th className="py-1.5">Resource</th>
                <th className="py-1.5">Result</th>
                <th className="py-1.5">Detail</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((e) => (
                <tr key={e.seq} className="border-t border-line-soft">
                  <td className="py-1"><span title={e.occurred_at}>{timeAgo(e.occurred_at)}</span></td>
                  <td className="py-1">
                    <div>{e.actor_name}</div>
                    <div className="text-[10.5px] text-faint">{e.actor_kind}</div>
                  </td>
                  <td className="py-1 font-mono text-[12px]">{e.action}</td>
                  <td className="max-w-[220px] truncate py-1 font-mono text-[12px]" title={e.resource}>
                    {e.resource}
                  </td>
                  <td className="py-1"><Pill tone={resultTone[e.result]}>{e.result}</Pill></td>
                  <td
                    className="max-w-[240px] truncate py-1 text-[12px] text-faint"
                    title={e.detail ?? undefined}
                  >
                    {e.detail}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {events.hasNextPage && (
            <button
              type="button"
              onClick={() => void events.fetchNextPage()}
              disabled={events.isFetchingNextPage}
              className="mt-3 rounded border border-line bg-card px-3 py-1.5 text-[12.5px] font-semibold disabled:opacity-40"
            >
              {events.isFetchingNextPage ? 'Loading…' : 'Load more'}
            </button>
          )}
        </>
      )}
    </div>
  )
}
