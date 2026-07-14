import { act, renderHook } from '@testing-library/react'
import { expect, test } from 'vitest'
import { useRowSelection } from './useRowSelection'

test('toggle adds/removes; count and isSelected track', () => {
  const { result } = renderHook(() => useRowSelection())
  act(() => result.current.toggle('A'))
  expect(result.current.isSelected('A')).toBe(true)
  expect(result.current.count).toBe(1)
  act(() => result.current.toggle('A'))
  expect(result.current.isSelected('A')).toBe(false)
  expect(result.current.count).toBe(0)
})

test('setAll selects all when partial, clears when already all', () => {
  const { result } = renderHook(() => useRowSelection())
  act(() => result.current.setAll(['A', 'B']))
  expect(result.current.count).toBe(2)
  act(() => result.current.setAll(['A', 'B'])) // all present -> clear
  expect(result.current.count).toBe(0)
})

test('prune keeps only allowed keys', () => {
  const { result } = renderHook(() => useRowSelection())
  act(() => result.current.setAll(['A', 'B', 'C']))
  act(() => result.current.prune(['A', 'C']))
  expect(result.current.count).toBe(2)
  expect(result.current.isSelected('B')).toBe(false)
})

test('prune returns same ref when nothing changes', () => {
  const { result } = renderHook(() => useRowSelection())
  act(() => result.current.setAll(['A', 'B']))
  const before = result.current.selected
  act(() => result.current.prune(['A', 'B']))
  expect(result.current.selected).toBe(before) // same ref, no needless render
})
