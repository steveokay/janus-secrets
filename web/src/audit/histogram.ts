import type { HistBucket } from '../lib/endpoints'

export type Bucket = 'hour' | 'day'

/** Auto granularity: spans up to 48h use hourly buckets, longer spans daily. */
export function pickBucket(fromISO: string, toISO: string): Bucket {
  const span = new Date(toISO).getTime() - new Date(fromISO).getTime()
  return span <= 48 * 3600_000 ? 'hour' : 'day'
}

function truncate(d: Date, bucket: Bucket): Date {
  const c = new Date(d)
  c.setUTCMinutes(0, 0, 0)
  if (bucket === 'day') c.setUTCHours(0)
  return c
}

/** Fill empty buckets across [from,to] at the given granularity, merging counts.
 *  Keys align to UTC hour/day floors, matching Postgres date_trunc. */
export function zeroFill(buckets: HistBucket[], fromISO: string, toISO: string, bucket: Bucket): HistBucket[] {
  const byStart = new Map(buckets.map((b) => [new Date(b.start).getTime(), b]))
  const stepMs = bucket === 'hour' ? 3600_000 : 24 * 3600_000
  const out: HistBucket[] = []
  let t = truncate(new Date(fromISO), bucket).getTime()
  const end = truncate(new Date(toISO), bucket).getTime()
  for (; t <= end; t += stepMs) {
    out.push(byStart.get(t) ?? { start: new Date(t).toISOString(), success: 0, denied: 0, error: 0 })
  }
  return out
}
