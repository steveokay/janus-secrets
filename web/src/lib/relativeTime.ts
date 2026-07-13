const MIN = 60_000, HOUR = 3_600_000, DAY = 86_400_000

/**
 * "2m ago" / "in 3d" style relative time; falls back to "Mon D" past ~30 days.
 * Bidirectional (past AND future); floor semantics.
 */
export function relativeTime(iso: string, now: Date = new Date()): string {
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return ''
  const diff = t - now.getTime()
  const abs = Math.abs(diff)
  if (abs < MIN) return diff <= 0 ? 'just now' : 'in 1m'
  const fmt = (n: number, u: string) => (diff < 0 ? `${n}${u} ago` : `in ${n}${u}`)
  if (abs < HOUR) return fmt(Math.floor(abs / MIN), 'm')
  if (abs < DAY) return fmt(Math.floor(abs / HOUR), 'h')
  if (abs < 30 * DAY) return fmt(Math.floor(abs / DAY), 'd')
  return new Date(t).toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
}
