import { describe, expect, test } from 'vitest'
import { relativeTime } from './relativeTime'

const NOW = new Date('2026-07-13T12:00:00Z')

describe('relativeTime', () => {
  test.each([
    ['2026-07-13T11:59:40Z', 'just now'],
    ['2026-07-13T11:58:00Z', '2m ago'],
    ['2026-07-13T09:00:00Z', '3h ago'],
    ['2026-07-10T12:00:00Z', '3d ago'],
    ['2026-05-13T12:00:00Z', 'May 13'],
    ['2026-07-13T12:02:00Z', 'in 2m'],
    ['2026-07-16T13:00:00Z', 'in 3d'],
  ])('%s → %s', (iso, want) => {
    expect(relativeTime(iso, NOW)).toBe(want)
  })
  test('invalid input returns empty string', () => {
    expect(relativeTime('not-a-date', NOW)).toBe('')
  })
})
