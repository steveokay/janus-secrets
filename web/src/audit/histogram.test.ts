import { describe, it, expect } from 'vitest'
import { pickBucket, zeroFill } from './histogram'

describe('histogram helpers', () => {
  it('pickBucket: <=48h span → hour, else day', () => {
    expect(pickBucket('2026-01-01T00:00:00Z', '2026-01-02T00:00:00Z')).toBe('hour')
    expect(pickBucket('2026-01-01T00:00:00Z', '2026-01-10T00:00:00Z')).toBe('day')
  })
  it('zeroFill inserts empty buckets across the range at the given granularity', () => {
    const filled = zeroFill(
      [{ start: '2026-01-02T00:00:00Z', success: 5, denied: 0, error: 0 }],
      '2026-01-01T00:00:00Z', '2026-01-03T00:00:00Z', 'day',
    )
    expect(filled).toHaveLength(3)
    expect(filled[1]).toEqual({ start: '2026-01-02T00:00:00Z', success: 5, denied: 0, error: 0 })
    expect(filled[0].success + filled[0].denied + filled[0].error).toBe(0)
  })
})
