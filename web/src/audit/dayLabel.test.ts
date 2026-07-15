import { dayLabel } from './dayLabel'

const NOW = new Date('2026-07-15T12:00:00Z')

test('today / yesterday / absolute date', () => {
  expect(dayLabel('2026-07-15T09:30:00Z', NOW)).toBe('Today')
  // Mid-day inputs so the label doesn't drift across a local-day boundary in
  // CI/dev timezones (e.g. the note's warning about T23:00Z / T01:00Z inputs).
  expect(dayLabel('2026-07-14T12:00:00Z', NOW)).toBe('Yesterday')
  expect(dayLabel('2026-07-01T09:30:00Z', NOW)).toBe('2026-07-01')
})

test('groups consecutive rows by calendar day', () => {
  // Same local calendar day for any UTC offset within +/-11h of noon.
  expect(dayLabel('2026-07-15T09:00:00Z', NOW)).toBe(dayLabel('2026-07-15T15:00:00Z', NOW))
})
