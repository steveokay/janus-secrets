import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { Sidebar } from './Sidebar'

test('renders projects, then the selected project’s env → config tree', async () => {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'acme', name: 'Acme' }] })),
    http.get('/v1/projects/p1/environments', () => HttpResponse.json({ environments: [{ id: 'e1', slug: 'prod', name: 'Prod' }] })),
    http.get('/v1/projects/p1/environments/e1/configs', () =>
      HttpResponse.json({ configs: [{ id: 'c1', environment_id: 'e1', name: 'prod', inherits_from: null, created_at: '' }] })),
  )
  renderApp(<Sidebar />, { route: '/projects/p1/configs/c1', withAuth: false })
  expect(await screen.findByText('Acme')).toBeInTheDocument()
  expect(await screen.findByText('prod')).toBeInTheDocument() // env label
  expect(await screen.findByRole('link', { name: /^prod$/i })).toHaveAttribute('href', '/projects/p1/configs/c1')
  // Active config link is marked for styling and a11y.
  expect(screen.getByRole('link', { name: /^prod$/i })).toHaveAttribute('aria-current', 'page')
})

test('primary nav links to the five dev-focused destinations', async () => {
  server.use(http.get('/v1/projects', () => HttpResponse.json({ projects: [] })))
  renderApp(<Sidebar />, { route: '/', withAuth: false })
  expect(await screen.findByRole('link', { name: 'Projects' })).toHaveAttribute('href', '/')
  expect(screen.getByRole('link', { name: 'Activity' })).toHaveAttribute('href', '/audit')
  expect(screen.getByRole('link', { name: 'Members' })).toHaveAttribute('href', '/members')
  expect(screen.getByRole('link', { name: 'Tokens' })).toHaveAttribute('href', '/tokens')
  expect(screen.getByRole('link', { name: 'Settings' })).toHaveAttribute('href', '/settings')
})
