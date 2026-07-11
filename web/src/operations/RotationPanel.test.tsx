import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
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
