import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import { render } from '@testing-library/react'
import { afterEach } from 'vitest'
import App from './App'
import { server } from './test/msw'
import { queryClient } from './lib/queryClient'

// App uses the real queryClient singleton; clear it between tests to avoid
// one test's cached projects/seal-status bleeding into the next.
afterEach(() => queryClient.clear())

function boot(seal: object, me: number) {
  server.use(
    http.get('/v1/sys/seal-status', () => HttpResponse.json(seal)),
    http.get('/v1/auth/me', () => (me === 200 ? HttpResponse.json({ email: 'me@corp.io' }) : new HttpResponse(null, { status: me }))),
    http.get('/v1/projects', () => HttpResponse.json({ projects: [] })),
  )
}

test('sealed server routes to the unseal screen', async () => {
  boot({ initialized: true, sealed: true, type: 'shamir', threshold: 2, shares: 3, progress: 0 }, 401)
  render(<App />)
  expect(await screen.findByText(/unseal janus/i)).toBeInTheDocument()
})

test('unsealed + unauthenticated routes to login', async () => {
  boot({ initialized: true, sealed: false, type: 'shamir' }, 401)
  render(<App />)
  expect(await screen.findByRole('button', { name: /sign in/i })).toBeInTheDocument()
})

test('unsealed + authenticated shows the app shell', async () => {
  boot({ initialized: true, sealed: false, type: 'shamir' }, 200)
  render(<App />)
  // Email moved into the user-menu dropdown; the shell shows the avatar + seal pill.
  expect(await screen.findByRole('button', { name: /user menu/i })).toBeInTheDocument()
  expect(screen.getByText(/unsealed/i)).toBeInTheDocument()
})
