import { timeAgo } from './time'

const now = new Date('2026-07-06T12:00:00Z')

test.each([
  ['2026-07-06T11:59:30Z', 'just now'],
  ['2026-07-06T11:59:00Z', '1m ago'],
  ['2026-07-06T11:01:00Z', '59m ago'],
  ['2026-07-06T11:00:00Z', '1h ago'],
  ['2026-07-05T13:00:00Z', '23h ago'],
  ['2026-07-05T12:00:00Z', '1d ago'],
  ['2026-06-06T12:00:00Z', '30d ago'],
])('timeAgo(%s) → %s', (iso, expected) => {
  expect(timeAgo(iso, now)).toBe(expected)
})

test('older than 30 days falls back to locale date', () => {
  expect(timeAgo('2026-06-05T12:00:00Z', now)).toBe(new Date('2026-06-05T12:00:00Z').toLocaleDateString())
})

test('future timestamps clamp to just now', () => {
  expect(timeAgo('2026-07-06T12:05:00Z', now)).toBe('just now')
})
