import { renderHook } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { expect, test } from 'vitest'
import { ThemeProvider } from '../theme/ThemeProvider'
import { usePaletteItems, type PaletteItem } from './usePaletteItems'

function wrapper(qc: QueryClient, route: string) {
  return function W({ children }: { children: React.ReactNode }) {
    return (
      <QueryClientProvider client={qc}>
        <ThemeProvider>
          <MemoryRouter initialEntries={[route]}>{children}</MemoryRouter>
        </ThemeProvider>
      </QueryClientProvider>
    )
  }
}

function seed(): QueryClient {
  const qc = new QueryClient()
  qc.setQueryData(['projects'], [{ id: 'p1', slug: 'gw', name: 'api-gateway' }])
  qc.setQueryData(['envs', 'p1'], [{ id: 'e1', slug: 'dev', name: 'Development' }])
  qc.setQueryData(['configs', 'p1', 'e1'], [
    { id: 'c1', environment_id: 'e1', name: 'dev', inherits_from: null, created_at: '2026-07-01T00:00:00Z' },
  ])
  qc.setQueryData(['config', 'c1', 'masked'], {
    DATABASE_URL: { value_version: 1, created_at: '2026-07-01T00:00:00Z', origin: 'own' },
    STRIPE_KEY: { value_version: 1, created_at: '2026-07-01T00:00:00Z', origin: 'own' },
  })
  return qc
}

test('always includes projects and nav actions', () => {
  const qc = seed()
  const { result } = renderHook(() => usePaletteItems(), { wrapper: wrapper(qc, '/') })
  const items = result.current
  expect(items.some((i: PaletteItem) => i.group === 'Projects' && i.label === 'api-gateway')).toBe(true)
  expect(items.some((i: PaletteItem) => i.group === 'Actions' && /activity/i.test(i.label))).toBe(true)
})

test('includes active-project configs and secret KEY NAMES (never values)', () => {
  const qc = seed()
  const { result } = renderHook(() => usePaletteItems(), { wrapper: wrapper(qc, '/projects/p1/configs/c1') })
  const items = result.current
  expect(items.some((i) => i.group === 'Configs' && i.label === 'dev')).toBe(true)
  const secret = items.find((i) => i.group === 'Secrets' && i.label === 'DATABASE_URL')
  expect(secret).toBeTruthy()
  expect(secret!.to).toBe('/projects/p1/configs/c1')
  // No item anywhere carries a secret value (masked metadata has none to leak).
  const serialized = JSON.stringify(items)
  expect(serialized).not.toContain('value_version')
})

test('omits configs/secrets when no active project (top-level route)', () => {
  const qc = seed()
  const { result } = renderHook(() => usePaletteItems(), { wrapper: wrapper(qc, '/') })
  expect(result.current.some((i) => i.group === 'Configs')).toBe(false)
  expect(result.current.some((i) => i.group === 'Secrets')).toBe(false)
})
