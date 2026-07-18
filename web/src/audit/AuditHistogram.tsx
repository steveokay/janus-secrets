import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { endpoints, AuditEventFilters } from '../lib/endpoints'
import { pickBucket, zeroFill, Bucket } from './histogram'

const CHART_HEIGHT = 64 // px

function endOf(startISO: string, bucket: Bucket): string {
  const stepMs = bucket === 'hour' ? 3600_000 : 24 * 3600_000
  return new Date(new Date(startISO).getTime() + stepMs).toISOString()
}

function formatBarDate(iso: string): string {
  return new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

export function AuditHistogram({ filters, onRange }: {
  filters: AuditEventFilters
  onRange: (fromISO: string, toISO: string) => void
}) {
  const from = filters.from ?? new Date(Date.now() - 7 * 24 * 3600_000).toISOString()
  const to = filters.to ?? new Date().toISOString()
  const [bucket, setBucket] = useState<Bucket>(() => pickBucket(from, to))

  const { data } = useQuery({
    queryKey: ['audit', 'histogram', filters, bucket],
    queryFn: () => endpoints.auditHistogram({ ...filters, from, to, bucket }),
    retry: false,
  })

  const bars = zeroFill(data ?? [], from, to, bucket)
  const max = Math.max(1, ...bars.map((b) => b.success + b.denied + b.error))
  const hasActivity = bars.some((b) => b.success + b.denied + b.error > 0)

  return (
    <div className="mb-3 rounded-card border border-line bg-card p-3">
      <div className="mb-2 flex items-center justify-between gap-3">
        <div className="flex items-center gap-3 text-[11px] text-ink-faint">
          <Legend swatch="bg-success" label="success" />
          <Legend swatch="bg-danger" label="denied" />
          <Legend swatch="bg-warning" label="error" />
          <span>max {max}</span>
        </div>
        <div className="flex overflow-hidden rounded border border-line text-[11px]">
          <button
            type="button"
            aria-pressed={bucket === 'hour'}
            onClick={() => setBucket('hour')}
            className={bucket === 'hour' ? 'bg-brand-soft px-2 py-1 font-semibold text-brand-text' : 'bg-card px-2 py-1 text-ink-faint'}
          >
            Hour
          </button>
          <button
            type="button"
            aria-pressed={bucket === 'day'}
            onClick={() => setBucket('day')}
            className={bucket === 'day' ? 'bg-brand-soft px-2 py-1 font-semibold text-brand-text' : 'bg-card px-2 py-1 text-ink-faint'}
          >
            Day
          </button>
        </div>
      </div>

      {!hasActivity ? (
        <p className="py-4 text-center text-[12.5px] text-ink-faint">No activity in range</p>
      ) : (
        <div className="flex items-end gap-1 overflow-x-auto" style={{ height: CHART_HEIGHT }}>
          {bars.map((b) => {
            const total = b.success + b.denied + b.error
            const label = `${formatBarDate(b.start)} — ${total} events (${b.success} success, ${b.denied} denied, ${b.error} error)`
            const hSuccess = (b.success / max) * CHART_HEIGHT
            const hDenied = (b.denied / max) * CHART_HEIGHT
            const hError = (b.error / max) * CHART_HEIGHT
            return (
              <button
                key={b.start}
                type="button"
                aria-label={label}
                title={label}
                onClick={() => onRange(b.start, endOf(b.start, bucket))}
                className="flex w-3 shrink-0 flex-col justify-end"
                style={{ height: CHART_HEIGHT }}
              >
                {hError > 0 && <span className="w-full bg-warning" style={{ height: hError }} />}
                {hDenied > 0 && <span className="w-full bg-danger" style={{ height: hDenied }} />}
                {hSuccess > 0 && <span className="w-full bg-success" style={{ height: hSuccess }} />}
              </button>
            )
          })}
        </div>
      )}
    </div>
  )
}

function Legend({ swatch, label }: { swatch: string; label: string }) {
  return (
    <span className="flex items-center gap-1">
      <span className={`h-2 w-2 rounded-full ${swatch}`} />
      {label}
    </span>
  )
}
