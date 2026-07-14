import { expect, test } from 'vitest'
import { toEnvText } from './exportEnv'
import { parseDotenv } from './rowState'

test('formats bare KEY=VALUE lines, sorted by key', () => {
  expect(toEnvText([['B', 'two'], ['A', 'one']])).toBe('A=one\nB=two')
})

test('quotes values that need protection', () => {
  expect(toEnvText([['K', 'a b']])).toBe('K="a b"')        // whitespace
  expect(toEnvText([['K', 'a#b']])).toBe('K="a#b"')        // comment char
  expect(toEnvText([['K', 'a"b']])).toBe(`K='a"b'`)        // " value single-quoted (no escape)
})

test('round-trips through parseDotenv for representative values', () => {
  const pairs = { PLAIN: 'postgres://a', SPACED: 'has space', HASH: 'a#b', EMPTY: '' }
  const text = toEnvText(Object.entries(pairs))
  expect(parseDotenv(text).pairs).toEqual(pairs)
})

test('double-quote values round-trip via single-quoting', () => {
  const pairs = { Q: 'a"b', PLAIN: 'x' }
  const text = toEnvText(Object.entries(pairs))
  expect(parseDotenv(text).pairs).toEqual(pairs)
})

test('values with both quote kinds fall back to escaped double-quote (lossy on Janus re-import)', () => {
  expect(toEnvText([['K', `a"'b`]])).toBe(`K="a\\"'b"`)
})
