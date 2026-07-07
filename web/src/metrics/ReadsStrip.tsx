import type { UseQueryResult } from '@tanstack/react-query'
import { Activity } from 'lucide-react'
import type { Reads24h } from '../lib/endpoints'
import { useReads24h, useProjectReads24h } from './hooks'

function TopList({ title, items }: { title: string; items: { id: string; name: string; reads: number }[] }) {
  if (items.length === 0) return null
  return (
    <div className="min-w-0 flex-1">
      <div className="mb-1.5 text-[11px] font-semibold uppercase tracking-[.08em] text-faint">{title}</div>
      <ul className="flex flex-col gap-1">
        {items.map((it) => (
          <li key={it.id} className="flex items-center justify-between gap-3">
            <span className="truncate font-mono text-[12px] text-muted">{it.name}</span>
            <span className="shrink-0 tabular-nums text-[12px] text-faint">{it.reads.toLocaleString()}</span>
          </li>
        ))}
      </ul>
    </div>
  )
}

function StripBody({ data }: { data: Reads24h }) {
  const configs = data.top_configs.map((c) => ({ id: c.config_id, name: c.config_name, reads: c.reads }))
  const tokens = data.top_tokens.map((t) => ({ id: t.token_id, name: t.token_name, reads: t.reads }))
  return (
    <div data-metrics-strip className="mb-5 rounded-card border border-line bg-card p-4">
      <div className="flex flex-wrap items-start gap-x-8 gap-y-4">
        <div className="shrink-0">
          <div className="flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-[.08em] text-faint">
            <Activity size={13} strokeWidth={1.7} /> Reads 24h
          </div>
          <div className="mt-1 text-[28px] font-semibold tabular-nums text-ink">{data.reads_24h.toLocaleString()}</div>
          {data.reads_24h === 0 && <div className="text-[12px] text-faint">No reads yet</div>}
        </div>
        <TopList title="Top configs" items={configs} />
        <TopList title="Top tokens" items={tokens} />
      </div>
    </div>
  )
}

// Supplementary UI: it must never block or error the surrounding page.
// Hide on error/403; show a skeleton while loading.
function StripView({ q }: { q: UseQueryResult<Reads24h> }) {
  if (q.isError) return null
  if (q.isLoading) return <div aria-hidden className="mb-5 h-24 rounded-card bg-line-soft" />
  if (!q.data) return null
  return <StripBody data={q.data} />
}

// Two thin wrappers so each calls exactly one hook (rules-of-hooks safe).
export function InstanceReadsStrip() {
  return <StripView q={useReads24h()} />
}
export function ProjectReadsStrip({ pid }: { pid: string }) {
  return <StripView q={useProjectReads24h(pid)} />
}
