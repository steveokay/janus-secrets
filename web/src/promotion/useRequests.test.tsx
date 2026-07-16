import { http, HttpResponse } from 'msw'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClientProvider, QueryClient } from '@tanstack/react-query'
import { ReactNode } from 'react'
import { server } from '../test/msw'
import {
  usePendingRequests,
  useMyRequests,
  usePromotionRequest,
  useApproveRequest,
  useRejectRequest,
  useCancelRequest,
  usePendingApprovalCount,
} from './useRequests'

function wrapper(qc: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  )
}

function mockList() {
  server.use(
    http.get('/v1/promote/requests', ({ request }) => {
      const u = new URL(request.url)
      const mine = u.searchParams.get('mine') === 'true'
      const status = u.searchParams.get('status')
      const base = {
        id: 'req-1',
        project_id: 'proj-1',
        source_config_id: 'c-dev',
        source_version: 5,
        target_env_id: 'env-staging',
        target_name: 'default',
        create_target: false,
        keys: ['FEATURE_X'],
        selections: [{ key: 'FEATURE_X', action: 'set' }],
        note: 'please review',
        status: status ?? 'pending',
        requested_by: mine ? 'user-me' : 'user-other',
        created_at: '2026-07-16T00:00:00Z',
      }
      return HttpResponse.json({ requests: [base] })
    }),
  )
}

test('usePendingRequests fetches with project + status=pending query params', async () => {
  let capturedUrl = ''
  server.use(
    http.get('/v1/promote/requests', ({ request }) => {
      capturedUrl = request.url
      return HttpResponse.json({ requests: [] })
    }),
  )
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const { result } = renderHook(() => usePendingRequests('proj-1'), { wrapper: wrapper(qc) })
  await waitFor(() => expect(result.current.isSuccess).toBe(true))
  expect(capturedUrl).toContain('project=proj-1')
  expect(capturedUrl).toContain('status=pending')
})

test('useMyRequests fetches with mine=true', async () => {
  let capturedUrl = ''
  server.use(
    http.get('/v1/promote/requests', ({ request }) => {
      capturedUrl = request.url
      return HttpResponse.json({ requests: [] })
    }),
  )
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const { result } = renderHook(() => useMyRequests('proj-1'), { wrapper: wrapper(qc) })
  await waitFor(() => expect(result.current.isSuccess).toBe(true))
  expect(capturedUrl).toContain('mine=true')
})

test('usePendingRequests returns the mocked list shape', async () => {
  mockList()
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const { result } = renderHook(() => usePendingRequests('proj-1'), { wrapper: wrapper(qc) })
  await waitFor(() => expect(result.current.isSuccess).toBe(true))
  expect(result.current.data).toHaveLength(1)
  expect(result.current.data?.[0].id).toBe('req-1')
  expect(result.current.data?.[0].keys).toEqual(['FEATURE_X'])
})

test('usePromotionRequest fetches a single request including value-free diff', async () => {
  server.use(
    http.get('/v1/promote/requests/:id', () =>
      HttpResponse.json({
        id: 'req-1',
        project_id: 'proj-1',
        source_config_id: 'c-dev',
        source_version: 5,
        target_env_id: 'env-staging',
        target_config_id: 'c-stg',
        target_name: 'default',
        create_target: false,
        keys: ['FEATURE_X'],
        selections: [{ key: 'FEATURE_X', action: 'set' }],
        note: 'please review',
        status: 'pending',
        requested_by: 'user-other',
        created_at: '2026-07-16T00:00:00Z',
        diff: {
          source_version: 5,
          target_exists: true,
          entries: [{ key: 'FEATURE_X', status: 'add', locked: false }],
        },
      }),
    ),
  )
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const { result } = renderHook(() => usePromotionRequest('req-1'), { wrapper: wrapper(qc) })
  await waitFor(() => expect(result.current.isSuccess).toBe(true))
  expect(result.current.data?.diff?.entries[0].key).toBe('FEATURE_X')
  // value-free: no value fields on diff entries
  expect(result.current.data?.diff?.entries[0]).not.toHaveProperty('source_value')
  expect(result.current.data?.diff?.entries[0]).not.toHaveProperty('target_value')
})

test('useApproveRequest posts to approve and returns ApplyResult', async () => {
  server.use(
    http.post('/v1/promote/requests/:id/approve', () =>
      HttpResponse.json({ target_version: 6, applied: ['FEATURE_X'], skipped: [] }),
    ),
  )
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const { result } = renderHook(() => useApproveRequest(), { wrapper: wrapper(qc) })
  result.current.mutate('req-1')
  await waitFor(() => expect(result.current.isSuccess).toBe(true))
  expect(result.current.data).toEqual({ target_version: 6, applied: ['FEATURE_X'], skipped: [] })
})

test('useRejectRequest posts a note and returns rejected status', async () => {
  let body: any
  server.use(
    http.post('/v1/promote/requests/:id/reject', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ status: 'rejected' })
    }),
  )
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const { result } = renderHook(() => useRejectRequest(), { wrapper: wrapper(qc) })
  result.current.mutate({ id: 'req-1', note: 'not ready' })
  await waitFor(() => expect(result.current.isSuccess).toBe(true))
  expect(body.note).toBe('not ready')
  expect(result.current.data).toEqual({ status: 'rejected' })
})

test('useCancelRequest posts to cancel and returns cancelled status', async () => {
  server.use(
    http.post('/v1/promote/requests/:id/cancel', () => HttpResponse.json({ status: 'cancelled' })),
  )
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const { result } = renderHook(() => useCancelRequest(), { wrapper: wrapper(qc) })
  result.current.mutate('req-1')
  await waitFor(() => expect(result.current.isSuccess).toBe(true))
  expect(result.current.data).toEqual({ status: 'cancelled' })
})

test('usePendingApprovalCount sums pending requests across all projects, tolerating per-project 403s', async () => {
  server.use(
    http.get('/v1/projects', () =>
      HttpResponse.json({
        projects: [
          { id: 'proj-1', slug: 'alpha', name: 'Alpha' },
          { id: 'proj-2', slug: 'beta', name: 'Beta' },
        ],
      }),
    ),
    http.get('/v1/promote/requests', ({ request }) => {
      const project = new URL(request.url).searchParams.get('project')
      if (project === 'proj-1') {
        return HttpResponse.json({
          requests: [
            { id: 'r1', status: 'pending' },
            { id: 'r2', status: 'pending' },
          ],
        })
      }
      // proj-2: forbidden — should be tolerated (excluded, not an error)
      return HttpResponse.json({ error: { code: 'forbidden', message: 'no access' } }, { status: 403 })
    }),
  )
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const { result } = renderHook(() => usePendingApprovalCount(), { wrapper: wrapper(qc) })
  await waitFor(() => expect(result.current.isLoading).toBe(false))
  expect(result.current.count).toBe(2)
})
