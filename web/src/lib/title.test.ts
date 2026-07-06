import { renderHook } from '@testing-library/react'
import { useTitle } from './title'

test('sets "<section> · Janus" while mounted and restores on unmount', () => {
  const { unmount, rerender } = renderHook(({ s }: { s?: string }) => useTitle(s), {
    initialProps: { s: 'Audit viewer' },
  })
  expect(document.title).toBe('Audit viewer · Janus')
  rerender({ s: 'Tokens' })
  expect(document.title).toBe('Tokens · Janus')
  unmount()
  expect(document.title).toBe('Janus')
})

test('no section → bare product name', () => {
  renderHook(() => useTitle())
  expect(document.title).toBe('Janus')
})
