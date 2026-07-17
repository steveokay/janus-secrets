import { expect, test, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClientProvider, QueryClient } from '@tanstack/react-query'
import { endpoints, type MemberScope } from '../lib/endpoints'
import { ApiError } from '../lib/api'
import { RbacMatrix } from './RbacMatrix'

afterEach(() => {
  vi.restoreAllMocks()
})

function renderMatrix(onPickScope: (scope: MemberScope) => void) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <RbacMatrix onPickScope={onPickScope} />
    </QueryClientProvider>,
  )
}

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

test('renders header cells, role + env-count badge, and fires onPickScope on cell click', async () => {
  mockHappyPath()
  const onPickScope = vi.fn()
  const { container } = renderMatrix(onPickScope)

  expect(await screen.findByText('Instance')).toBeInTheDocument()
  expect(await screen.findByText('App')).toBeInTheDocument()
  expect(await screen.findByText('admin')).toBeInTheDocument()
  expect(await screen.findByText('+1 env')).toBeInTheDocument()

  expect(container.querySelector('.overflow-x-auto')).toBeInTheDocument()

  const cellButton = screen.getByRole('button', { name: /App role for a@x.io/i })
  fireEvent.click(cellButton)
  expect(onPickScope).toHaveBeenCalledWith({ kind: 'project', pid: 'p1' })
})

test('shows "Member access required" when the instance members query 403s', async () => {
  vi.spyOn(endpoints, 'listProjects').mockResolvedValue([] as any)
  vi.spyOn(endpoints, 'listEnvironments').mockResolvedValue([] as any)
  vi.spyOn(endpoints, 'listMembers').mockImplementation((s: MemberScope) => {
    if (s.kind === 'instance') return Promise.reject(new ApiError(403, 'forbidden', 'no access'))
    return Promise.resolve([])
  })
  vi.spyOn(endpoints, 'listUsers').mockResolvedValue([] as any)

  renderMatrix(vi.fn())

  await waitFor(() => expect(screen.getByText('Member access required')).toBeInTheDocument())
})
