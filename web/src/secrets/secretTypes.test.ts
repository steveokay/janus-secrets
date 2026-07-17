import { describe, it, expect } from 'vitest'
import { SECRET_TYPES, SECRET_TYPE_ORDER, normalizeType } from './secretTypes'

describe('secretTypes', () => {
  it('normalizes unknown/empty to string', () => {
    expect(normalizeType(undefined)).toBe('string')
    expect(normalizeType('')).toBe('string')
    expect(normalizeType('bogus')).toBe('string')
    expect(normalizeType('json')).toBe('json')
  })
  it('multiline types are json/ssh_key/certificate/note', () => {
    expect(SECRET_TYPES.json.multiline).toBe(true)
    expect(SECRET_TYPES.ssh_key.multiline).toBe(true)
    expect(SECRET_TYPES.string.multiline).toBe(false)
    expect(SECRET_TYPES.password.multiline).toBe(false)
  })
  it('json validator rejects invalid json, accepts valid', () => {
    expect(SECRET_TYPES.json.validate!('{bad')).toBeTruthy()
    expect(SECRET_TYPES.json.validate!('{"a":1}')).toBeNull()
  })
  it('order covers all types', () => {
    expect(SECRET_TYPE_ORDER).toEqual(['string','password','json','ssh_key','certificate','note'])
  })
})
