import { http, HttpResponse } from 'msw'
import { screen, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ToastProvider } from '../ui/Toast'
import { TokenMeta } from '../lib/endpoints'
import { TokensPage } from './TokensPage'

function T(over: Partial<TokenMeta> = {}): TokenMeta {
  return {
    id: 't1',
    name: 'ci',
    scope_kind: 'config',
    scope_id: 'c1234567',
    access: 'read',
    created_by: 'u1',
    created_at: '2026-07-06T10:00:00Z',
    ...over,
  }
}

function mockTokens(tokens: TokenMeta[]) {
  server.use(http.get('/v1/tokens', () => HttpResponse.json({ tokens })))
}

function mockCascade() {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'proj', name: 'Proj' }] })),
    http.get('/v1/projects/p1/environments', () =>
      HttpResponse.json({ environments: [{ id: 'e1', slug: 'prod', name: 'Prod' }] })),
    http.get('/v1/projects/p1/environments/e1/configs', () =>
      HttpResponse.json({
        configs: [{ id: 'c1', environment_id: 'e1', name: 'primary', inherits_from: null, created_at: '2026-07-06T00:00:00Z' }],
      })),
  )
}

function mount() {
  return renderApp(
    <ToastProvider><TokensPage /></ToastProvider>,
    { route: '/tokens', withAuth: false },
  )
}

test('list renders rows: name, scope pill by kind, access, never expiry, revoked pill', async () => {
  mockTokens([
    T({ id: 't1', name: 'ci-config', scope_kind: 'config', scope_id: 'c1234567', access: 'read' }),
    T({ id: 't2', name: 'ci-env', scope_kind: 'environment', scope_id: 'e1', access: 'readwrite', revoked_at: '2026-07-06T12:00:00Z' }),
    T({ id: 't3', name: 'ci-transit', scope_kind: 'transit', scope_id: '', access: 'use' }),
  ])
  mount()

  expect(await screen.findByText('ci-config')).toBeInTheDocument()
  expect(screen.getByText('ci-env')).toBeInTheDocument()
  expect(screen.getByText('ci-transit')).toBeInTheDocument()

  expect(screen.getByText('config')).toBeInTheDocument()
  expect(screen.getByText('environment')).toBeInTheDocument()
  expect(screen.getByText('transit')).toBeInTheDocument()

  expect(screen.getAllByText('never').length).toBeGreaterThan(0)
  expect(screen.getByText('revoked')).toBeInTheDocument()
  expect(screen.getByText('all keys')).toBeInTheDocument()
})

test('mint flow: kind switch flips access options, cascading picker, POST body, RevealOnce, list invalidation', async () => {
  let tokensHits = 0
  server.use(http.get('/v1/tokens', () => {
    tokensHits++
    return HttpResponse.json({ tokens: [] })
  }))
  mockCascade()
  let body: unknown
  server.use(http.post('/v1/tokens', async ({ request }) => {
    body = await request.json()
    return HttpResponse.json({
      token: 'janus_svc_minted123',
      id: 't9',
      name: 'ci-token',
      scope: { kind: 'config', id: 'c1' },
      access: 'readwrite',
      expires_at: null,
    })
  }))

  mount()
  await screen.findByText('No service tokens yet')
  expect(tokensHits).toBe(1)

  await userEvent.click(screen.getAllByRole('button', { name: 'Mint token' })[0])

  const access = screen.getByLabelText('access') as HTMLSelectElement
  expect(within(access).getAllByRole('option').map((o) => o.textContent)).toEqual(['read', 'readwrite'])

  await userEvent.selectOptions(screen.getByLabelText('kind'), 'transit')
  expect(within(access).getAllByRole('option').map((o) => o.textContent)).toEqual(['use', 'manage'])

  await userEvent.selectOptions(screen.getByLabelText('kind'), 'config')
  expect(within(access).getAllByRole('option').map((o) => o.textContent)).toEqual(['read', 'readwrite'])

  await userEvent.type(screen.getByLabelText('name'), 'ci-token')

  await screen.findByRole('option', { name: 'Proj' })
  await userEvent.selectOptions(screen.getByLabelText('project'), 'p1')

  await screen.findByRole('option', { name: 'Prod' })
  await userEvent.selectOptions(screen.getByLabelText('environment'), 'e1')

  await screen.findByRole('option', { name: 'primary' })
  await userEvent.selectOptions(screen.getByLabelText('config'), 'c1')

  await userEvent.selectOptions(screen.getByLabelText('access'), 'readwrite')
  await userEvent.type(screen.getByLabelText('ttl seconds'), '3600')

  await userEvent.click(screen.getByRole('button', { name: 'Mint' }))

  await waitFor(() => expect(body).toEqual({
    name: 'ci-token',
    scope: { kind: 'config', id: 'c1' },
    access: 'readwrite',
    ttl_seconds: 3600,
  }))

  expect(await screen.findByText('janus_svc_minted123')).toBeInTheDocument()
  await waitFor(() => expect(tokensHits).toBe(2))
})

test('revoke: confirm dialog then DELETE and success toast', async () => {
  mockTokens([T({ id: 't1', name: 'ci-config' })])
  let deleted = false
  server.use(http.delete('/v1/tokens/t1', () => {
    deleted = true
    return new HttpResponse(null, { status: 204 })
  }))
  mount()

  await screen.findByText('ci-config')
  await userEvent.click(screen.getByRole('button', { name: 'Revoke' }))

  const dialog = await screen.findByRole('alertdialog')
  expect(within(dialog).getByText(/ci-config/)).toBeInTheDocument()
  await userEvent.click(within(dialog).getByRole('button', { name: 'Revoke' }))

  await waitFor(() => expect(deleted).toBe(true))
  expect(await screen.findByText('Token revoked')).toBeInTheDocument()
})

test('revoke failure fires a danger toast with the curated message', async () => {
  mockTokens([T({ id: 't1', name: 'ci-config' })])
  server.use(http.delete('/v1/tokens/t1', () =>
    HttpResponse.json({ error: { code: 'conflict', message: 'token already revoked' } }, { status: 409 })))
  mount()

  await screen.findByText('ci-config')
  await userEvent.click(screen.getByRole('button', { name: 'Revoke' }))
  const dialog = await screen.findByRole('alertdialog')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Revoke' }))

  // apiErrorTitle passes the server's curated 409 message through to the toast.
  expect(await screen.findByText('token already revoked')).toBeInTheDocument()
})

test('403 on list shows the "Token access required" empty state', async () => {
  server.use(http.get('/v1/tokens', () =>
    HttpResponse.json({ error: { code: 'forbidden', message: 'x' } }, { status: 403 })))
  mount()
  expect(await screen.findByText('Token access required')).toBeInTheDocument()
})

test('zero tokens shows an empty state with a mint CTA that opens the sheet', async () => {
  mockTokens([])
  server.use(http.get('/v1/projects', () => HttpResponse.json({ projects: [] })))
  mount()

  expect(await screen.findByText('No service tokens yet')).toBeInTheDocument()
  const buttons = screen.getAllByRole('button', { name: 'Mint token' })
  await userEvent.click(buttons[buttons.length - 1])

  expect(await screen.findByLabelText('kind')).toBeInTheDocument()
})
