import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ToastProvider } from '../ui/Toast'
import { RequestReview } from './RequestReview'

function mockDetail(overrides: Partial<any> = {}) {
  server.use(
    http.get('/v1/promote/requests/req-1', () =>
      HttpResponse.json({
        id: 'req-1',
        project_id: 'proj-1',
        source_config_id: 'c-dev',
        source_version: 5,
        target_env_id: 'env-staging',
        target_config_id: 'c-stg',
        target_name: 'default',
        create_target: false,
        keys: ['FEATURE_X', 'OLD_KEY', 'LOG_LEVEL'],
        selections: [
          { key: 'FEATURE_X', action: 'set' },
          { key: 'OLD_KEY', action: 'remove' },
          { key: 'LOG_LEVEL', action: 'set' },
        ],
        note: 'please review before Friday',
        status: 'pending',
        requested_by: 'user-other',
        created_at: '2026-07-16T00:00:00Z',
        diff: {
          source_version: 5,
          target_exists: true,
          entries: [
            { key: 'FEATURE_X', status: 'add', locked: false },
            { key: 'OLD_KEY', status: 'remove', locked: false },
            { key: 'LOG_LEVEL', status: 'change', locked: false },
          ],
        },
        ...overrides,
      }),
    ),
  )
}

function renderReview(overrides: Partial<Parameters<typeof RequestReview>[0]> = {}) {
  return renderApp(
    <ToastProvider>
      <RequestReview requestId="req-1" open onOpenChange={() => {}} {...overrides} />
    </ToastProvider>,
    { route: '/', withAuth: false },
  )
}

test('shows the value-free diff (key names + status) and the requester note', async () => {
  mockDetail()
  renderReview()
  expect(await screen.findByText('FEATURE_X')).toBeInTheDocument()
  expect(screen.getByText('OLD_KEY')).toBeInTheDocument()
  expect(screen.getByText('LOG_LEVEL')).toBeInTheDocument()
  expect(screen.getByText('Add')).toBeInTheDocument()
  expect(screen.getByText('Remove')).toBeInTheDocument()
  expect(screen.getByText('Change')).toBeInTheDocument()
  expect(screen.getByText('please review before Friday')).toBeInTheDocument()
  // never a secret value
  expect(screen.queryByText(/postgres:\/\//)).not.toBeInTheDocument()
})

test('Approve shows the ApplyResult counts and skipped keys', async () => {
  mockDetail()
  server.use(
    http.post('/v1/promote/requests/req-1/approve', () =>
      HttpResponse.json({ target_version: 6, applied: ['FEATURE_X', 'LOG_LEVEL'], skipped: ['OLD_KEY'] }),
    ),
  )
  renderReview()
  await screen.findByText('FEATURE_X')
  await userEvent.click(screen.getByRole('button', { name: /^approve/i }))
  expect(await screen.findByText(/v6/i)).toBeInTheDocument()
  expect(screen.getByText(/applied 2/i)).toBeInTheDocument()
  expect(screen.getByText(/skipped:/i)).toBeInTheDocument()
})

test('Reject captures a note and submits it', async () => {
  mockDetail()
  let body: any
  server.use(
    http.post('/v1/promote/requests/req-1/reject', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ status: 'rejected' })
    }),
  )
  renderReview()
  await screen.findByText('FEATURE_X')
  await userEvent.click(screen.getByRole('button', { name: /^reject/i }))
  const noteBox = await screen.findByLabelText(/reason|note/i)
  await userEvent.type(noteBox, 'not ready yet')
  await userEvent.click(screen.getByRole('button', { name: /confirm reject|submit/i }))
  await waitFor(() => expect(body?.note).toBe('not ready yet'))
  expect(await screen.findByText(/rejected/i)).toBeInTheDocument()
})
