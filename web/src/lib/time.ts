// Coarse relative time for dashboard rows. `now` is injectable for tests.
export function timeAgo(iso: string, now: Date = new Date()): string {
  const s = Math.max(0, Math.floor((now.getTime() - new Date(iso).getTime()) / 1000))
  if (s < 60) return 'just now'
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ago`
  const d = Math.floor(h / 24)
  if (d <= 30) return `${d}d ago`
  return new Date(iso).toLocaleDateString()
}
