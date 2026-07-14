import { act, renderHook } from '@testing-library/react'
import { expect, test, vi } from 'vitest'
import { useRowNav } from './useRowNav'

function press(key: string, target?: EventTarget) {
  const e = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true })
  if (target) Object.defineProperty(e, 'target', { value: target })
  act(() => { window.dispatchEvent(e) })
  return e
}

function setup(over: Partial<Parameters<typeof useRowNav>[0]> = {}) {
  const cb = { onEdit: vi.fn(), onReveal: vi.fn(), onRemove: vi.fn(), onToggleSelect: vi.fn(), onFocusFilter: vi.fn(), ...over }
  const { result } = renderHook(() => useRowNav({ visible: ['A', 'B', 'C'], ...cb }))
  return { result, cb }
}

test('arrow/j-k move active within visible', () => {
  const { result } = setup()
  press('ArrowDown'); expect(result.current.active).toBe('A')
  press('j');         expect(result.current.active).toBe('B')
  press('ArrowUp');   expect(result.current.active).toBe('A')
  press('k');         expect(result.current.active).toBe('A') // clamped at top
})

test('action keys call callbacks for the active row', () => {
  const { result, cb } = setup()
  press('ArrowDown') // active = A
  press('e');      expect(cb.onEdit).toHaveBeenCalledWith('A')
  press('Enter');  expect(cb.onReveal).toHaveBeenCalledWith('A')
  press('x');      expect(cb.onToggleSelect).toHaveBeenCalledWith('A')
  press('Delete'); expect(cb.onRemove).toHaveBeenCalledWith('A')
  press('Escape'); expect(result.current.active).toBeNull()
})

test('/ focuses filter and prevents default', () => {
  const { cb } = setup()
  const e = press('/')
  expect(cb.onFocusFilter).toHaveBeenCalled()
  expect(e.defaultPrevented).toBe(true)
})

test('inert when a text input is focused', () => {
  const { result } = setup()
  const input = document.createElement('input')
  press('ArrowDown', input)
  expect(result.current.active).toBeNull()
})
