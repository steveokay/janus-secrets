import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

// R4 dark-AA guard: `text-brand-deep` fails WCAG AA as a foreground on the dark
// theme's near-black surfaces. Foreground brand text must use `text-brand-text`,
// which resolves to the deep brand in light (unchanged) and a lifted brand in
// dark. The `--brand-deep` token itself still exists (for any future background
// use); only the `text-brand-deep` CLASS is banned here.
const SELF = fileURLToPath(import.meta.url)
const SRC = join(dirname(SELF), '..')

function walk(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name)
    return statSync(p).isDirectory() ? walk(p) : [p]
  })
}

test('no text-brand-deep foreground class in web/src (use text-brand-text for dark AA)', () => {
  const files = walk(SRC).filter((f) => /\.(ts|tsx)$/.test(f) && f !== SELF)
  const offenders: string[] = []
  for (const f of files) {
    readFileSync(f, 'utf8').split('\n').forEach((line, i) => {
      if (/\btext-brand-deep\b/.test(line)) offenders.push(`${f}:${i + 1}`)
    })
  }
  expect(offenders).toEqual([])
})
