import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ProjectBoard } from './ProjectBoard'

// Ops handlers so the board renders cleanly under onUnhandledRequest:'error'.
function opsEmpty() {
  server.use(
    http.get('/v1/rotation/policies', () => HttpResponse.json({ policies: [] })),
    http.get('/v1/sync/targets', () => HttpResponse.json({ targets: [] })),
  )
}

const CREATED_AT = new Date(Date.now() - 3_600_000).toISOString()

function mock() {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'gw', name: 'api-gateway' }] })),
    http.get('/v1/projects/p1/environments', () =>
      HttpResponse.json({ environments: [{ id: 'e1', slug: 'prod', name: 'Production' }] })),
    http.get('/v1/projects/p1/environments/e1/configs', () =>
      HttpResponse.json({ configs: [
        // promoted config
        { id: 'c1', environment_id: 'e1', name: 'prod', inherits_from: null, created_at: CREATED_AT,
          promoted_from_env: 'staging', promoted_from_version: 3 },
        // plain config, no provenance
        { id: 'c2', environment_id: 'e1', name: 'prod_alt', inherits_from: null, created_at: CREATED_AT },
      ] })),
  )
  opsEmpty()
}

test('promoted config card shows the promoted-from indicator with an accessible label', async () => {
  mock()
  renderApp(<ProjectBoard />, { route: '/projects/p1', withAuth: false })
  const promoted = await screen.findByRole('link', { name: /^prod\b/i })
  expect(promoted).toBeInTheDocument()
  expect(await screen.findByTitle('Promoted from staging v3')).toBeInTheDocument()
})

test('non-promoted config card shows no promoted-from indicator', async () => {
  mock()
  renderApp(<ProjectBoard />, { route: '/projects/p1', withAuth: false })
  const plain = await screen.findByRole('link', { name: /prod_alt/i })
  expect(plain).toBeInTheDocument()
  // Exactly one indicator across the whole board (only c1 has provenance).
  expect(screen.getAllByTitle(/^Promoted from /)).toHaveLength(1)
})
