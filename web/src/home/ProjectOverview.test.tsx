import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ProjectOverview } from './ProjectOverview'

test('renders env cards with config rows, counts and empty env', async () => {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'acme', name: 'acme-api' }] })),
    http.get('/v1/projects/p1/environments', () =>
      HttpResponse.json({ environments: [
        { id: 'e1', slug: 'prod', name: 'production' },
        { id: 'e2', slug: 'dev', name: 'development' },
      ] }),
    ),
    http.get('/v1/projects/p1/environments/e1/configs', () =>
      HttpResponse.json({ configs: [{ id: 'c1', environment_id: 'e1', name: 'root', inherits_from: null, created_at: '' }] }),
    ),
    http.get('/v1/projects/p1/environments/e2/configs', () => HttpResponse.json({ configs: [] })),
    http.get('/v1/configs/c1/secrets', () =>
      HttpResponse.json({ secrets: {
        DATABASE_URL: { value_version: 3, created_at: '2026-07-06T10:00:00Z', origin: 'own' },
        API_KEY: { value_version: 1, created_at: '2026-07-06T08:00:00Z', origin: 'own' },
      } }),
    ),
  )
  renderApp(<ProjectOverview />, { route: '/projects/p1', withAuth: false })
  expect(await screen.findByRole('heading', { name: 'acme-api' })).toBeInTheDocument()
  expect(await screen.findByText('production')).toBeInTheDocument()
  expect(await screen.findByRole('link', { name: /root/ })).toHaveAttribute('href', '/projects/p1/configs/c1')
  expect(await screen.findByText(/2 keys/)).toBeInTheDocument()
  expect(await screen.findByText('No configs yet')).toBeInTheDocument()
  expect(screen.getByText(/Reads 24h/)).toBeInTheDocument()
})

test('zero environments shows EmptyState with create action', async () => {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'acme', name: 'acme-api' }] })),
    http.get('/v1/projects/p1/environments', () => HttpResponse.json({ environments: [] })),
  )
  renderApp(<ProjectOverview />, { route: '/projects/p1', withAuth: false })
  expect(await screen.findByText('No environments yet')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /create environment/i }))
  expect(await screen.findByRole('heading', { name: /new environment/i })).toBeInTheDocument()
})
