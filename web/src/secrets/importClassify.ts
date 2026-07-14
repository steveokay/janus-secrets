import type { MaskedSecret } from '../lib/endpoints'

export type ImportRow = { key: string; kind: 'add' | 'update' }

// Value-free classification of parsed .env pairs against the current masked
// list: 'update' if the key already exists (will edit/override), else 'add'.
export function classifyImport(
  pairs: Record<string, string>,
  masked: Record<string, MaskedSecret>,
): ImportRow[] {
  return Object.keys(pairs)
    .sort((a, b) => a.localeCompare(b))
    .map((key) => ({ key, kind: key in masked ? 'update' : 'add' }))
}
