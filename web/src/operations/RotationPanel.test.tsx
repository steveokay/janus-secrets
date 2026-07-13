import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { RotationPanel } from './RotationPanel'

function topo() {
  server.use(http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'acme', name: 'Acme' }] })))
  server.use(http.get('/v1/projects/:pid/environments', () => HttpResponse.json({ environments: [{ id: 'e1', slug: 'prod', name: 'prod' }] })))
  server.use(http.get('/v1/projects/:pid/environments/:eid/configs', () => HttpResponse.json({ configs: [{ id: 'c1', environment_id: 'e1', name: 'prod', inherits_from: null, created_at: 'x' }] })))
}
const POLICY = { id: 'r1', project_id: 'p1', config_id: 'c1', secret_key: 'DB_PASSWORD', type: 'postgres', interval_seconds: 3600, status: 'active', failure_count: 0, last_error: null, next_rotation_at: new Date(Date.now() + 7200_000).toISOString(), last_rotated_at: null, created_at: 'x' }
function mockList(policies = [POLICY]) {
  server.use(http.get('/v1/rotation/policies', () => HttpResponse.json({ policies })))
}

test('lists a policy with its config + status', async () => {
  topo(); mockList()
  renderApp(<RotationPanel filter="all" />, { route: '/operations', withAuth: false })
  expect(await screen.findByText('DB_PASSWORD')).toBeInTheDocument()
  expect(screen.getByText('active')).toBeInTheDocument()
})

test('rotate-now posts to /rotate', async () => {
  topo(); mockList()
  let hit = false
  server.use(http.post('/v1/rotation/policies/r1/rotate', () => { hit = true; return HttpResponse.json({ rotated: true, config_version: 5 }) }))
  renderApp(<RotationPanel filter="all" />, { route: '/operations', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /rotate now/i }))
  expect(hit).toBe(true)
})

test('pause patches status=paused', async () => {
  topo(); mockList()
  let body: any
  server.use(http.patch('/v1/rotation/policies/r1', async ({ request }) => { body = await request.json(); return HttpResponse.json({ ...POLICY, status: 'paused' }) }))
  renderApp(<RotationPanel filter="all" />, { route: '/operations', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /pause/i }))
  expect(body).toEqual({ status: 'paused' })
})

test('all-403 renders access-required', async () => {
  topo()
  server.use(http.get('/v1/rotation/policies', () => HttpResponse.json({ error: { code: 'forbidden', message: 'no' } }, { status: 403 })))
  renderApp(<RotationPanel filter="all" />, { route: '/operations', withAuth: false })
  expect(await screen.findByText(/access required/i)).toBeInTheDocument()
})

test('a 500 on the list query renders the error state', async () => {
  topo()
  server.use(http.get('/v1/rotation/policies', () => HttpResponse.json({ error: { code: 'internal', message: 'boom' } }, { status: 500 })))
  renderApp(<RotationPanel filter="all" />, { route: '/operations', withAuth: false })
  expect(await screen.findByRole('alert')).toHaveTextContent(/couldn't load/i)
})

test('a 500 while enumerating configs surfaces the error state', async () => {
  // engine list succeeds, but the decorative config-name enumeration 500s →
  // the panel must not silently render as though all is well
  topo(); mockList()
  server.use(http.get('/v1/projects/:pid/environments/:eid/configs', () => HttpResponse.json({ error: { code: 'internal', message: 'boom' } }, { status: 500 })))
  renderApp(<RotationPanel filter="all" />, { route: '/operations', withAuth: false })
  expect(await screen.findByRole('alert')).toHaveTextContent(/couldn't load/i)
})

// ── Create flow ──────────────────────────────────────────────────────────────

test('shows a New policy affordance and no CLI empty-state copy', async () => {
  topo(); mockList([])
  renderApp(<RotationPanel filter="all" />, { route: '/operations', withAuth: false })
  expect(await screen.findByRole('button', { name: /new policy/i })).toBeInTheDocument()
  expect(screen.queryByText(/janus rotation create/i)).not.toBeInTheDocument()
})

test('postgres shows a password admin_dsn; webhook shows url + password hmac_key', async () => {
  topo(); mockList([])
  renderApp(<RotationPanel filter="all" />, { route: '/operations', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /new policy/i }))
  // Config picker present in the Sheet
  expect(await screen.findByRole('option', { name: 'Acme / prod / prod' })).toBeInTheDocument()

  // postgres (default) → admin_dsn is a password field
  const dsn = screen.getByLabelText(/admin dsn/i)
  expect(dsn).toHaveAttribute('type', 'password')

  // switch to webhook → url + password hmac_key
  await userEvent.selectOptions(screen.getByLabelText(/^type$/i), 'webhook')
  expect(screen.getByLabelText(/^url$/i)).toBeInTheDocument()
  expect(screen.getByLabelText(/^hmac key$/i)).toHaveAttribute('type', 'password')
})

test('Create is disabled until required fields are filled, then POSTs nested config.admin_dsn', async () => {
  topo(); mockList([])
  let body: any
  server.use(http.post('/v1/rotation/policies', async ({ request }) => {
    body = await request.json()
    return HttpResponse.json({ ...POLICY, id: 'r2' }, { status: 201 })
  }))
  renderApp(<RotationPanel filter="all" />, { route: '/operations', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /new policy/i }))
  await screen.findByRole('option', { name: 'Acme / prod / prod' })

  const create = screen.getByRole('button', { name: /^create$/i })
  expect(create).toBeDisabled()

  await userEvent.selectOptions(screen.getByLabelText(/^config$/i), 'c1')
  await userEvent.type(screen.getByLabelText(/secret key/i), 'DB_PASSWORD')
  await userEvent.type(screen.getByLabelText(/admin dsn/i), 'postgres://u:p@h/db')
  expect(create).toBeEnabled()

  await userEvent.click(create)
  // Sheet closes on success → its title heading disappears
  await waitFor(() => expect(screen.queryByRole('heading', { name: /new rotation policy/i })).not.toBeInTheDocument())
  expect(body.config_id).toBe('c1')
  expect(body.secret_key).toBe('DB_PASSWORD')
  expect(body.type).toBe('postgres')
  expect(body.config.admin_dsn).toBe('postgres://u:p@h/db')
  expect(body.config.url).toBeUndefined()
})

test('successful create closes the Sheet and invalidates the list', async () => {
  topo()
  let listCalls = 0
  server.use(http.get('/v1/rotation/policies', () => { listCalls++; return HttpResponse.json({ policies: [] }) }))
  server.use(http.post('/v1/rotation/policies', () => HttpResponse.json({ ...POLICY, id: 'r2' }, { status: 201 })))
  renderApp(<RotationPanel filter="all" />, { route: '/operations', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /new policy/i }))
  await screen.findByRole('option', { name: 'Acme / prod / prod' })
  await userEvent.selectOptions(screen.getByLabelText(/^config$/i), 'c1')
  await userEvent.type(screen.getByLabelText(/secret key/i), 'DB_PASSWORD')
  await userEvent.type(screen.getByLabelText(/admin dsn/i), 'postgres://u:p@h/db')
  const before = listCalls
  await userEvent.click(screen.getByRole('button', { name: /^create$/i }))
  // Sheet closed → its title heading is gone
  await waitFor(() => expect(screen.queryByRole('heading', { name: /new rotation policy/i })).not.toBeInTheDocument())
  await waitFor(() => expect(listCalls).toBeGreaterThan(before))
})
