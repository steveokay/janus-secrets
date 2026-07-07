import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ProjectBoard } from './ProjectBoard'

function mock() {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'gw', name: 'api-gateway' }] })),
    http.get('/v1/projects/p1/environments', () =>
      HttpResponse.json({ environments: [
        { id: 'e1', slug: 'dev', name: 'Development' },
        { id: 'e2', slug: 'prod', name: 'Production' },
      ] })),
    http.get('/v1/projects/p1/environments/e1/configs', () =>
      HttpResponse.json({ configs: [
        { id: 'c1', environment_id: 'e1', name: 'dev', inherits_from: null, created_at: '' },
        { id: 'c2', environment_id: 'e1', name: 'dev_personal', inherits_from: 'c1', created_at: '' },
      ] })),
    http.get('/v1/projects/p1/environments/e2/configs', () =>
      HttpResponse.json({ configs: [{ id: 'c3', environment_id: 'e2', name: 'prod', inherits_from: null, created_at: '' }] })),
  )
}

test('renders a column per environment with its configs', async () => {
  mock()
  renderApp(<ProjectBoard />, { route: '/projects/p1', withAuth: false })
  expect(await screen.findByRole('heading', { name: 'Development' })).toBeInTheDocument()
  expect(screen.getByRole('heading', { name: 'Production' })).toBeInTheDocument()
  expect(await screen.findByRole('link', { name: /^dev$/i })).toHaveAttribute('href', '/projects/p1/configs/c1')
  expect(screen.getByRole('link', { name: /prod/i })).toHaveAttribute('href', '/projects/p1/configs/c3')
})

test('inherited config renders nested under its base', async () => {
  mock()
  renderApp(<ProjectBoard />, { route: '/projects/p1', withAuth: false })
  const branch = await screen.findByRole('link', { name: /dev_personal/i })
  expect(branch).toHaveAttribute('data-inherited', 'true')
})

test('shows the CLI hint and breadcrumb', async () => {
  mock()
  renderApp(<ProjectBoard />, { route: '/projects/p1', withAuth: false })
  expect(await screen.findByText('api-gateway')).toBeInTheDocument()
  expect(screen.getByText(/janus run/i)).toBeInTheDocument()
})

test('a failed config fetch surfaces an error, not a permanent skeleton', async () => {
  mock()
  // e1's config list errors; the Development column must show an error, and the
  // other column must still render normally.
  server.use(
    http.get('/v1/projects/p1/environments/e1/configs', () =>
      HttpResponse.json({ error: { code: 'internal', message: 'boom' } }, { status: 500 })),
  )
  renderApp(<ProjectBoard />, { route: '/projects/p1', withAuth: false })
  expect(await screen.findByRole('link', { name: /prod/i })).toBeInTheDocument()
  expect(await screen.findByText(/couldn't load configs/i)).toBeInTheDocument()
})
