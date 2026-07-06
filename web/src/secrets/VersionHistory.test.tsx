import { http, HttpResponse } from 'msw'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ToastProvider } from '../ui/Toast'
import { VersionHistory } from './VersionHistory'

const VERSIONS = {
  versions: [
    { version: 3, message: 'rotate stripe key', created_by: 'steve@acme.dev', created_at: '2026-07-06T10:00:00Z' },
    { version: 2, message: '', created_by: 'steve@acme.dev', created_at: '2026-07-05T10:00:00Z' },
    { version: 1, message: 'initial import', created_by: 'steve@acme.dev', created_at: '2026-07-04T10:00:00Z' },
  ],
}

function mount(dirty = false) {
  return renderApp(
    <ToastProvider><VersionHistory cid="c1" dirty={dirty} /></ToastProvider>,
    { withAuth: false },
  )
}

test('lists versions newest-first with current marker; v1 shows Initial version', async () => {
  server.use(http.get('/v1/configs/c1/versions', () => HttpResponse.json(VERSIONS)))
  mount()
  expect(await screen.findByText('v3')).toBeInTheDocument()
  expect(screen.getByText('current')).toBeInTheDocument()
  expect(screen.getByText('no message')).toBeInTheDocument()
  await userEvent.click(screen.getByText('initial import'))
  expect(await screen.findByText('Initial version')).toBeInTheDocument()
})

test('expanding v3 loads diff vs v2 and renders key chips', async () => {
  server.use(
    http.get('/v1/configs/c1/versions', () => HttpResponse.json(VERSIONS)),
    http.get('/v1/configs/c1/versions/diff', ({ request }) => {
      const u = new URL(request.url)
      expect(u.searchParams.get('a')).toBe('2')
      expect(u.searchParams.get('b')).toBe('3')
      return HttpResponse.json({ a: 2, b: 3, added: ['SENTRY_DSN'], changed: ['STRIPE_KEY'], removed: ['LEGACY'] })
    }),
  )
  mount()
  await userEvent.click(await screen.findByText('rotate stripe key'))
  expect(await screen.findByText('SENTRY_DSN')).toBeInTheDocument()
  expect(screen.getByText('STRIPE_KEY')).toBeInTheDocument()
  expect(screen.getByText('LEGACY')).toBeInTheDocument()
})

test('rollback: confirm dialog → POST body → success toast', async () => {
  let body: unknown
  server.use(
    http.get('/v1/configs/c1/versions', () => HttpResponse.json(VERSIONS)),
    http.post('/v1/configs/c1/rollback', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ version: 4, id: 'cv4', created_at: '2026-07-06T12:00:00Z' })
    }),
  )
  mount()
  const rollbacks = await screen.findAllByRole('button', { name: 'Roll back' })
  await userEvent.click(rollbacks[0]) // v2's button (v3 is current)
  await userEvent.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Roll back' }))
  expect(await screen.findByText('Rolled back to v2 — saved as v4')).toBeInTheDocument()
  expect(body).toEqual({ target_version: 2, message: 'Rollback to v2' })
})

test('dirty editor disables rollback with hint', async () => {
  server.use(http.get('/v1/configs/c1/versions', () => HttpResponse.json(VERSIONS)))
  mount(true)
  const btns = await screen.findAllByRole('button', { name: 'Roll back' })
  expect(btns[0]).toBeDisabled()
  expect(btns[0]).toHaveAttribute('title', 'Save or discard your changes first')
})

test('rollback failure shows danger toast', async () => {
  server.use(
    http.get('/v1/configs/c1/versions', () => HttpResponse.json(VERSIONS)),
    http.post('/v1/configs/c1/rollback', () =>
      HttpResponse.json({ error: { code: 'conflict', message: 'stale' } }, { status: 409 }),
    ),
  )
  mount()
  const rollbacks = await screen.findAllByRole('button', { name: 'Roll back' })
  await userEvent.click(rollbacks[0])
  await userEvent.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Roll back' }))
  expect(await screen.findByText('Rollback failed.')).toBeInTheDocument()
})

test('dirty state renders a visible rollback hint', async () => {
  server.use(http.get('/v1/configs/c1/versions', () => HttpResponse.json(VERSIONS)))
  mount(true)
  expect(await screen.findByText('Save or discard your changes to enable rollback.')).toBeInTheDocument()
})
