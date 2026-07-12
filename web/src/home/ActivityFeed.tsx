import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { endpoints } from '../lib/endpoints'
import { relativeTime } from '../lib/relativeTime'
import { resultTone } from '../audit/resultTone'
import { Card } from '../ui/Card'
import { Pill } from '../ui/Pill'
import { Skeleton } from '../ui/Skeleton'

export function ActivityFeed() {
  const q = useQuery({
    queryKey: ['audit', 'events', { limit: 8 }],
    queryFn: () => endpoints.listAuditEvents({ limit: 8 }),
    retry: false,
  })

  if (q.isLoading) return <Skeleton className="mb-6 h-[290px] rounded-card" />
  // Section hides on error (e.g. 403) rather than erroring.
  if (q.isError) return null

  const events = q.data?.events ?? []

  return (
    <Card className="mb-6">
      <div className="px-4 pt-3 pb-1 text-[10px] font-semibold uppercase tracking-[.1em] text-ink-faint">
        Recent activity
      </div>
      <ul>
        {events.length === 0 && (
          <li className="px-4 py-2 text-[11px] text-ink-faint">No activity yet</li>
        )}
        {events.map((e) => (
          <li
            key={e.seq}
            className="flex items-center gap-2.5 border-b border-line-soft px-4 py-2 last:border-b-0"
          >
            <Pill tone={resultTone[e.result]} dot className="shrink-0">{e.result}</Pill>
            <span className="font-mono text-[11px] text-ink">{e.action}</span>
            <span className="min-w-0 flex-1 truncate text-[11px] text-ink-faint" title={e.resource}>
              {e.resource} · {e.actor_name}
            </span>
            <span className="shrink-0 text-[11px] tabular-nums text-ink-faint" title={e.occurred_at}>
              {relativeTime(e.occurred_at)}
            </span>
          </li>
        ))}
      </ul>
      <div className="px-4 py-2">
        <Link to="/audit" className="text-[11px] text-brand-text">
          View all →
        </Link>
      </div>
    </Card>
  )
}
