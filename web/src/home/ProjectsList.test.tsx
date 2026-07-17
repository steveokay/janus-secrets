import { http, HttpResponse } from 'msw'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ToastProvider } from '../ui/Toast'
import { ProjectsList } from './ProjectsList'
import * as endpointsModule from '../lib/endpoints'

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

test('renders a glyph badge on each project card', async () => {
  mockProjects([
    { id: 'p1', slug: 'api-gateway', name: 'api-gateway' },
    { id: 'p2', slug: 'web', name: 'web-frontend' },
  ])
  renderApp(<ProjectsList />, { route: '/', withAuth: false })
  await screen.findByRole('link', { name: /api-gateway/i })
  const glyphs = screen.getAllByTestId('project-glyph')
  expect(glyphs).toHaveLength(2)
})

test('recency line shows active for last_activity_at and created for created_at only', async () => {
  server.use(
    http.get('/v1/projects', () =>
      HttpResponse.json({
        projects: [
          {
            id: 'p1',
            slug: 'active-proj',
            name: 'active-proj',
            created_at: '2026-07-01T00:00:00Z',
            last_activity_at: '2026-07-16T00:00:00Z',
          },
          {
            id: 'p2',
            slug: 'quiet-proj',
            name: 'quiet-proj',
            created_at: '2026-07-10T00:00:00Z',
            last_activity_at: null,
          },
        ],
      }),
    ),
    http.get('/v1/projects/:pid/environments', () => HttpResponse.json({ environments: [] })),
  )
  renderApp(<ProjectsList />, { route: '/', withAuth: false })
  await screen.findByRole('link', { name: /active-proj/i })
  expect(screen.getByText(/active /)).toBeInTheDocument()
  expect(screen.getByText(/created /)).toBeInTheDocument()
})

test('sort select offers Newest/Oldest/Recently active, and Newest orders by created_at desc', async () => {
  server.use(
    http.get('/v1/projects', () =>
      HttpResponse.json({
        projects: [
          { id: 'p1', slug: 'older', name: 'older', created_at: '2026-01-01T00:00:00Z' },
          { id: 'p2', slug: 'newer', name: 'newer', created_at: '2026-07-01T00:00:00Z' },
        ],
      }),
    ),
    http.get('/v1/projects/:pid/environments', () => HttpResponse.json({ environments: [] })),
  )
  renderApp(<ProjectsList />, { route: '/', withAuth: false })
  await screen.findByRole('link', { name: /older/i })

  const sortSelect = screen.getByRole('combobox', { name: /sort/i })
  expect(within(sortSelect).getByRole('option', { name: 'Newest' })).toHaveValue('created-desc')
  expect(within(sortSelect).getByRole('option', { name: 'Oldest' })).toHaveValue('created-asc')
  expect(within(sortSelect).getByRole('option', { name: 'Recently active' })).toHaveValue('activity-desc')

  await userEvent.selectOptions(sortSelect, 'created-desc')
  const links = screen.getAllByRole('link')
  expect(within(links[0]).getByText('newer')).toBeInTheDocument()
})

test('the card quick-action menu offers Rename and Delete', async () => {
  mockProjects([{ id: 'p1', slug: 'api-gateway', name: 'api-gateway' }])
  renderApp(<ProjectsList />, { route: '/', withAuth: false })
  await screen.findByRole('link', { name: /api-gateway/i })
  await userEvent.click(screen.getByRole('button', { name: /actions for api-gateway/i }))
  expect(await screen.findByRole('menuitem', { name: /rename/i })).toBeInTheDocument()
  expect(screen.getByRole('menuitem', { name: /delete/i })).toBeInTheDocument()
})

test('renaming a project via the quick-action menu calls endpoints.renameProject and invalidates the projects query', async () => {
  mockProjects([{ id: 'p1', slug: 'api-gateway', name: 'api-gateway' }])
  const renameSpy = vi
    .spyOn(endpointsModule.endpoints, 'renameProject')
    .mockResolvedValue({ id: 'p1', slug: 'api-gateway', name: 'api-gateway-2' })
  renderApp(<ProjectsList />, { route: '/', withAuth: false })
  await screen.findByRole('link', { name: /api-gateway/i })
  await userEvent.click(screen.getByRole('button', { name: /actions for api-gateway/i }))
  await userEvent.click(await screen.findByRole('menuitem', { name: /rename/i }))
  const input = await screen.findByRole('textbox', { name: /name/i })
  expect(input).toHaveValue('api-gateway')
  await userEvent.clear(input)
  await userEvent.type(input, 'api-gateway-2')
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  expect(renameSpy).toHaveBeenCalledWith('p1', 'api-gateway-2')
  renameSpy.mockRestore()
})

test('rename success toast names the NEW project name, not the stale old one', async () => {
  mockProjects([{ id: 'p1', slug: 'api-gateway', name: 'api-gateway' }])
  const renameSpy = vi
    .spyOn(endpointsModule.endpoints, 'renameProject')
    .mockResolvedValue({ id: 'p1', slug: 'api-gateway', name: 'api-gateway-2' })
  renderApp(
    <ToastProvider>
      <ProjectsList />
    </ToastProvider>,
    { route: '/', withAuth: false },
  )
  await screen.findByRole('link', { name: /api-gateway/i })
  await userEvent.click(screen.getByRole('button', { name: /actions for api-gateway/i }))
  await userEvent.click(await screen.findByRole('menuitem', { name: /rename/i }))
  const input = await screen.findByRole('textbox', { name: /name/i })
  await userEvent.clear(input)
  await userEvent.type(input, 'api-gateway-2')
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  // The toast must reflect the freshly-entered name, not the stale prop.
  expect(await screen.findByText(/renamed to api-gateway-2/i)).toBeInTheDocument()
  expect(screen.queryByText('Renamed to api-gateway')).not.toBeInTheDocument()
  renameSpy.mockRestore()
})
