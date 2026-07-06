import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { Landing } from './Landing'

test('no projects: hero with CTA that opens the create dialog', async () => {
  server.use(http.get('/v1/projects', () => HttpResponse.json({ projects: [] })))
  renderApp(<Landing />, { withAuth: false })
  expect(await screen.findByText('Your secrets, sealed and audited')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /create your first project/i }))
  expect(await screen.findByRole('heading', { name: /new project/i })).toBeInTheDocument()
})

test('with projects: link cards + new-project button', async () => {
  server.use(
    http.get('/v1/projects', () =>
      HttpResponse.json({ projects: [
        { id: 'p1', slug: 'acme', name: 'acme-api' },
        { id: 'p2', slug: 'web', name: 'storefront' },
      ] }),
    ),
  )
  renderApp(<Landing />, { withAuth: false })
  expect(await screen.findByRole('link', { name: /acme-api/ })).toHaveAttribute('href', '/projects/p1')
  expect(screen.getByRole('link', { name: /storefront/ })).toHaveAttribute('href', '/projects/p2')
  expect(screen.getByRole('button', { name: /new project/i })).toBeInTheDocument()
})

test('error state announces failure', async () => {
  server.use(http.get('/v1/projects', () => HttpResponse.error()))
  renderApp(<Landing />, { withAuth: false })
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not load projects.')
})

test('loading skeletons are aria-hidden', () => {
  server.use(http.get('/v1/projects', async () => {
    await new Promise((r) => setTimeout(r, 1000))
    return HttpResponse.json({ projects: [] })
  }))
  const { container } = renderApp(<Landing />, { withAuth: false })
  const skeleton = container.querySelector('[aria-hidden]')
  expect(skeleton).not.toBeNull()
})
