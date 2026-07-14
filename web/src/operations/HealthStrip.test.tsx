import { http, HttpResponse } from 'msw'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { HealthStrip } from './HealthStrip'

// Mirrors RotationPanel.test's topo(): the aggregator walks projects → envs →
// configs before fanning out the engine lists.
function topo() {
  server.use(http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'acme', name: 'Acme' }] })))
  server.use(http.get('/v1/projects/:pid/environments', () => HttpResponse.json({ environments: [{ id: 'e1', slug: 'prod', name: 'prod' }] })))
  server.use(http.get('/v1/projects/:pid/environments/:eid/configs', () => HttpResponse.json({ configs: [{ id: 'c1', environment_id: 'e1', name: 'prod', inherits_from: null, created_at: 'x' }] })))
}
const POLICY = { id: 'r1', project_id: 'p1', config_id: 'c1', secret_key: 'DB_PASSWORD', type: 'postgres', interval_seconds: 3600, status: 'failed', failure_count: 3, last_error: 'boom', next_rotation_at: 'x', last_rotated_at: null, created_at: 'x' }
const POLICY2 = { ...POLICY, id: 'r2', secret_key: 'API_KEY' }

function seedEngines() {
  server.use(http.get('/v1/rotation/policies', () => HttpResponse.json({ policies: [POLICY, POLICY2] })))
  server.use(http.get('/v1/sync/targets', () => HttpResponse.json({ targets: [] })))
  server.use(http.get('/v1/dynamic/roles', () => HttpResponse.json({ roles: [] })))
}

test('Rotation segment shows the failing count with danger styling + icon', async () => {
  topo()
  seedEngines()
  renderApp(<HealthStrip filter="all" onGo={() => {}} />, { route: '/operations', withAuth: false })

  const seg = await screen.findByRole('button', { name: /rotation: 2 failing/i })
  expect(within(seg).getByText(/2 failing/)).toBeInTheDocument()
  // failing > 0 → danger token on the failing span + an AlertTriangle icon
  const failing = within(seg).getByText(/2 failing/)
  expect(failing.className).toMatch(/text-danger/)
  expect(seg.querySelector('svg')).not.toBeNull()
})

test('clicking the Rotation segment calls onGo("rotation")', async () => {
  const onGo = vi.fn()
  // No aggregator wiring needed: with the default (unmocked) msw handlers the
  // strip still renders segments; a focused click test just exercises onGo.
  topo()
  seedEngines()
  renderApp(<HealthStrip filter="all" onGo={onGo} />, { route: '/operations', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /rotation:/i }))
  expect(onGo).toHaveBeenCalledWith('rotation')
})

test('Dynamic segment shows role count only (no failing/lease fan-out)', async () => {
  topo()
  server.use(http.get('/v1/rotation/policies', () => HttpResponse.json({ policies: [] })))
  server.use(http.get('/v1/sync/targets', () => HttpResponse.json({ targets: [] })))
  server.use(http.get('/v1/dynamic/roles', () => HttpResponse.json({ roles: [{ id: 'd1', project_id: 'p1', config_id: 'c1', name: 'ro', default_ttl_seconds: 60, max_ttl_seconds: 120, created_at: 'x' }] })))
  renderApp(<HealthStrip filter="all" onGo={() => {}} />, { route: '/operations', withAuth: false })
  const seg = await screen.findByRole('button', { name: /dynamic:/i })
  expect(within(seg).getByText('1 role')).toBeInTheDocument()
})

test('failing === 0 renders muted (no danger token, no icon)', async () => {
  topo()
  server.use(http.get('/v1/rotation/policies', () => HttpResponse.json({ policies: [{ ...POLICY, status: 'active' }] })))
  server.use(http.get('/v1/sync/targets', () => HttpResponse.json({ targets: [] })))
  server.use(http.get('/v1/dynamic/roles', () => HttpResponse.json({ roles: [] })))
  renderApp(<HealthStrip filter="all" onGo={() => {}} />, { route: '/operations', withAuth: false })
  const seg = await screen.findByRole('button', { name: /rotation: 0 failing/i })
  const failing = within(seg).getByText(/0 failing/)
  expect(failing.className).not.toMatch(/text-danger/)
  expect(seg.querySelector('svg')).toBeNull()
})
