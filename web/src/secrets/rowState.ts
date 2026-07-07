import type { MaskedSecret } from '../lib/endpoints'
import type { Buffer } from './dirty'

export type Change = 'added' | 'edited' | 'removed' | null
export interface RowState { change: Change; origin: MaskedSecret['origin']; existing: boolean }

export function rowState(
  key: string,
  masked: Record<string, MaskedSecret>,
  buffer: Buffer,
  original: Record<string, string>,
): RowState {
  const existing = key in masked
  const serverOrigin = masked[key]?.origin ?? 'own'
  const entry = buffer[key]
  if (!entry) return { change: null, origin: serverOrigin, existing }
  if (entry.value === null) {
    return { change: existing ? 'removed' : null, origin: serverOrigin, existing }
  }
  const had = key in original
  const changed = !had || original[key] !== entry.value
  if (!changed) return { change: null, origin: serverOrigin, existing }
  if (existing) {
    const origin = serverOrigin === 'inherited' ? 'overridden' : serverOrigin
    return { change: 'edited', origin, existing }
  }
  return { change: 'added', origin: 'own', existing }
}

const KEY_RE = /^[A-Za-z_][A-Za-z0-9_]*$/
function unquote(v: string): string {
  const t = v.trim()
  const last = t[t.length - 1]
  if (t.length >= 2 && ((t[0] === '"' && last === '"') || (t[0] === "'" && last === "'"))) {
    return t.slice(1, -1)
  }
  return t
}

export function parseDotenv(text: string): { pairs: Record<string, string>; skipped: number } {
  const pairs: Record<string, string> = {}
  let skipped = 0
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim()
    if (line === '' || line.startsWith('#')) continue
    const eq = line.indexOf('=')
    if (eq <= 0) { skipped++; continue }
    const key = line.slice(0, eq).trim()
    if (!KEY_RE.test(key)) { skipped++; continue }
    pairs[key] = unquote(line.slice(eq + 1))
  }
  return { pairs, skipped }
}
