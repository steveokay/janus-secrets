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
