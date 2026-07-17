import { rowState, parseDotenv } from './rowState'
import type { MaskedSecret } from '../lib/endpoints'

const masked: Record<string, MaskedSecret> = {
  OWN: { value_version: 3, created_at: '', origin: 'own' },
  INH: { value_version: 1, created_at: '', origin: 'inherited' },
}
const original = { OWN: 'a' }

const maskedTyped: Record<string, MaskedSecret> = {
  OWN: { value_version: 3, created_at: '', origin: 'own', type: 'string' },
}

test('no buffer entry → no change, server origin', () => {
  expect(rowState('OWN', masked, {}, original)).toEqual({ change: null, origin: 'own', existing: true })
  expect(rowState('INH', masked, {}, original)).toEqual({ change: null, origin: 'inherited', existing: true })
})
test('editing an own key to a new value → edited', () => {
  expect(rowState('OWN', masked, { OWN: { value: 'b' } }, original)).toMatchObject({ change: 'edited', origin: 'own' })
})
test('buffer value equal to original → not a change', () => {
  expect(rowState('OWN', masked, { OWN: { value: 'a' } }, original).change).toBeNull()
})
test('editing an inherited key → edited + overridden', () => {
  expect(rowState('INH', masked, { INH: { value: 'x' } }, original)).toMatchObject({ change: 'edited', origin: 'overridden' })
})
test('removing an existing key → removed', () => {
  expect(rowState('OWN', masked, { OWN: { value: null } }, original)).toMatchObject({ change: 'removed' })
})
test('a brand-new key → added', () => {
  expect(rowState('NEW', masked, { NEW: { value: 'v' } }, original)).toMatchObject({ change: 'added', existing: false })
})

test('type-only change on an existing key → edited, value unchanged', () => {
  expect(
    rowState('OWN', maskedTyped, { OWN: { value: 'a', type: 'password' } }, original),
  ).toMatchObject({ change: 'edited', origin: 'own', existing: true })
})
test('same value and same type as server → not a change', () => {
  expect(
    rowState('OWN', maskedTyped, { OWN: { value: 'a', type: 'string' } }, original).change,
  ).toBeNull()
})
test('no buffer type recorded (undefined) is treated as the server type → not a change', () => {
  expect(rowState('OWN', maskedTyped, { OWN: { value: 'a' } }, original).change).toBeNull()
})

test('parseDotenv: KEY=VALUE, comments, blanks, quotes, invalid', () => {
  const r = parseDotenv(['# comment', '', 'A=1', 'B="two words"', "C='q'", 'bad key=x', 'D=', 'nokeyval'].join('\n'))
  expect(r.pairs).toEqual({ A: '1', B: 'two words', C: 'q', D: '' })
  expect(r.skipped).toBe(2) // 'bad key=x' (invalid key) + 'nokeyval' (no '='); blank + '#' are ignored, not counted
})
