import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { OperationsPage } from './OperationsPage'

function baseTopo() {
  server.use(http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'acme', name: 'Acme' }] })))
  server.use(http.get('/v1/projects/:pid/environments', () => HttpResponse.json({ environments: [{ id: 'e1', slug: 'prod', name: 'prod' }] })))
  server.use(http.get('/v1/projects/:pid/environments/:eid/configs', () => HttpResponse.json({ configs: [{ id: 'c1', environment_id: 'e1', name: 'prod', inherits_from: null, created_at: 'x' }] })))
  server.use(http.get('/v1/rotation/policies', () => HttpResponse.json({ policies: [] })))
  server.use(http.get('/v1/sync/targets', () => HttpResponse.json({ targets: [] })))
  server.use(http.get('/v1/dynamic/roles', () => HttpResponse.json({ roles: [] })))
}

test('defaults to the Rotation tab and switches to Sync', async () => {
  baseTopo()
  renderApp(<OperationsPage />, { route: '/operations', withAuth: false })
  expect(await screen.findByRole('tab', { name: /rotation/i })).toHaveAttribute('aria-selected', 'true')
  await userEvent.click(screen.getByRole('tab', { name: /sync/i }))
  expect(screen.getByRole('tab', { name: /sync/i })).toHaveAttribute('aria-selected', 'true')
})

test('honors ?tab=dynamic from the URL', async () => {
  baseTopo()
  renderApp(<OperationsPage />, { route: '/operations?tab=dynamic', withAuth: false })
  expect(await screen.findByRole('tab', { name: /dynamic/i })).toHaveAttribute('aria-selected', 'true')
})
