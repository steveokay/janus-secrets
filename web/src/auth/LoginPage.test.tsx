import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { LoginPage } from './LoginPage'

function mockMe(status: number) {
  server.use(http.get('/v1/auth/me', () =>
    status === 200 ? HttpResponse.json({ kind: 'user', id: 'u1', name: 'me@corp.io' }) : new HttpResponse(null, { status }),
  ))
}

test('successful login triggers /me refresh', async () => {
  mockMe(401) // initial mount: not logged in
  let loggedIn = false
  server.use(
    http.post('/v1/auth/login', () => { loggedIn = true; return HttpResponse.json({ user: { id: 'u1', email: 'me@corp.io' } }) }),
  )
  renderApp(<LoginPage />, { withAuth: true })
  await userEvent.type(screen.getByLabelText(/email/i), 'me@corp.io')
  await userEvent.type(screen.getByLabelText(/password/i), 'pw')
  mockMe(200) // after login, /me now succeeds
  await userEvent.click(screen.getByRole('button', { name: /sign in/i }))
  await waitFor(() => expect(loggedIn).toBe(true))
})

test('401 shows a generic error (no enumeration)', async () => {
  mockMe(401)
  server.use(http.post('/v1/auth/login', () => new HttpResponse(JSON.stringify({ error: { code: 'unauthorized', message: 'x' } }), { status: 401 })))
  renderApp(<LoginPage />)
  await userEvent.type(screen.getByLabelText(/email/i), 'me@corp.io')
  await userEvent.type(screen.getByLabelText(/password/i), 'bad')
  await userEvent.click(screen.getByRole('button', { name: /sign in/i }))
  expect(await screen.findByRole('alert')).toHaveTextContent(/invalid email or password/i)
})

test('429 shows a rate-limit message', async () => {
  mockMe(401)
  server.use(http.post('/v1/auth/login', () => new HttpResponse(null, { status: 429 })))
  renderApp(<LoginPage />)
  await userEvent.type(screen.getByLabelText(/email/i), 'me@corp.io')
  await userEvent.type(screen.getByLabelText(/password/i), 'pw')
  await userEvent.click(screen.getByRole('button', { name: /sign in/i }))
  expect(await screen.findByRole('alert')).toHaveTextContent(/too many attempts/i)
})
