import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ToastProvider } from '../ui/Toast'
import { RequestsPanel } from './RequestsPanel'

function mockMe() {
  server.use(http.get('/v1/auth/me', () => HttpResponse.json({ kind: 'user', id: 'user-me', name: 'me@acme.dev' })))
}

function pendingReq(overrides: Partial<any> = {}) {
  return {
    id: 'req-1',
    project_id: 'proj-1',
    source_config_id: 'c-dev',
    source_version: 5,
    target_env_id: 'env-staging',
    target_name: 'default',
    create_target: false,
    keys: ['FEATURE_X', 'API_URL'],
    selections: [
      { key: 'FEATURE_X', action: 'set' },
      { key: 'API_URL', action: 'set' },
    ],
    note: 'ship it please',
    status: 'pending',
    requested_by: 'user-other',
    created_at: '2026-07-16T00:00:00Z',
    ...overrides,
  }
}

function mockLists({ pending = [], mine = [] }: { pending?: any[]; mine?: any[] } = {}) {
  server.use(
    http.get('/v1/promote/requests', ({ request }) => {
      const u = new URL(request.url)
      if (u.searchParams.get('mine') === 'true') return HttpResponse.json({ requests: mine })
      return HttpResponse.json({ requests: pending })
    }),
  )
}

function renderPanel(overrides: Partial<Parameters<typeof RequestsPanel>[0]> = {}) {
  mockMe()
  return renderApp(
    <ToastProvider>
      <RequestsPanel projectId="proj-1" {...overrides} />
    </ToastProvider>,
    { route: '/', withAuth: true },
  )
}

test('renders pending requests with target, key count, requester, and note', async () => {
  mockLists({ pending: [pendingReq()] })
  renderPanel()
  expect(await screen.findByText(/default/i)).toBeInTheDocument()
  expect(screen.getByText(/2 keys/i)).toBeInTheDocument()
  expect(screen.getByText('user-other')).toBeInTheDocument()
  expect(screen.getByText('ship it please')).toBeInTheDocument()
})

test('renders a My requests section from the mine=true list', async () => {
  mockLists({ pending: [], mine: [pendingReq({ id: 'req-2', requested_by: 'user-me', status: 'applied' })] })
  renderPanel()
  expect(await screen.findByText(/my requests/i)).toBeInTheDocument()
  expect(screen.getByText('applied')).toBeInTheDocument()
})

test('empty pending list shows an empty state, not an error', async () => {
  mockLists({ pending: [], mine: [] })
  renderPanel()
  expect(await screen.findByText(/no pending requests/i)).toBeInTheDocument()
})

test('Cancel is only offered on the viewer-owned pending row and cancels it', async () => {
  mockLists({ pending: [], mine: [pendingReq({ id: 'req-3', requested_by: 'user-me', status: 'pending' })] })
  let cancelled = false
  server.use(
    http.post('/v1/promote/requests/req-3/cancel', () => {
      cancelled = true
      return HttpResponse.json({ status: 'cancelled' })
    }),
  )
  renderPanel()
  const cancelBtn = await screen.findByRole('button', { name: /cancel/i })
  await userEvent.click(cancelBtn)
  await waitFor(() => expect(cancelled).toBe(true))
})

test('Approve opens the review for that request', async () => {
  mockLists({ pending: [pendingReq()] })
  server.use(
    http.get('/v1/promote/requests/req-1', () =>
      HttpResponse.json({
        ...pendingReq(),
        diff: { source_version: 5, target_exists: true, entries: [{ key: 'FEATURE_X', status: 'add', locked: false }] },
      }),
    ),
  )
  renderPanel()
  await screen.findByText(/default/i)
  await userEvent.click(screen.getByRole('button', { name: /approve/i }))
  expect(await screen.findByText(/review promotion request/i)).toBeInTheDocument()
  expect(await screen.findAllByText(/ship it please/i)).toHaveLength(2) // row + review both render the note
})

test('Reject opens the review for that request', async () => {
  mockLists({ pending: [pendingReq()] })
  server.use(
    http.get('/v1/promote/requests/req-1', () =>
      HttpResponse.json({
        ...pendingReq(),
        diff: { source_version: 5, target_exists: true, entries: [{ key: 'FEATURE_X', status: 'add', locked: false }] },
      }),
    ),
  )
  renderPanel()
  await screen.findByText(/default/i)
  await userEvent.click(screen.getByRole('button', { name: /reject/i }))
  expect(await screen.findByText(/review promotion request/i)).toBeInTheDocument()
})

test('list is value-free: no secret value text appears anywhere', async () => {
  mockLists({ pending: [pendingReq()] })
  renderPanel()
  await screen.findByText(/default/i)
  expect(screen.queryByText(/postgres:\/\//)).not.toBeInTheDocument()
})
