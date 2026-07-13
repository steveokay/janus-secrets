import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ToastProvider } from '../ui/Toast'
import { FederationSection } from './FederationSection'

function mount() {
  return renderApp(
    <ToastProvider><FederationSection /></ToastProvider>,
    { route: '/settings?section=federation', withAuth: false },
  )
}

const CONFIG = { issuer: 'https://token.actions.githubusercontent.com', audience: 'urn:janus:acme', enabled: true }
const BINDING = {
  id: 'b1', name: 'ci-deploy', match_claims: { repository: 'org/repo' },
  scope_kind: 'config' as const, scope_id: 'cfg-uuid', access: 'read' as const,
  ttl_seconds: 900, enabled: true,
}

// By default the config GET 404s and bindings return []; individual tests override.
function stubOk() {
  server.use(
    http.get('/v1/sys/oidc/federation', () => HttpResponse.json(CONFIG)),
    http.get('/v1/sys/oidc/federation/bindings', () => HttpResponse.json([])),
  )
}

test('403 renders a requires-admin hint and no form', async () => {
  server.use(
    http.get('/v1/sys/oidc/federation', () =>
      HttpResponse.json({ error: { code: 'forbidden', message: 'nope' } }, { status: 403 })),
    http.get('/v1/sys/oidc/federation/bindings', () =>
      HttpResponse.json({ error: { code: 'forbidden', message: 'nope' } }, { status: 403 })),
  )
  mount()
  expect(await screen.findByText(/instance admin/i)).toBeInTheDocument()
  expect(screen.queryByLabelText(/issuer/i)).not.toBeInTheDocument()
})

test('404 renders the not-configured empty state with an add affordance', async () => {
  server.use(
    http.get('/v1/sys/oidc/federation', () =>
      HttpResponse.json({ error: { code: 'not_found', message: 'not configured' } }, { status: 404 })),
    http.get('/v1/sys/oidc/federation/bindings', () => HttpResponse.json([])),
  )
  mount()
  expect(await screen.findByText(/no trust provider configured/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /configure provider/i }))
  expect(await screen.findByLabelText(/issuer/i)).toBeInTheDocument()
})

test('200 shows the trust-provider view and can save it', async () => {
  let body: any
  server.use(
    http.get('/v1/sys/oidc/federation', () => HttpResponse.json(CONFIG)),
    http.get('/v1/sys/oidc/federation/bindings', () => HttpResponse.json([])),
    http.put('/v1/sys/oidc/federation', async ({ request }) => { body = await request.json(); return HttpResponse.json({ ok: true }) }),
  )
  mount()
  expect(await screen.findByDisplayValue('https://token.actions.githubusercontent.com')).toBeInTheDocument()
  expect(screen.getByDisplayValue('urn:janus:acme')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /^save$/i }))
  await waitFor(() => expect(body).toBeTruthy())
  expect(body.issuer).toBe('https://token.actions.githubusercontent.com')
  expect(body.audience).toBe('urn:janus:acme')
})

test('bindings list renders with a binding', async () => {
  server.use(
    http.get('/v1/sys/oidc/federation', () => HttpResponse.json(CONFIG)),
    http.get('/v1/sys/oidc/federation/bindings', () => HttpResponse.json([BINDING])),
  )
  mount()
  expect(await screen.findByText('ci-deploy')).toBeInTheDocument()
  expect(screen.getByText('org/repo')).toBeInTheDocument()
})

test('empty bindings show a no-bindings hint', async () => {
  stubOk()
  mount()
  expect(await screen.findByText(/no trust bindings/i)).toBeInTheDocument()
})

test('create-binding Sheet requires repository, then POSTs the expected shape', async () => {
  let body: any
  server.use(
    http.get('/v1/sys/oidc/federation', () => HttpResponse.json(CONFIG)),
    http.get('/v1/sys/oidc/federation/bindings', () => HttpResponse.json([])),
    http.post('/v1/sys/oidc/federation/bindings', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ ...BINDING, ...body }, { status: 201 })
    }),
  )
  mount()
  await userEvent.click(await screen.findByRole('button', { name: /new binding/i }))
  const create = await screen.findByRole('button', { name: /create binding/i })
  // blocked until name AND repository are filled
  expect(create).toBeDisabled()
  await userEvent.type(screen.getByLabelText(/^name$/i), 'ci-deploy')
  expect(create).toBeDisabled()
  await userEvent.type(screen.getByLabelText(/^repository$/i), 'org/repo')
  await userEvent.type(screen.getByLabelText(/scope id/i), 'cfg-uuid')
  expect(create).toBeEnabled()
  await userEvent.click(create)
  await waitFor(() => expect(body).toBeTruthy())
  expect(body.name).toBe('ci-deploy')
  expect(body.match_claims.repository).toBe('org/repo')
  expect(body.scope_kind).toBe('config')
  expect(body.scope_id).toBe('cfg-uuid')
  expect(body.access).toBe('read')
  expect(body.ttl_seconds).toBe(900)
  expect(body.enabled).toBe(true)
})

test('delete-binding confirms then calls DELETE', async () => {
  let deleted = false
  server.use(
    http.get('/v1/sys/oidc/federation', () => HttpResponse.json(CONFIG)),
    http.get('/v1/sys/oidc/federation/bindings', () => HttpResponse.json([BINDING])),
    http.delete('/v1/sys/oidc/federation/bindings/b1', () => { deleted = true; return new HttpResponse(null, { status: 204 }) }),
  )
  mount()
  await userEvent.click(await screen.findByRole('button', { name: /delete binding/i }))
  await userEvent.click(await screen.findByRole('button', { name: /^delete$/i }))
  await waitFor(() => expect(deleted).toBe(true))
})
