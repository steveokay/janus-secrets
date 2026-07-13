import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

// N7: the legacy aliases text-muted/text-faint/shadow-card are retired in favor
// of the Nocturne tokens text-ink-mute/text-ink-faint/shadow-elev-1. Guard so
// they don't creep back.
const SELF = fileURLToPath(import.meta.url)
const SRC = join(dirname(SELF), '..')
function walk(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name)
    return statSync(p).isDirectory() ? walk(p) : [p]
  })
}
test('no legacy color aliases in web/src (use Nocturne ink/elev tokens)', () => {
  const files = walk(SRC).filter((f) => /\.(ts|tsx)$/.test(f) && f !== SELF)
  const offenders: string[] = []
  for (const f of files) {
    readFileSync(f, 'utf8').split('\n').forEach((line, i) => {
      if (/\b(?:text-muted|text-faint|shadow-card)\b/.test(line)) offenders.push(`${f}:${i + 1}`)
    })
  }
  expect(offenders).toEqual([])
})
