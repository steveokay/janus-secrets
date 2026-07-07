import { expect, test } from 'vitest'
import { fuzzyScore } from './fuzzy'

test('empty query matches everything with score 0', () => {
  expect(fuzzyScore('', 'anything')).toBe(0)
})

test('substring match scores higher than scattered subsequence', () => {
  const contiguous = fuzzyScore('prod', 'production')
  const scattered = fuzzyScore('prod', 'p-r-o-d-uction')
  expect(contiguous).not.toBeNull()
  expect(scattered).not.toBeNull()
  expect((contiguous as number) > (scattered as number)).toBe(true)
})

test('prefix match scores higher than mid-string match', () => {
  const prefix = fuzzyScore('api', 'api-gateway')
  const mid = fuzzyScore('api', 'legacy-api')
  expect((prefix as number) > (mid as number)).toBe(true)
})

test('non-subsequence returns null', () => {
  expect(fuzzyScore('xyz', 'production')).toBeNull()
})

test('case-insensitive', () => {
  expect(fuzzyScore('PROD', 'production')).not.toBeNull()
})
