import { http, HttpResponse } from 'msw'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ProjectsList } from './ProjectsList'

function mockProjects(projects: { id: string; slug: string; name: string }[]) {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects })),
    http.get('/v1/projects/:pid/environments', () => HttpResponse.json({ environments: [] })),
  )
}

test('renders a card per project with its slug', async () => {
  mockProjects([
    { id: 'p1', slug: 'api-gateway', name: 'api-gateway' },
    { id: 'p2', slug: 'web', name: 'web-frontend' },
  ])
  renderApp(<ProjectsList />, { route: '/', withAuth: false })
  expect(await screen.findByRole('link', { name: /api-gateway/i })).toHaveAttribute('href', '/projects/p1')
  expect(screen.getByRole('link', { name: /web-frontend/i })).toHaveAttribute('href', '/projects/p2')
})

test('search filters the list', async () => {
  mockProjects([
    { id: 'p1', slug: 'api-gateway', name: 'api-gateway' },
    { id: 'p2', slug: 'web', name: 'web-frontend' },
  ])
  renderApp(<ProjectsList />, { route: '/', withAuth: false })
  await screen.findByRole('link', { name: /api-gateway/i })
  await userEvent.type(screen.getByRole('searchbox', { name: /search projects/i }), 'web')
  expect(screen.queryByRole('link', { name: /api-gateway/i })).not.toBeInTheDocument()
  expect(screen.getByRole('link', { name: /web-frontend/i })).toBeInTheDocument()
})

test('sort Z-A reverses order', async () => {
  mockProjects([
    { id: 'p1', slug: 'alpha', name: 'alpha' },
    { id: 'p2', slug: 'zeta', name: 'zeta' },
  ])
  renderApp(<ProjectsList />, { route: '/', withAuth: false })
  await screen.findByRole('link', { name: /alpha/i })
  await userEvent.selectOptions(screen.getByRole('combobox', { name: /sort/i }), 'name-desc')
  const links = screen.getAllByRole('link')
  expect(within(links[0]).getByText('zeta')).toBeInTheDocument()
})

test('empty state offers to create the first project', async () => {
  mockProjects([])
  renderApp(<ProjectsList />, { route: '/', withAuth: false })
  expect(await screen.findByText(/no projects yet/i)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /create.*project/i })).toBeInTheDocument()
})
