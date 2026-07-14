import { useEffect, useState } from 'react'
import { Sheet } from '../ui/Sheet'
import { Button } from '../ui/Button'
import { Skeleton } from '../ui/Skeleton'
import { EmptyState } from '../ui/EmptyState'
import { ApiError } from '../lib/api'
import { type RunView, type RunsPage } from './endpoints'
import { StatusPill, RelTime, LastError } from './ops-ui'

// Value-free run drill-in: renders only timing/status/config-version/attempt and
// a sanitized error CATEGORY (never a secret/DSN/key value). `load` returns a
// masked page from the audited runs endpoint.
export function RunHistorySheet({ open, onOpenChange, title, load }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  load: (cursor?: number) => Promise<RunsPage>
}) {
  const [runs, setRuns] = useState<RunView[]>([])
  const [cursor, setCursor] = useState<number | null>(null)
  const [loading, setLoading] = useState(false)
  const [forbidden, setForbidden] = useState(false)
  const [errored, setErrored] = useState(false)

  async function loadPage(c?: number) {
    setLoading(true)
    try {
      const p = await load(c)
      setRuns((prev) => (c ? [...prev, ...(p.runs ?? [])] : (p.runs ?? [])))
      setCursor(p.next_cursor)
    } catch (e) {
      if (e instanceof ApiError && e.status === 403) setForbidden(true)
      else setErrored(true)
    } finally {
      setLoading(false)
    }
  }

  // Open edge: reset state and load the first page.
  useEffect(() => {
    if (!open) return
    setRuns([])
    setCursor(null)
    setForbidden(false)
    setErrored(false)
    loadPage()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  return (
    <Sheet open={open} onOpenChange={onOpenChange} title={title}>
      {forbidden ? (
        <EmptyState title="Access required" hint="Ask a project admin for the manage role." />
      ) : errored ? (
        <p role="alert" className="text-danger">Couldn't load run history.</p>
      ) : loading && runs.length === 0 ? (
        <div className="space-y-2">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-9 w-full" />)}</div>
      ) : runs.length === 0 ? (
        <EmptyState title="No runs recorded yet" />
      ) : (
        <div className="flex flex-col gap-3">
          <div className="overflow-x-auto rounded-card border border-line bg-surface-2">
            <table className="w-full text-[12px]">
              <thead>
                <tr className="border-b border-line bg-surface-1 text-left text-ink-faint">
                  <th className="px-2 py-1.5 font-medium">When</th>
                  <th className="px-2 py-1.5 font-medium">Status</th>
                  <th className="px-2 py-1.5 font-medium">Duration</th>
                  <th className="px-2 py-1.5 font-medium">Cfg</th>
                  <th className="px-2 py-1.5 font-medium">Attempt</th>
                </tr>
              </thead>
              <tbody>
                {runs.map((r) => (
                  <tr key={r.id} className="border-b border-line-soft">
                    <td className="px-2 py-1.5"><RelTime iso={r.started_at} /></td>
                    <td className="px-2 py-1.5">
                      <span className="inline-flex items-center gap-1">
                        <StatusPill status={r.status} />
                        {r.status === 'failure' && <LastError text={r.error} />}
                      </span>
                    </td>
                    <td className="px-2 py-1.5 text-ink-mute">{fmtDuration(r.started_at, r.ended_at)}</td>
                    <td className="px-2 py-1.5 text-ink-mute">
                      {r.config_version != null ? `v${r.config_version}` : '—'}
                      {r.keys_count != null && <span className="text-ink-faint"> · {r.keys_count} keys</span>}
                    </td>
                    <td className="px-2 py-1.5 text-ink-mute">{r.attempt_num}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {cursor != null && (
            <div className="flex justify-center">
              <Button size="sm" variant="ghost" loading={loading} onClick={() => loadPage(cursor)}>Load more</Button>
            </div>
          )}
        </div>
      )}
    </Sheet>
  )
}

// Milliseconds between two ISO timestamps, rendered compactly. NaN → "—".
function fmtDuration(start: string, end: string): string {
  const ms = new Date(end).getTime() - new Date(start).getTime()
  // NaN (bad timestamp) or a negative delta (clock skew between the two write
  // points) is not a meaningful duration → render a dash.
  if (Number.isNaN(ms) || ms < 0) return '—'
  if (ms < 1000) return `${ms}ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`
  return `${Math.round(ms / 60_000)}m`
}
