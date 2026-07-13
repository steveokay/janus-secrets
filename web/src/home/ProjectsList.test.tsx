import { http, HttpResponse } from 'msw'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, useLocation } from 'react-router-dom'
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

test('opens the create dialog on ?new=1 and clears the param', async () => {
  mockProjects([{ id: 'p1', slug: 'api-gateway', name: 'api-gateway' }])
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  function LocationProbe() {
    const loc = useLocation()
    return <div data-testid="loc">{loc.search}</div>
  }
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/projects?new=1']}>
        <ProjectsList />
        <LocationProbe />
      </MemoryRouter>
    </QueryClientProvider>,
  )
  // The create-project dialog appears.
  expect(await screen.findByRole('heading', { name: /create project/i })).toBeInTheDocument()
  // The `new` param is cleared so a refresh/back doesn't re-open it.
  expect(screen.getByTestId('loc')).toHaveTextContent('')
  expect(screen.getByTestId('loc').textContent).not.toContain('new=1')
})

test('shows the instance Reads 24h strip above the projects', async () => {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'api', name: 'api' }] })),
    http.get('/v1/projects/:pid/environments', () => HttpResponse.json({ environments: [] })),
    http.get('/v1/metrics/reads-24h', () =>
      HttpResponse.json({ reads_24h: 42, top_configs: [], top_tokens: [] })),
  )
  renderApp(<ProjectsList />, { route: '/', withAuth: false })
  expect(await screen.findByText('42')).toBeInTheDocument()
})
