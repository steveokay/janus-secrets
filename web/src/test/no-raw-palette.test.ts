import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

// Enforces spec hard rule #1 (docs/superpowers/specs/2026-07-07-dark-redesign-design.md):
// (a) no raw Tailwind palette classes — components use theme tokens;
// (b) no hex color literals in src — token hexes live ONLY in src/theme.css
//     (the CSS-variable token source), static assets in public/ are not scanned.
// Scans every .ts/.tsx/.css under web/src including tests; the only exclusions
// are this file itself (its regexes would self-match) and theme.css (the sole
// legitimate home for token hex values).
const SELF = fileURLToPath(import.meta.url)
const SRC = join(dirname(SELF), '..')
const THEME_CSS = join(SRC, 'theme.css') // the sole legitimate home for token hex values

const RAW_PALETTE =
  /\b(?:bg|text|border|ring|ring-offset|fill|stroke|divide|from|via|to|placeholder|accent|caret|decoration|outline|shadow)-(?:gray|slate|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)-\d{2,3}(?:\/\d+)?\b/
const HEX_LITERAL = /#[0-9a-fA-F]{3,8}\b/

function walk(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name)
    return statSync(p).isDirectory() ? walk(p) : [p]
  })
}

test('no raw Tailwind palette classes or hex literals in web/src (use theme tokens)', () => {
  const files = walk(SRC).filter((f) => /\.(ts|tsx|css)$/.test(f) && f !== SELF)
  const offenders: string[] = []
  for (const f of files) {
    readFileSync(f, 'utf8').split('\n').forEach((line, i) => {
      const raw = line.match(RAW_PALETTE)
      if (raw) offenders.push(`${f}:${i + 1} ${raw[0]}`)
      const hex = line.match(HEX_LITERAL)
      if (hex && f !== THEME_CSS) offenders.push(`${f}:${i + 1} hex literal ${hex[0]}`)
    })
  }
  expect(offenders).toEqual([])
})
