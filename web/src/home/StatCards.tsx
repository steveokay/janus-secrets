import type { ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { endpoints, type Project } from '../lib/endpoints'
import { relativeTime } from '../lib/relativeTime'
import { useReads24h } from '../metrics/hooks'
import { opsEndpoints } from '../operations/endpoints'
import { useFanOut } from '../operations/useAggregated'
import { Card } from '../ui/Card'
import { cn } from '../ui/cn'

// MUST match the queryKey prefixes used by useRotation/useSync in
// web/src/operations/useAggregated.ts (per-project key ['ops','rotation',pid])
// so the home cards and the /operations page share the same cache entries.
const ROTATION_KEY = ['ops', 'rotation'] as const
const SYNC_KEY = ['ops', 'sync'] as const

function Stat({ label, value, tone, sub, hidden }: {
  label: string; value: ReactNode; tone?: 'ok' | 'warn'; sub?: ReactNode; hidden?: boolean
}) {
  if (hidden) return null
  return (
    <Card className="p-4">
      <div className="text-[10px] font-semibold uppercase tracking-[.1em] text-ink-faint">{label}</div>
      <div className={cn('mt-1 text-[22px] font-bold tabular-nums',
        tone === 'ok' ? 'text-success' : tone === 'warn' ? 'text-warning' : 'text-ink-hi')}>{value}</div>
      {sub && <div className="mt-1 truncate text-[11px] text-ink-faint">{sub}</div>}
    </Card>
  )
}

function ReadsStat() {
  const q = useReads24h()
  const top = (q.data?.top_configs ?? []).slice(0, 3)
  return (
    <Stat
      label="Reads 24h"
      hidden={q.isError}
      value={q.data ? q.data.reads_24h.toLocaleString() : '—'}
      sub={top.length > 0 && (
        <span className="flex flex-col">
          {top.map((c) => (
            <span key={c.config_id} className="truncate">
              <span className="font-mono text-ink-mute">{c.config_name}</span> · {c.reads}
            </span>
          ))}
        </span>
      )}
    />
  )
}

// Shared value/tone rule for the rotation + sync engine cards.
function engineValue(all: { status: string }[]): { value: ReactNode; tone?: 'ok' | 'warn' } {
  const failed = all.filter((p) => p.status === 'failed').length
  if (failed > 0) return { value: `${failed} failing`, tone: 'warn' }
  if (all.length === 0) return { value: 0 }
  return { value: `${all.length} healthy`, tone: 'ok' }
}

/** Soonest upcoming timestamp among active items, or undefined when none. */
function soonest<T extends { status: string }>(all: T[], at: (x: T) => string): string | undefined {
  const times = all.filter((x) => x.status === 'active').map(at).filter(Boolean)
  if (times.length === 0) return undefined
  return times.reduce((a, b) => (new Date(a).getTime() <= new Date(b).getTime() ? a : b))
}

function RotationsStat({ projects }: { projects: Project[] }) {
  const { perScope, isLoading, isError, someForbidden } = useFanOut(projects, ROTATION_KEY, opsEndpoints.rotation.list)
  const all = perScope.flatMap((s) => s.data)
  const { value, tone } = engineValue(all)
  const next = soonest(all, (p) => p.next_rotation_at)
  return (
    <Stat
      label="Rotations"
      hidden={isError || (someForbidden && all.length === 0)}
      value={isLoading ? '—' : value}
      tone={isLoading ? undefined : tone}
      sub={!isLoading && next ? `next: ${relativeTime(next)}` : undefined}
    />
  )
}

function SyncsStat({ projects }: { projects: Project[] }) {
  const { perScope, isLoading, isError, someForbidden } = useFanOut(projects, SYNC_KEY, opsEndpoints.sync.list)
  const all = perScope.flatMap((s) => s.data)
  const { value, tone } = engineValue(all)
  // last_error is a server-sanitized operational message (never a secret);
  // /operations renders it the same way.
  const firstFailed = all.find((t) => t.status === 'failed')
  const next = soonest(all, (t) => t.next_sync_at)
  const sub = firstFailed?.last_error ?? (next ? `next: ${relativeTime(next)}` : undefined)
  return (
    <Stat
      label="Syncs"
      hidden={isError || (someForbidden && all.length === 0)}
      value={isLoading ? '—' : value}
      tone={isLoading ? undefined : tone}
      sub={isLoading ? undefined : sub}
    />
  )
}

// Card 4 = Audit chain: GET /v1/dynamic/leases requires role_id (the handler
// 400s without it — "role_id is required"), so there is no cheap all-leases
// count. Shares queryKey ['audit','verify'] with HomeHeader's badge.
function AuditStat() {
  const q = useQuery({ queryKey: ['audit', 'verify'], queryFn: endpoints.verifyAudit, retry: false })
  return (
    <Stat
      label="Audit events"
      hidden={q.isError}
      value={q.data ? q.data.count.toLocaleString() : '—'}
      tone={q.data ? (q.data.valid ? 'ok' : 'warn') : undefined}
      sub={q.data ? (q.data.valid ? 'chain verified' : 'chain integrity FAILED') : undefined}
    />
  )
}

export function StatCards({ projects }: { projects: Project[] }) {
  return (
    <div className="mb-6 grid grid-cols-2 gap-3 lg:grid-cols-4">
      <ReadsStat />
      <RotationsStat projects={projects} />
      <SyncsStat projects={projects} />
      <AuditStat />
    </div>
  )
}
