import { envTone } from './env'

test.each([
  ['dev', 'info'],
  ['development', 'info'],
  ['staging', 'warning'],
  ['stage', 'warning'],
  ['test', 'warning'],
  ['qa', 'warning'],
  ['prod', 'danger'],
  ['production', 'danger'],
  ['PROD', 'danger'],
  ['custom-env', 'info'],
])('envTone(%s) → %s', (slug, tone) => {
  expect(envTone(slug)).toBe(tone)
})
