import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { SyncPanel } from './SyncPanel'

function topo() {
  server.use(http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'acme', name: 'Acme' }] })))
  server.use(http.get('/v1/projects/:pid/environments', () => HttpResponse.json({ environments: [{ id: 'e1', slug: 'prod', name: 'prod' }] })))
  server.use(http.get('/v1/projects/:pid/environments/:eid/configs', () => HttpResponse.json({ configs: [{ id: 'c1', environment_id: 'e1', name: 'prod', inherits_from: null, created_at: 'x' }] })))
}
const GH = { id: 's1', project_id: 'p1', config_id: 'c1', provider: 'github', prune: true, interval_seconds: 3600, addr: { owner: 'acme', repo: 'widgets', environment: 'production' }, status: 'failed', failure_count: 3, last_error: 'apply failed', next_sync_at: new Date().toISOString(), last_synced_at: null, managed_keys: ['A'], created_at: 'x' }

test('renders provider + destination + failed status with last-error marker', async () => {
  topo()
  server.use(http.get('/v1/sync/targets', () => HttpResponse.json({ targets: [GH] })))
  renderApp(<SyncPanel filter="all" />, { route: '/operations', withAuth: false })
  expect(await screen.findByText('acme/widgets:production')).toBeInTheDocument()
  expect(screen.getByText('failed')).toBeInTheDocument()
  expect(screen.getByLabelText('last error')).toBeInTheDocument()
})

test('sync-now posts to /sync', async () => {
  topo()
  server.use(http.get('/v1/sync/targets', () => HttpResponse.json({ targets: [GH] })))
  let hit = false
  server.use(http.post('/v1/sync/targets/s1/sync', () => { hit = true; return HttpResponse.json({ synced: true }) }))
  renderApp(<SyncPanel filter="all" />, { route: '/operations', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /sync now/i }))
  expect(hit).toBe(true)
})

// ── Create flow ──────────────────────────────────────────────────────────────

function mockEmpty() {
  server.use(http.get('/v1/sync/targets', () => HttpResponse.json({ targets: [] })))
}

test('shows a New target affordance and no CLI empty-state copy', async () => {
  topo(); mockEmpty()
  renderApp(<SyncPanel filter="all" />, { route: '/operations', withAuth: false })
  expect(await screen.findByRole('button', { name: /new target/i })).toBeInTheDocument()
  expect(screen.queryByText(/janus sync create/i)).not.toBeInTheDocument()
})

test('github shows owner/repo + password pat; k8s shows namespace/secret_name/api_url + password ca_cert/token', async () => {
  topo(); mockEmpty()
  renderApp(<SyncPanel filter="all" />, { route: '/operations', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /new target/i }))
  expect(await screen.findByRole('option', { name: 'Acme / prod / prod' })).toBeInTheDocument()

  // github (default) → owner/repo text + pat password
  expect(screen.getByLabelText(/^owner$/i)).toBeInTheDocument()
  expect(screen.getByLabelText(/^repo$/i)).toBeInTheDocument()
  expect(screen.getByLabelText(/personal access token/i)).toHaveAttribute('type', 'password')

  // switch to k8s → namespace/secret_name/api_url text + ca_cert/token password
  await userEvent.selectOptions(screen.getByLabelText(/^provider$/i), 'k8s')
  expect(screen.getByLabelText(/^namespace$/i)).toBeInTheDocument()
  expect(screen.getByLabelText(/secret name/i)).toBeInTheDocument()
  expect(screen.getByLabelText(/api url/i)).toBeInTheDocument()
  expect(screen.getByLabelText(/ca cert/i)).toHaveAttribute('type', 'password')
  expect(screen.getByLabelText(/^token$/i)).toHaveAttribute('type', 'password')
})

test('Create is disabled until github required fields are filled, then POSTs nested addr/creds', async () => {
  topo(); mockEmpty()
  let body: any
  server.use(http.post('/v1/sync/targets', async ({ request }) => {
    body = await request.json()
    return HttpResponse.json({ ...GH, id: 's2' }, { status: 201 })
  }))
  renderApp(<SyncPanel filter="all" />, { route: '/operations', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /new target/i }))
  await screen.findByRole('option', { name: 'Acme / prod / prod' })

  const create = screen.getByRole('button', { name: /^create$/i })
  expect(create).toBeDisabled()

  await userEvent.selectOptions(screen.getByLabelText(/^config$/i), 'c1')
  await userEvent.type(screen.getByLabelText(/^owner$/i), 'acme')
  await userEvent.type(screen.getByLabelText(/^repo$/i), 'widgets')
  await userEvent.type(screen.getByLabelText(/personal access token/i), 'ghp_secret')
  expect(create).toBeEnabled()

  await userEvent.click(create)
  await waitFor(() => expect(screen.queryByRole('heading', { name: /new sync target/i })).not.toBeInTheDocument())
  expect(body.config_id).toBe('c1')
  expect(body.provider).toBe('github')
  expect(body.prune).toBe(true)
  expect(body.addr.owner).toBe('acme')
  expect(body.addr.repo).toBe('widgets')
  expect(body.addr.namespace).toBeUndefined()
  expect(body.creds.pat).toBe('ghp_secret')
  expect(body.creds.token).toBeUndefined()
})

test('k8s POST nests addr.namespace/secret_name + creds.token and sends provider:k8s', async () => {
  topo(); mockEmpty()
  let body: any
  server.use(http.post('/v1/sync/targets', async ({ request }) => {
    body = await request.json()
    return HttpResponse.json({ ...GH, id: 's3', provider: 'k8s' }, { status: 201 })
  }))
  renderApp(<SyncPanel filter="all" />, { route: '/operations', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /new target/i }))
  await screen.findByRole('option', { name: 'Acme / prod / prod' })
  await userEvent.selectOptions(screen.getByLabelText(/^config$/i), 'c1')
  await userEvent.selectOptions(screen.getByLabelText(/^provider$/i), 'k8s')
  await userEvent.type(screen.getByLabelText(/^namespace$/i), 'apps')
  await userEvent.type(screen.getByLabelText(/secret name/i), 'app-secrets')
  await userEvent.type(screen.getByLabelText(/api url/i), 'https://k8s.example.com')
  await userEvent.type(screen.getByLabelText(/^token$/i), 'bearer_secret')

  const create = screen.getByRole('button', { name: /^create$/i })
  expect(create).toBeEnabled()
  await userEvent.click(create)
  await waitFor(() => expect(screen.queryByRole('heading', { name: /new sync target/i })).not.toBeInTheDocument())
  expect(body.provider).toBe('k8s')
  expect(body.addr.namespace).toBe('apps')
  expect(body.addr.secret_name).toBe('app-secrets')
  expect(body.addr.owner).toBeUndefined()
  expect(body.creds.api_url).toBe('https://k8s.example.com')
  expect(body.creds.token).toBe('bearer_secret')
  expect(body.creds.pat).toBeUndefined()
})

test('successful create closes the Sheet and invalidates the list', async () => {
  topo()
  let listCalls = 0
  server.use(http.get('/v1/sync/targets', () => { listCalls++; return HttpResponse.json({ targets: [] }) }))
  server.use(http.post('/v1/sync/targets', () => HttpResponse.json({ ...GH, id: 's2' }, { status: 201 })))
  renderApp(<SyncPanel filter="all" />, { route: '/operations', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /new target/i }))
  await screen.findByRole('option', { name: 'Acme / prod / prod' })
  await userEvent.selectOptions(screen.getByLabelText(/^config$/i), 'c1')
  await userEvent.type(screen.getByLabelText(/^owner$/i), 'acme')
  await userEvent.type(screen.getByLabelText(/^repo$/i), 'widgets')
  await userEvent.type(screen.getByLabelText(/personal access token/i), 'ghp_secret')
  const before = listCalls
  await userEvent.click(screen.getByRole('button', { name: /^create$/i }))
  await waitFor(() => expect(screen.queryByRole('heading', { name: /new sync target/i })).not.toBeInTheDocument())
  await waitFor(() => expect(listCalls).toBeGreaterThan(before))
})

test('a create error keeps the Sheet open and shows the inline curated message', async () => {
  topo(); mockEmpty()
  server.use(http.post('/v1/sync/targets', () =>
    HttpResponse.json({ error: { code: 'validation', message: 'invalid sync target configuration' } }, { status: 400 })))
  renderApp(<SyncPanel filter="all" />, { route: '/operations', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /new target/i }))
  await screen.findByRole('option', { name: 'Acme / prod / prod' })
  await userEvent.selectOptions(screen.getByLabelText(/^config$/i), 'c1')
  await userEvent.type(screen.getByLabelText(/^owner$/i), 'acme')
  await userEvent.type(screen.getByLabelText(/^repo$/i), 'widgets')
  await userEvent.type(screen.getByLabelText(/personal access token/i), 'ghp_secret')
  await userEvent.click(screen.getByRole('button', { name: /^create$/i }))
  // A 400 `validation` maps through errorMessage() to the curated
  // "Please check your input." (raw server text is NOT echoed).
  expect(await screen.findByText(/please check your input/i)).toBeInTheDocument()
  expect(screen.getByRole('heading', { name: /new sync target/i })).toBeInTheDocument()
})
