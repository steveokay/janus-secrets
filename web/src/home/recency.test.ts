import { describe, it, expect } from 'vitest'
import { recencyLabel } from './recency'

describe('recencyLabel', () => {
  it('prefers activity when present', () => {
    const l = recencyLabel({ created_at: '2020-01-01T00:00:00Z', last_activity_at: '2020-06-01T00:00:00Z' })
    expect(l).toMatch(/^active /)
  })
  it('falls back to created when no activity', () => {
    const l = recencyLabel({ created_at: '2020-01-01T00:00:00Z', last_activity_at: null })
    expect(l).toMatch(/^created /)
  })
  it('returns empty string when nothing known', () => {
    expect(recencyLabel({})).toBe('')
  })
})
