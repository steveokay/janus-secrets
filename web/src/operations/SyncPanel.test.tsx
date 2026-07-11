import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
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
