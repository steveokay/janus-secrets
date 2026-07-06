import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { CreateProjectForm } from './CreateForms'

test('creating a project posts slug+name and calls onCreated', async () => {
  let body: any
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [] })),
    http.post('/v1/projects', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ id: 'p1', slug: body.slug, name: body.name })
    }),
  )
  const onCreated = vi.fn()
  renderApp(<CreateProjectForm onCreated={onCreated} onClose={() => {}} />, { withAuth: false })
  await userEvent.type(screen.getByLabelText(/slug/i), 'acme')
  await userEvent.type(screen.getByLabelText(/name/i), 'Acme')
  await userEvent.click(screen.getByRole('button', { name: /create/i }))
  await waitFor(() => expect(onCreated).toHaveBeenCalledWith(expect.objectContaining({ id: 'p1' })))
  expect(body).toEqual({ slug: 'acme', name: 'Acme' })
})
