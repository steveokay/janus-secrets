import { expect, test, vi, afterEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClientProvider, QueryClient } from '@tanstack/react-query'
import { ReactNode } from 'react'
import { endpoints, type MemberScope } from '../lib/endpoints'
import { ApiError } from '../lib/api'
import { useRbacMatrix } from './useRbacMatrix'

function wrapper(qc: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  )
}

afterEach(() => {
  vi.restoreAllMocks()
})

function mockHappyPath() {
  vi.spyOn(endpoints, 'listProjects').mockResolvedValue([
    { id: 'p1', slug: 'app', name: 'App' },
  ] as any)
  vi.spyOn(endpoints, 'listEnvironments').mockImplementation((pid: string) => {
    if (pid === 'p1') return Promise.resolve([{ id: 'e1', slug: 'dev', name: 'Dev' }] as any)
    return Promise.resolve([])
  })
  vi.spyOn(endpoints, 'listMembers').mockImplementation((s: MemberScope) => {
    if (s.kind === 'instance') return Promise.resolve([{ user_id: 'u1', role: 'owner' }] as any)
    if (s.kind === 'project') return Promise.resolve([{ user_id: 'u1', role: 'admin' }] as any)
    return Promise.resolve([{ user_id: 'u1', role: 'developer' }] as any)
  })
  vi.spyOn(endpoints, 'listUsers').mockResolvedValue([
    { id: 'u1', email: 'a@x.io', disabled: false },
  ] as any)
}

test('fans out listMembers across instance + projects + envs and assembles the matrix', async () => {
  mockHappyPath()
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const { result } = renderHook(() => useRbacMatrix(), { wrapper: wrapper(qc) })

  await waitFor(() => expect(result.current.isLoading).toBe(false))

  expect(result.current.model.instanceRole.get('u1')).toBe('owner')
  expect(result.current.model.projectCells.get('u1')!.get('p1')).toEqual({ role: 'admin', envCount: 1 })
  expect(result.current.forbidden).toBe(false)
})

test('sets forbidden without throwing when the instance member list is a 403', async () => {
  vi.spyOn(endpoints, 'listProjects').mockResolvedValue([] as any)
  vi.spyOn(endpoints, 'listEnvironments').mockResolvedValue([] as any)
  vi.spyOn(endpoints, 'listMembers').mockImplementation((s: MemberScope) => {
    if (s.kind === 'instance') return Promise.reject(new ApiError(403, 'forbidden', 'no access'))
    return Promise.resolve([])
  })
  vi.spyOn(endpoints, 'listUsers').mockResolvedValue([] as any)

  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const { result } = renderHook(() => useRbacMatrix(), { wrapper: wrapper(qc) })

  await waitFor(() => expect(result.current.isLoading).toBe(false))

  expect(result.current.forbidden).toBe(true)
})
