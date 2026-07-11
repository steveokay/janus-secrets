import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
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

test('view leases opens the sheet', async () => {
  topo()
  server.use(http.get('/v1/dynamic/roles', () => HttpResponse.json({ roles: [ROLE] })))
  server.use(http.get('/v1/dynamic/leases', () => HttpResponse.json({ leases: [] })))
  renderApp(<DynamicPanel filter="all" />, { route: '/operations', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /leases/i }))
  expect(await screen.findByText(/Leases · readonly/)).toBeInTheDocument()
})
