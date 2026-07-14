import { http, HttpResponse } from 'msw'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { DynamicPanel } from './DynamicPanel'

function topo() {
  server.use(http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'acme', name: 'Acme' }] })))
  server.use(http.get('/v1/projects/:pid/environments', () => HttpResponse.json({ environments: [{ id: 'e1', slug: 'prod', name: 'prod' }] })))
  server.use(http.get('/v1/projects/:pid/environments/:eid/configs', () => HttpResponse.json({ configs: [{ id: 'c1', environment_id: 'e1', name: 'prod', inherits_from: null, created_at: 'x' }] })))
}
const ROLE = { id: 'role1', project_id: 'p1', config_id: 'c1', name: 'readonly', default_ttl_seconds: 3600, max_ttl_seconds: 86400, created_at: 'x' }

test('lists roles and issues creds, showing the password once', async () => {
  topo()
  server.use(http.get('/v1/dynamic/roles', () => HttpResponse.json({ roles: [ROLE] })))
  server.use(http.post('/v1/dynamic/roles/role1/creds', () => HttpResponse.json({ lease_id: 'l1', username: 'janus_readonly_x', password: 'ONE-TIME-PW', expires_at: new Date().toISOString() })))
  server.use(http.get('/v1/dynamic/leases', () => HttpResponse.json({ leases: [] })))
  renderApp(<DynamicPanel filter="all" />, { route: '/operations', withAuth: false })
  expect(await screen.findByText('readonly')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /issue/i }))
  expect(await screen.findByText('ONE-TIME-PW')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /done/i }))
  expect(screen.queryByText('ONE-TIME-PW')).toBeNull()
})

test('bulk delete: select 2 roles, confirm gates the DELETEs, then fires exactly one per id', async () => {
  topo()
  const ROLE_A = { ...ROLE, id: 'roleA', name: 'alpha', config_id: 'c1' }
  const ROLE_B = { ...ROLE, id: 'roleB', name: 'bravo', config_id: 'c1' }
  server.use(http.get('/v1/dynamic/roles', () => HttpResponse.json({ roles: [ROLE_A, ROLE_B] })))
  const deleted: string[] = []
  server.use(http.delete('/v1/dynamic/roles/:id', ({ params }) => { deleted.push(params.id as string); return HttpResponse.json({}, { status: 204 }) }))
  renderApp(<DynamicPanel filter="all" />, { route: '/operations', withAuth: false })

  await userEvent.click(await screen.findByLabelText('select alpha'))
  await userEvent.click(screen.getByLabelText('select bravo'))
  await userEvent.click(screen.getByRole('button', { name: /delete role/i }))

  // ConfirmDialog gates: no DELETE until confirm.
  expect(await screen.findByRole('heading', { name: /delete 2 dynamic roles\?/i })).toBeInTheDocument()
  expect(deleted).toEqual([])

  const dialog = screen.getByRole('alertdialog')
  await userEvent.click(within(dialog).getByRole('button', { name: /^delete$/i }))
  await waitFor(() => expect(deleted.length).toBe(2))
  expect(new Set(deleted)).toEqual(new Set(['roleA', 'roleB']))
})

test('a sortable header click reorders the rows', async () => {
  topo()
  const ROLE_A = { ...ROLE, id: 'roleA', name: 'zeta', config_id: 'c1' }
  const ROLE_B = { ...ROLE, id: 'roleB', name: 'alpha', config_id: 'c1' }
  server.use(http.get('/v1/dynamic/roles', () => HttpResponse.json({ roles: [ROLE_A, ROLE_B] })))
  renderApp(<DynamicPanel filter="all" />, { route: '/operations', withAuth: false })

  // Default order = project then role name → alpha before zeta.
  await screen.findByText('alpha')
  let cells = screen.getAllByText(/alpha|zeta/).map((el) => el.textContent)
  expect(cells).toEqual(['alpha', 'zeta'])

  // Click Role header → asc keeps alpha,zeta; click again → desc flips to zeta,alpha.
  await userEvent.click(screen.getByRole('button', { name: /sort by role/i }))
  await userEvent.click(screen.getByRole('button', { name: /sort by role/i }))
  await waitFor(() => {
    cells = screen.getAllByText(/alpha|zeta/).map((el) => el.textContent)
    expect(cells).toEqual(['zeta', 'alpha'])
  })
})

test('view leases opens the sheet', async () => {
  topo()
  server.use(http.get('/v1/dynamic/roles', () => HttpResponse.json({ roles: [ROLE] })))
  server.use(http.get('/v1/dynamic/leases', () => HttpResponse.json({ leases: [] })))
  renderApp(<DynamicPanel filter="all" />, { route: '/operations', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /leases/i }))
  expect(await screen.findByText(/Leases · readonly/)).toBeInTheDocument()
})

// ── Create flow ──────────────────────────────────────────────────────────────

function mockEmpty() {
  server.use(http.get('/v1/dynamic/roles', () => HttpResponse.json({ roles: [] })))
}

test('shows a New role affordance and no CLI empty-state copy', async () => {
  topo(); mockEmpty()
  renderApp(<DynamicPanel filter="all" />, { route: '/operations', withAuth: false })
  expect(await screen.findByRole('button', { name: /new role/i })).toBeInTheDocument()
  expect(screen.queryByText(/janus dynamic roles create/i)).not.toBeInTheDocument()
})

test('the create Sheet shows config picker, name, ttls, password admin_dsn, creation textarea, and placeholder hint chips', async () => {
  topo(); mockEmpty()
  renderApp(<DynamicPanel filter="all" />, { route: '/operations', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /new role/i }))
  expect(await screen.findByRole('option', { name: 'Acme / prod / prod' })).toBeInTheDocument()

  expect(screen.getByLabelText(/^name$/i)).toBeInTheDocument()
  expect(screen.getByLabelText(/default ttl/i)).toBeInTheDocument()
  expect(screen.getByLabelText(/max ttl/i)).toBeInTheDocument()
  expect(screen.getByLabelText(/admin dsn/i)).toHaveAttribute('type', 'password')
  expect(screen.getByLabelText(/creation statements/i)).toBeInTheDocument()

  // Visual placeholder hint chips near the creation textarea.
  expect(screen.getByText('{{name}}')).toBeInTheDocument()
  expect(screen.getByText('{{password}}')).toBeInTheDocument()
  expect(screen.getByText('{{expiration}}')).toBeInTheDocument()
})

test('Create is disabled until required fields are filled, then POSTs nested config + ttls', async () => {
  topo(); mockEmpty()
  let body: any
  server.use(http.post('/v1/dynamic/roles', async ({ request }) => {
    body = await request.json()
    return HttpResponse.json({ ...ROLE, id: 'role2', name: 'writer' }, { status: 201 })
  }))
  renderApp(<DynamicPanel filter="all" />, { route: '/operations', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /new role/i }))
  await screen.findByRole('option', { name: 'Acme / prod / prod' })

  const create = screen.getByRole('button', { name: /^create$/i })
  expect(create).toBeDisabled()

  await userEvent.selectOptions(screen.getByLabelText(/^config$/i), 'c1')
  await userEvent.type(screen.getByLabelText(/^name$/i), 'writer')
  await userEvent.type(screen.getByLabelText(/admin dsn/i), 'postgres://admin@host/db')
  await userEvent.type(screen.getByLabelText(/creation statements/i), 'CREATE ROLE "{{name}}" LOGIN PASSWORD \'{{password}}\';')
  expect(create).toBeEnabled()

  await userEvent.click(create)
  await waitFor(() => expect(screen.queryByRole('heading', { name: /new dynamic role/i })).not.toBeInTheDocument())
  expect(body.config_id).toBe('c1')
  expect(body.name).toBe('writer')
  expect(body.default_ttl_seconds).toBe(3600)
  expect(body.max_ttl_seconds).toBe(86400)
  expect(body.config.admin_dsn).toBe('postgres://admin@host/db')
  expect(body.config.creation_statements).toContain('CREATE ROLE')
  expect(body.config.revocation_statements).toBeUndefined()
  expect(body.config.renew_statements).toBeUndefined()
})

test('successful create closes the Sheet and invalidates the list', async () => {
  topo()
  let listCalls = 0
  server.use(http.get('/v1/dynamic/roles', () => { listCalls++; return HttpResponse.json({ roles: [] }) }))
  server.use(http.post('/v1/dynamic/roles', () => HttpResponse.json({ ...ROLE, id: 'role2' }, { status: 201 })))
  renderApp(<DynamicPanel filter="all" />, { route: '/operations', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /new role/i }))
  await screen.findByRole('option', { name: 'Acme / prod / prod' })
  await userEvent.selectOptions(screen.getByLabelText(/^config$/i), 'c1')
  await userEvent.type(screen.getByLabelText(/^name$/i), 'writer')
  await userEvent.type(screen.getByLabelText(/admin dsn/i), 'postgres://admin@host/db')
  await userEvent.type(screen.getByLabelText(/creation statements/i), 'CREATE ROLE "{{name}}";')
  const before = listCalls
  await userEvent.click(screen.getByRole('button', { name: /^create$/i }))
  await waitFor(() => expect(screen.queryByRole('heading', { name: /new dynamic role/i })).not.toBeInTheDocument())
  await waitFor(() => expect(listCalls).toBeGreaterThan(before))
})

test('a create error keeps the Sheet open and shows the inline curated message', async () => {
  topo(); mockEmpty()
  server.use(http.post('/v1/dynamic/roles', () =>
    HttpResponse.json({ error: { code: 'validation', message: 'invalid dynamic role configuration' } }, { status: 400 })))
  renderApp(<DynamicPanel filter="all" />, { route: '/operations', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /new role/i }))
  await screen.findByRole('option', { name: 'Acme / prod / prod' })
  await userEvent.selectOptions(screen.getByLabelText(/^config$/i), 'c1')
  await userEvent.type(screen.getByLabelText(/^name$/i), 'writer')
  await userEvent.type(screen.getByLabelText(/admin dsn/i), 'postgres://admin@host/db')
  await userEvent.type(screen.getByLabelText(/creation statements/i), 'CREATE ROLE "{{name}}";')
  await userEvent.click(screen.getByRole('button', { name: /^create$/i }))
  // A 400 `validation` maps through errorMessage() to the curated
  // "Please check your input." (raw server text is NOT echoed).
  expect(await screen.findByText(/please check your input/i)).toBeInTheDocument()
  expect(screen.getByRole('heading', { name: /new dynamic role/i })).toBeInTheDocument()
})
