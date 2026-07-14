import { expect, test } from 'vitest'
import { sortRows } from './sortRows'
import type { MaskedSecret } from '../lib/endpoints'

const masked: Record<string, MaskedSecret> = {
  BETA:  { value_version: 2, created_at: '2026-01-02T00:00:00Z', origin: 'own' },
  alpha: { value_version: 5, created_at: '2026-01-03T00:00:00Z', origin: 'inherited' },
  GAMMA: { value_version: 1, created_at: '2026-01-01T00:00:00Z', origin: 'overridden' },
}
const rows = ['BETA', 'alpha', 'GAMMA']

test('null sort returns rows unchanged (new array)', () => {
  const out = sortRows(rows, masked, null)
  expect(out).toEqual(rows)
  expect(out).not.toBe(rows)
})

test('key asc/desc is case-insensitive', () => {
  expect(sortRows(rows, masked, { key: 'key', dir: 'asc' })).toEqual(['alpha', 'BETA', 'GAMMA'])
  expect(sortRows(rows, masked, { key: 'key', dir: 'desc' })).toEqual(['GAMMA', 'BETA', 'alpha'])
})

test('version and updated sort numerically / chronologically', () => {
  expect(sortRows(rows, masked, { key: 'version', dir: 'asc' })).toEqual(['GAMMA', 'BETA', 'alpha'])
  expect(sortRows(rows, masked, { key: 'updated', dir: 'desc' })).toEqual(['alpha', 'BETA', 'GAMMA'])
})

test('origin sorts alphabetically (inherited<overridden<own), key breaks ties', () => {
  expect(sortRows(rows, masked, { key: 'origin', dir: 'asc' })).toEqual(['alpha', 'GAMMA', 'BETA'])
})

test('added rows (no masked entry) pin to top in both directions', () => {
  const withAdded = ['BETA', 'NEWKEY', 'alpha']
  const asc = sortRows(withAdded, masked, { key: 'key', dir: 'asc' })
  const desc = sortRows(withAdded, masked, { key: 'key', dir: 'desc' })
  expect(asc[0]).toBe('NEWKEY')
  expect(desc[0]).toBe('NEWKEY')
})
