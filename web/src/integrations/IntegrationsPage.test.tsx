import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { IntegrationsPage } from './IntegrationsPage'

// useSync('all') fans out: list projects → per-project envs (for the config
// name map) → per-project sync targets. Empty envs keeps the map empty; the
// target list still drives the provider counts.
function topo() {
  server.use(http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'acme', name: 'Acme' }] })))
  server.use(http.get('/v1/projects/:pid/environments', () => HttpResponse.json({ environments: [] })))
}

const T = (provider: 'github' | 'k8s', id: string) => ({
  id, project_id: 'p1', config_id: 'c1', provider, prune: true, interval_seconds: 300,
  addr: {}, status: 'active', failure_count: 0, next_sync_at: 'x', managed_keys: [], created_at: 'x',
})

test('populated: shows per-provider sync counts, federation and OIDC status, and deep-links', async () => {
  topo()
  server.use(http.get('/v1/sync/targets', () => HttpResponse.json({ targets: [T('github', 's1'), T('github', 's2'), T('k8s', 's3')] })))
  server.use(http.get('/v1/sys/oidc/federation', () => HttpResponse.json({ issuer: 'https://x', audience: 'urn:janus', enabled: true })))
  server.use(http.get('/v1/auth/oidc/status', () => HttpResponse.json({ enabled: true, name: 'GitHub' })))

  renderApp(<IntegrationsPage />, { route: '/integrations', withAuth: false })

  expect(await screen.findByText('2')).toBeInTheDocument() // GitHub Actions sync count
  expect(screen.getByText('1')).toBeInTheDocument() // Kubernetes sync count
  expect(screen.getAllByText('enabled').length).toBeGreaterThanOrEqual(2) // federation + OIDC login
  expect(screen.getByRole('link', { name: /federation/i })).toHaveAttribute('href', '/settings?section=federation')
  expect(screen.getByRole('link', { name: /configure/i })).toHaveAttribute('href', '/settings?section=oidc')
  expect(screen.getAllByRole('link', { name: /sync|manage/i })[0]).toHaveAttribute('href', '/operations?tab=sync')
})

test('403-tolerant: all three cards still render with neutral status and working links', async () => {
  topo()
  const forbid = () => HttpResponse.json({ error: { code: 'forbidden', message: 'no' } }, { status: 403 })
  server.use(http.get('/v1/sync/targets', forbid))
  server.use(http.get('/v1/sys/oidc/federation', forbid))
  server.use(http.get('/v1/auth/oidc/status', () => HttpResponse.json({ enabled: false })))

  renderApp(<IntegrationsPage />, { route: '/integrations', withAuth: false })

  // GitHub sync (—), Kubernetes sync (—), CI federation (—) = 3 neutral lines.
  expect(await screen.findByRole('heading', { name: 'Kubernetes' })).toBeInTheDocument()
  expect(screen.getAllByText('—')).toHaveLength(3)
  expect(screen.getByText('disabled')).toBeInTheDocument() // OIDC status endpoint is public
  expect(screen.getByRole('heading', { name: 'GitHub' })).toBeInTheDocument()
  expect(screen.getByRole('heading', { name: /OIDC/i })).toBeInTheDocument()
  expect(screen.getByRole('link', { name: /configure/i })).toHaveAttribute('href', '/settings?section=oidc')
})
