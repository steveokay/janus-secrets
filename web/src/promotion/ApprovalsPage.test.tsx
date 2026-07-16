import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ToastProvider } from '../ui/Toast'
import { ApprovalsPage } from './ApprovalsPage'

function mockMe() {
  server.use(http.get('/v1/auth/me', () => HttpResponse.json({ kind: 'user', id: 'user-me', name: 'me@acme.dev' })))
}

function mockProjects() {
  server.use(
    http.get('/v1/projects', () =>
      HttpResponse.json({
        projects: [
          { id: 'proj-1', slug: 'alpha', name: 'Alpha' },
          { id: 'proj-2', slug: 'beta', name: 'Beta' },
        ],
      }),
    ),
  )
}

function renderPage() {
  mockMe()
  mockProjects()
  server.use(http.get('/v1/promote/requests', () => HttpResponse.json({ requests: [] })))
  return renderApp(
    <ToastProvider>
      <ApprovalsPage />
    </ToastProvider>,
    { route: '/', withAuth: true },
  )
}

test('renders a project selector and the requests panel for the selected project', async () => {
  renderPage()
  expect(await screen.findByText(/approvals/i)).toBeInTheDocument()
  expect(await screen.findByText(/pending approval/i)).toBeInTheDocument()
  expect(await screen.findByText(/my requests/i)).toBeInTheDocument()
})

test('switching the project selector re-fetches requests scoped to the new project', async () => {
  let lastProject = ''
  renderPage()
  server.use(
    http.get('/v1/promote/requests', ({ request }) => {
      lastProject = new URL(request.url).searchParams.get('project') ?? ''
      return HttpResponse.json({ requests: [] })
    }),
  )
  await screen.findByText(/pending approval/i)
  const select = screen.getByLabelText(/project/i) as HTMLSelectElement
  await userEvent.selectOptions(select, 'proj-2')
  await waitFor(() => expect(lastProject).toBe('proj-2'))
})
