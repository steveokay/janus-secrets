import type { MaskedSecret } from '../lib/endpoints'

export type SortKey = 'key' | 'origin' | 'updated' | 'version'
export type SortState = { key: SortKey; dir: 'asc' | 'desc' } | null

// Reorder a key list by the given sort state. Pending-added keys (absent from
// `masked`) always pin to the top so unsaved work stays visible regardless of
// direction. Stable via a key-ascending tiebreak.
export function sortRows(
  rows: string[],
  masked: Record<string, MaskedSecret>,
  sort: SortState,
): string[] {
  if (!sort) return [...rows]
  const dir = sort.dir === 'asc' ? 1 : -1
  const byKey = (a: string, b: string) => a.toLowerCase().localeCompare(b.toLowerCase())
  return [...rows].sort((a, b) => {
    const am = masked[a]
    const bm = masked[b]
    // Added rows (no masked entry) always float to the top.
    if (!am && !bm) return byKey(a, b)
    if (!am) return -1
    if (!bm) return 1
    let cmp = 0
    switch (sort.key) {
      case 'key': cmp = byKey(a, b); break
      case 'origin': cmp = am.origin.localeCompare(bm.origin); break
      case 'updated': cmp = am.created_at.localeCompare(bm.created_at); break
      case 'version': cmp = am.value_version - bm.value_version; break
    }
    if (cmp === 0) return byKey(a, b) // deterministic tiebreak (always asc)
    return cmp * dir
  })
}
