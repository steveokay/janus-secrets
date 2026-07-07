import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ToastProvider } from '../ui/Toast'
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
  expect(screen.getByText(/each project holds/i)).toBeInTheDocument()
  await userEvent.type(screen.getByLabelText(/slug/i), 'acme')
  await userEvent.type(screen.getByLabelText(/name/i), 'Acme')
  await userEvent.click(screen.getByRole('button', { name: /create/i }))
  await waitFor(() => expect(onCreated).toHaveBeenCalledWith(expect.objectContaining({ id: 'p1' })))
  expect(body).toEqual({ slug: 'acme', name: 'Acme' })
})

test('successful create fires a success toast; a failing create surfaces the mapped message inline', async () => {
  // Success path: mounted under ToastProvider so the confirmation toast renders.
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [] })),
    http.post('/v1/projects', () => HttpResponse.json({ id: 'p1', slug: 'acme', name: 'Acme' })),
  )
  const { unmount } = renderApp(
    <ToastProvider><CreateProjectForm onCreated={() => {}} onClose={() => {}} /></ToastProvider>,
    { withAuth: false },
  )
  await userEvent.type(screen.getByLabelText(/slug/i), 'acme')
  await userEvent.type(screen.getByLabelText(/name/i), 'Acme')
  await userEvent.click(screen.getByRole('button', { name: /create/i }))
  expect(await screen.findByText('Project created')).toBeInTheDocument()
  unmount()

  // Failure path: create-form failures stay inline (curated errorMessage).
  server.use(
    http.post('/v1/projects', () =>
      HttpResponse.json({ error: { code: 'conflict', message: 'slug already in use' } }, { status: 409 })),
  )
  renderApp(
    <ToastProvider><CreateProjectForm onCreated={() => {}} onClose={() => {}} /></ToastProvider>,
    { withAuth: false },
  )
  await userEvent.type(screen.getByLabelText(/slug/i), 'acme')
  await userEvent.type(screen.getByLabelText(/name/i), 'Acme')
  await userEvent.click(screen.getByRole('button', { name: /create/i }))
  expect(await screen.findByText('slug already in use')).toBeInTheDocument()
})
