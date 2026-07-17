import { emptyBuffer, setValue, removeKey, addKey, setType, summarize, toChanges, isDirty } from './dirty'

// original = the config's own raw values (what a save diffs against)
const original = { DB_URL: 'postgres://a', LOG_LEVEL: 'info' }

test('editing an existing key marks it changed', () => {
  const b = setValue(emptyBuffer(), 'LOG_LEVEL', 'debug')
  expect(isDirty(b)).toBe(true)
  expect(summarize(b, original)).toEqual({ added: 0, changed: 1, removed: 0 })
  expect(toChanges(b, original)).toEqual([{ key: 'LOG_LEVEL', value: 'debug' }])
})

test('setting a key back to its original value is not a change', () => {
  const b = setValue(setValue(emptyBuffer(), 'LOG_LEVEL', 'debug'), 'LOG_LEVEL', 'info')
  expect(summarize(b, original)).toEqual({ added: 0, changed: 0, removed: 0 })
  expect(toChanges(b, original)).toEqual([])
})

test('adding a new key and removing an existing one', () => {
  let b = addKey(emptyBuffer(), 'FEATURE_X', 'on')
  b = removeKey(b, 'DB_URL')
  expect(summarize(b, original)).toEqual({ added: 1, changed: 0, removed: 1 })
  expect(toChanges(b, original)).toEqual(
    expect.arrayContaining([{ key: 'FEATURE_X', value: 'on' }, { key: 'DB_URL', delete: true }]),
  )
})

test('removing a key that never existed contributes nothing', () => {
  const b = removeKey(emptyBuffer(), 'NOPE')
  expect(toChanges(b, original)).toEqual([])
  expect(isDirty(b)).toBe(false)
})

test('setType records a type on a key with no prior buffer entry', () => {
  const b = setType(emptyBuffer(), 'LOG_LEVEL', 'password')
  expect(b.LOG_LEVEL).toEqual({ value: null, type: 'password' })
})

test('setType preserves an existing value already in the buffer', () => {
  const b = setType(setValue(emptyBuffer(), 'LOG_LEVEL', 'debug'), 'LOG_LEVEL', 'password')
  expect(b.LOG_LEVEL).toEqual({ value: 'debug', type: 'password' })
})

test('setValue preserves a type already recorded in the buffer', () => {
  const b = setValue(setType(emptyBuffer(), 'LOG_LEVEL', 'password'), 'LOG_LEVEL', 'debug')
  expect(b.LOG_LEVEL).toEqual({ value: 'debug', type: 'password' })
})

test('removeKey drops any staged type (a deleted row has no type)', () => {
  const b = removeKey(setType(emptyBuffer(), 'LOG_LEVEL', 'password'), 'LOG_LEVEL')
  expect(b.LOG_LEVEL).toEqual({ value: null })
})

// --- type-aware save (Part 1) ---

const serverTypes = { DB_URL: 'string', LOG_LEVEL: 'string' }

test('toChanges includes the buffer type when set on a value change', () => {
  const b = setType(setValue(emptyBuffer(), 'LOG_LEVEL', 'debug'), 'LOG_LEVEL', 'password')
  expect(toChanges(b, original, serverTypes)).toEqual([{ key: 'LOG_LEVEL', value: 'debug', type: 'password' }])
})

test('toChanges omits type when the buffer entry has none set', () => {
  const b = setValue(emptyBuffer(), 'LOG_LEVEL', 'debug')
  expect(toChanges(b, original, serverTypes)).toEqual([{ key: 'LOG_LEVEL', value: 'debug' }])
})

test('a type-only change (no value edit) makes isDirty true and is included in toChanges', () => {
  const b = setType(emptyBuffer(), 'LOG_LEVEL', 'password')
  expect(isDirty(b, original, serverTypes)).toBe(true)
  expect(toChanges(b, original, serverTypes)).toEqual([{ key: 'LOG_LEVEL', value: 'info', type: 'password' }])
})

test('setting the type back to the server type (no value change) is not dirty', () => {
  const b = setType(emptyBuffer(), 'LOG_LEVEL', 'string')
  expect(isDirty(b, original, serverTypes)).toBe(false)
  expect(toChanges(b, original, serverTypes)).toEqual([])
})

test('summarize counts a type-only change as changed', () => {
  const b = setType(emptyBuffer(), 'LOG_LEVEL', 'password')
  expect(summarize(b, original, serverTypes)).toEqual({ added: 0, changed: 1, removed: 0 })
})

test('a type-only change on a key with no server type is still dirty when set to non-default', () => {
  const b = setType(emptyBuffer(), 'FEATURE_X', 'json')
  // FEATURE_X isn't in `original`, so a type-only entry with no value is not a
  // meaningful add — nothing to send, not dirty.
  expect(isDirty(b, original, serverTypes)).toBe(false)
  expect(toChanges(b, original, serverTypes)).toEqual([])
})

test('toChanges omits type on a delete', () => {
  const b = removeKey(emptyBuffer(), 'DB_URL')
  expect(toChanges(b, original, serverTypes)).toEqual([{ key: 'DB_URL', delete: true }])
})

test('a type-only change on a server-known key whose raw value has not landed in `original` yet is not sent (never misread as a delete)', () => {
  // DB_URL is known server-side (serverTypes) but its raw value hasn't been
  // fetched into `original` (e.g. the reveal is still in flight). Emitting
  // anything here would be unsafe: toChanges would misread value:null as a
  // delete for a key the caller never asked to remove.
  const b = setType(emptyBuffer(), 'DB_URL', 'json')
  const partialOriginal = { LOG_LEVEL: 'info' } // DB_URL missing on purpose
  expect(isDirty(b, partialOriginal, serverTypes)).toBe(false)
  expect(toChanges(b, partialOriginal, serverTypes)).toEqual([])
})
