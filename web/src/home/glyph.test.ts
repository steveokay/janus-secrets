import { glyphClass } from './glyph'

const VALID = ['bg-glyph-a', 'bg-glyph-b', 'bg-glyph-c', 'bg-glyph-d']

test('same slug always yields the same class', () => {
  expect(glyphClass('api-gateway')).toBe(glyphClass('api-gateway'))
  expect(glyphClass('web')).toBe(glyphClass('web'))
})

test('returned class is one of the four glyph classes', () => {
  for (const slug of ['api-gateway', 'web', 'billing', 'infra']) {
    expect(VALID).toContain(glyphClass(slug))
  }
})

test('representative slugs spread across more than one class', () => {
  const spread = new Set(
    ['api', 'web', 'billing', 'infra', 'ops', 'auth', 'data', 'edge'].map(glyphClass),
  )
  expect(spread.size).toBeGreaterThan(1)
})

test('empty slug returns a valid class', () => {
  expect(VALID).toContain(glyphClass(''))
})
