import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { Breadcrumb } from './Breadcrumb'

test('renders project / env / config for the active config route', async () => {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'acme', name: 'acme-api' }] })),
    http.get('/v1/projects/p1/environments', () => HttpResponse.json({ environments: [{ id: 'e1', slug: 'prod', name: 'production' }] })),
    http.get('/v1/projects/p1/environments/e1/configs', () =>
      HttpResponse.json({ configs: [{ id: 'c1', environment_id: 'e1', name: 'root', inherits_from: null, created_at: '' }] })),
  )
  renderApp(<Breadcrumb />, { route: '/projects/p1/configs/c1', withAuth: false })
  expect(await screen.findByText('acme-api')).toBeInTheDocument()
  expect(await screen.findByText('production')).toBeInTheDocument()
  expect(await screen.findByText('root')).toBeInTheDocument()
})

test('renders nothing outside a project route', () => {
  server.use(http.get('/v1/projects', () => HttpResponse.json({ projects: [] })))
  const { container } = renderApp(<Breadcrumb />, { route: '/', withAuth: false })
  expect(container.querySelector('nav')).toBeNull()
})

test('project-only route shows just the project name', async () => {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'acme', name: 'acme-api' }] })),
    http.get('/v1/projects/p1/environments', () => HttpResponse.json({ environments: [] })),
  )
  const { container } = renderApp(<Breadcrumb />, { route: '/projects/p1', withAuth: false })
  expect(await screen.findByText('acme-api')).toBeInTheDocument()
  expect(container.textContent).not.toContain('/')
})
