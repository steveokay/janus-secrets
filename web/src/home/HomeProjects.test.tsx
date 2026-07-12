import { http, HttpResponse } from 'msw'
import { screen, waitFor, within } from '@testing-library/react'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { useProjects } from '../secrets/nav'
import { HomeProjects } from './HomeProjects'

// HomeProjects takes the projects query as a prop (HomePage owns the fetch);
// this harness stands in for HomePage.
function Harness() {
  const projects = useProjects()
  return <HomeProjects projects={projects} />
}

function mockProjects(
  projects: { id: string; slug: string; name: string }[],
  environments: { id: string; slug: string; name: string }[] = [],
) {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects })),
    http.get('/v1/projects/:pid/environments', () => HttpResponse.json({ environments })),
  )
}

test('renders a card per project linking to the project page', async () => {
  mockProjects([
    { id: 'p1', slug: 'api-gateway', name: 'api-gateway' },
    { id: 'p2', slug: 'web', name: 'web-frontend' },
  ])
  renderApp(<Harness />, { withAuth: false })
  expect(await screen.findByRole('link', { name: /api-gateway/i })).toHaveAttribute('href', '/projects/p1')
  expect(screen.getByRole('link', { name: /web-frontend/i })).toHaveAttribute('href', '/projects/p2')
})

test('card shows name, slug and env pills', async () => {
  mockProjects(
    [{ id: 'p1', slug: 'api-gateway', name: 'API Gateway' }],
    [
      { id: 'e1', slug: 'dev', name: 'dev' },
      { id: 'e2', slug: 'prod', name: 'prod' },
    ],
  )
  renderApp(<Harness />, { withAuth: false })
  const card = await screen.findByRole('link', { name: /api gateway/i })
  expect(within(card).getByText('API Gateway')).toBeInTheDocument()
  expect(within(card).getByText('api-gateway')).toBeInTheDocument()
  expect(await within(card).findByText('dev')).toBeInTheDocument()
  expect(within(card).getByText('prod')).toBeInTheDocument()
})

test('empty state offers a New project action linking to /projects', async () => {
  mockProjects([])
  renderApp(<Harness />, { withAuth: false })
  expect(await screen.findByText(/no projects yet/i)).toBeInTheDocument()
  expect(screen.getByRole('link', { name: /new project/i })).toHaveAttribute('href', '/projects')
})

test('hides entirely when the projects query errors', async () => {
  server.use(
    http.get('/v1/projects', () =>
      HttpResponse.json({ error: { code: 'forbidden', message: 'x' } }, { status: 403 })),
  )
  const { container } = renderApp(<Harness />, { withAuth: false })
  // Skeletons render while loading; once the query errors the section unmounts.
  await waitFor(() => expect(container).toBeEmptyDOMElement())
  expect(screen.queryByText(/no projects yet/i)).not.toBeInTheDocument()
})
