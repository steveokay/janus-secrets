import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { useCopyFeedback } from './useCopyFeedback'

beforeEach(() => { vi.useFakeTimers() })
afterEach(() => { vi.useRealTimers() })

test('markCopied flips isCopied for ~1.2s then auto-resets', () => {
  const { result } = renderHook(() => useCopyFeedback())
  expect(result.current.isCopied()).toBe(false)
  act(() => result.current.markCopied())
  expect(result.current.isCopied()).toBe(true)
  act(() => { vi.advanceTimersByTime(1199) })
  expect(result.current.isCopied()).toBe(true)
  act(() => { vi.advanceTimersByTime(1) })
  expect(result.current.isCopied()).toBe(false)
})

test('tracks distinct ids independently (multi-row tables)', () => {
  const { result } = renderHook(() => useCopyFeedback())
  act(() => result.current.markCopied('KEY_A'))
  expect(result.current.isCopied('KEY_A')).toBe(true)
  expect(result.current.isCopied('KEY_B')).toBe(false)
  act(() => result.current.markCopied('KEY_B'))
  // Only the most recent id flashes — matches per-row icon that reads one shared state.
  expect(result.current.isCopied('KEY_B')).toBe(true)
  expect(result.current.isCopied('KEY_A')).toBe(false)
})
