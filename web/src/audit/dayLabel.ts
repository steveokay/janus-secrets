// Client-side calendar-day label for grouping audit rows. "Today" / "Yesterday"
// are relative to `now` (local time); older days render as an ISO date. Pure
// presentation over already-fetched rows — no fetch, no aggregation.
function ymd(d: Date): string {
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

export function dayLabel(iso: string, now: Date = new Date()): string {
  const d = new Date(iso)
  const key = ymd(d)
  if (key === ymd(now)) return 'Today'
  const yest = new Date(now)
  yest.setDate(now.getDate() - 1)
  if (key === ymd(yest)) return 'Yesterday'
  return key
}
