import { renderHook, act } from '@testing-library/react'
import { useTableControls } from './useTableControls'

interface Row { name: string; kind: string; rank: number }

const rows: Row[] = [
  { name: 'ci', kind: 'config', rank: 2 },
  { name: 'Deploy', kind: 'environment', rank: 0 },
  { name: 'backup', kind: 'config', rank: 1 },
]

const config = {
  searchFields: (r: Row) => [r.name, r.kind],
  comparators: {
    name: (a: Row, b: Row) => a.name.localeCompare(b.name),
    rank: (a: Row, b: Row) => a.rank - b.rank,
  },
}

it('returns all rows in input order when the query is empty and no sort is active', () => {
  const { result } = renderHook(() => useTableControls(rows, config))
  expect(result.current.view.map((r) => r.name)).toEqual(['ci', 'Deploy', 'backup'])
  expect(result.current.total).toBe(3)
  expect(result.current.matched).toBe(3)
  expect(result.current.sortKey).toBeNull()
})

it('filters case-insensitively across all search fields and trims the query', () => {
  const { result } = renderHook(() => useTableControls(rows, config))
  act(() => result.current.setQuery('  CONFIG '))
  expect(result.current.view.map((r) => r.name)).toEqual(['ci', 'backup'])
  expect(result.current.matched).toBe(2)
  expect(result.current.total).toBe(3)
})

it('cycles a header asc -> desc -> off, restoring input order on off', () => {
  const { result } = renderHook(() => useTableControls(rows, config))
  act(() => result.current.toggleSort('name'))
  expect(result.current.sortDir).toBe('asc')
  expect(result.current.view.map((r) => r.name)).toEqual(['backup', 'ci', 'Deploy'])
  act(() => result.current.toggleSort('name'))
  expect(result.current.sortDir).toBe('desc')
  expect(result.current.view.map((r) => r.name)).toEqual(['Deploy', 'ci', 'backup'])
  act(() => result.current.toggleSort('name'))
  expect(result.current.sortKey).toBeNull()
  expect(result.current.view.map((r) => r.name)).toEqual(['ci', 'Deploy', 'backup'])
})

it('switches to a different key at ascending', () => {
  const { result } = renderHook(() => useTableControls(rows, config))
  act(() => result.current.toggleSort('name'))
  act(() => result.current.toggleSort('name'))
  act(() => result.current.toggleSort('rank'))
  expect(result.current.sortKey).toBe('rank')
  expect(result.current.sortDir).toBe('asc')
  expect(result.current.view.map((r) => r.rank)).toEqual([0, 1, 2])
})

it('honors initialSort and never mutates the input array', () => {
  const input = [...rows]
  const { result } = renderHook(() =>
    useTableControls(input, { ...config, initialSort: { key: 'rank', dir: 'desc' } }),
  )
  expect(result.current.view.map((r) => r.rank)).toEqual([2, 1, 0])
  expect(input.map((r) => r.name)).toEqual(['ci', 'Deploy', 'backup'])
})
