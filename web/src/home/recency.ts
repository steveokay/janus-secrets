import { relativeTime } from '../lib/relativeTime'

/** Card recency line: "active 2h ago" if there's activity, else "created 3d ago", else "". */
export function recencyLabel(x: { created_at?: string; last_activity_at?: string | null }): string {
  if (x.last_activity_at) return `active ${relativeTime(x.last_activity_at)}`
  if (x.created_at) return `created ${relativeTime(x.created_at)}`
  return ''
}
