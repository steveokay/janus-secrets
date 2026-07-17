import { describe, it, expect } from 'vitest'
import { generatePassword } from './generatePassword'
describe('generatePassword', () => {
  it('returns the requested length', () => { expect(generatePassword(24)).toHaveLength(24) })
  it('two calls differ', () => { expect(generatePassword(24)).not.toBe(generatePassword(24)) })
  it('only uses the allowed charset', () => {
    expect(/^[A-Za-z0-9!@#$%^&*()\-_=+]+$/.test(generatePassword(64))).toBe(true)
  })
})
