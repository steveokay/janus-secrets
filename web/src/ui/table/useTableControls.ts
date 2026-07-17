import { useState } from 'react'

export type SortDir = 'asc' | 'desc'

export interface TableControlsConfig<T> {
  /** A row matches when ANY returned string contains the (trimmed, lowercased) query. */
  searchFields: (row: T) => string[]
  /** Ascending comparators keyed by sort key; the hook negates for descending. */
  comparators: Record<string, (a: T, b: T) => number>
  /** When absent, the view preserves input order until a header is clicked. */
  initialSort?: { key: string; dir: SortDir }
}

export interface TableControls<T> {
  query: string
  setQuery: (q: string) => void
  sortKey: string | null
  sortDir: SortDir
  /** Same key: asc -> desc -> off. Different key: switch to it at asc. */
  toggleSort: (key: string) => void
  view: T[]
  total: number
  matched: number
}

export function useTableControls<T>(
  rows: T[],
  config: TableControlsConfig<T>,
): TableControls<T> {
  const [query, setQuery] = useState('')
  const [sortKey, setSortKey] = useState<string | null>(config.initialSort?.key ?? null)
  const [sortDir, setSortDir] = useState<SortDir>(config.initialSort?.dir ?? 'asc')

  function toggleSort(key: string) {
    if (sortKey !== key) {
      setSortKey(key)
      setSortDir('asc')
      return
    }
    if (sortDir === 'asc') {
      setSortDir('desc')
      return
    }
    setSortKey(null) // was desc -> off (restore input order)
    setSortDir('asc')
  }

  // Lists here are small and fully loaded; deriving each render is cheaper than
  // the memo bookkeeping and sidesteps unstable-config-identity deps.
  const q = query.trim().toLowerCase()
  const filtered =
    q === ''
      ? rows
      : rows.filter((row) => config.searchFields(row).some((f) => f.toLowerCase().includes(q)))

  let view = filtered
  const cmp = sortKey !== null ? config.comparators[sortKey] : undefined
  if (cmp) {
    view = [...filtered].sort((a, b) => (sortDir === 'asc' ? cmp(a, b) : -cmp(a, b)))
  }

  return {
    query,
    setQuery,
    sortKey,
    sortDir,
    toggleSort,
    view,
    total: rows.length,
    matched: view.length,
  }
}
