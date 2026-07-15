import { http, HttpResponse } from 'msw'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ToastProvider } from '../ui/Toast'
import { TrashPage } from './TrashPage'

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function mockTrash(body: any) {
  server.use(http.get('/v1/trash', () => HttpResponse.json(body)))
}
const FULL = {
  projects: [{ id: 'p1', slug: 'web', name: 'Web', deleted_at: '2026-07-14T10:00:00Z' }],
  environments: [{ id: 'e1', slug: 'prod', name: 'Prod', project_id: 'p1', project_name: 'Web', deleted_at: '2026-07-14T10:00:00Z' }],
  configs: [{ id: 'c1', name: 'root', environment_id: 'e1', environment_name: 'Prod', project_id: 'p1', project_name: 'Web', deleted_at: '2026-07-14T10:00:00Z' }],
}
function mount() {
  return renderApp(<ToastProvider><TrashPage /></ToastProvider>, { route: '/trash', withAuth: false })
}

test('renders grouped deleted items with parent paths', async () => {
  mockTrash(FULL)
  mount()
  expect(await screen.findByText('Web')).toBeInTheDocument()
  expect(screen.getByText(/Prod \/ root/)).toBeInTheDocument()
})

test('empty trash shows the empty state', async () => {
  mockTrash({ projects: [], environments: [], configs: [] })
  mount()
  expect(await screen.findByText(/Trash is empty/i)).toBeInTheDocument()
})

test('403 renders empty state, not an error', async () => {
  server.use(http.get('/v1/trash', () => new HttpResponse(null, { status: 403 })))
  mount()
  expect(await screen.findByText(/Trash is empty/i)).toBeInTheDocument()
})

test('restore calls the restore endpoint and refetches', async () => {
  mockTrash(FULL)
  let restored = ''
  server.use(http.post('/v1/configs/:cid/restore', ({ params }) => {
    restored = String(params.cid)
    return HttpResponse.json({ id: 'c1', environment_id: 'e1', name: 'root', inherits_from: null, created_at: 'x' })
  }))
  mount()
  const row = (await screen.findByText(/Prod \/ root/)).closest('li')!
  await userEvent.click(within(row).getByRole('button', { name: /restore root/i }))
  expect(restored).toBe('c1')
})

test('destroy requires typing the exact name before it fires', async () => {
  mockTrash(FULL)
  let destroyed = ''
  server.use(http.delete('/v1/configs/:cid', ({ params, request }) => {
    if (new URL(request.url).search.includes('destroy=true')) destroyed = String(params.cid)
    return new HttpResponse(null, { status: 204 })
  }))
  mount()
  const row = (await screen.findByText(/Prod \/ root/)).closest('li')!
  await userEvent.click(within(row).getByRole('button', { name: /destroy root/i }))
  const confirm = screen.getByRole('button', { name: /permanently destroy/i })
  expect(confirm).toBeDisabled()
  await userEvent.type(screen.getByLabelText(/type the name to confirm/i), 'root')
  expect(confirm).toBeEnabled()
  await userEvent.click(confirm)
  expect(destroyed).toBe('c1')
})
