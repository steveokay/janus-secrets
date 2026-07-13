import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ToastProvider } from '../ui/Toast'
import { OIDCSection } from './OIDCSection'

function mount() {
  return renderApp(
    <ToastProvider><OIDCSection /></ToastProvider>,
    { route: '/settings?section=oidc', withAuth: false },
  )
}

const VIEW = {
  name: 'default', issuer: 'https://iss', client_id: 'cid', scopes: ['openid', 'email'],
  redirect_url: 'https://app/cb', enabled: true, secret_set: true,
}

test('403 renders a requires-admin hint and no form', async () => {
  server.use(
    http.get('/v1/sys/oidc', () =>
      HttpResponse.json({ error: { code: 'forbidden', message: 'nope' } }, { status: 403 })),
  )
  mount()
  expect(await screen.findByText(/instance admin/i)).toBeInTheDocument()
  expect(screen.queryByLabelText(/issuer/i)).not.toBeInTheDocument()
})

test('404 renders the not-configured empty state with a configure affordance', async () => {
  server.use(
    http.get('/v1/sys/oidc', () =>
      HttpResponse.json({ error: { code: 'not_found', message: 'not configured' } }, { status: 404 })),
  )
  mount()
  expect(await screen.findByText(/no oidc provider configured/i)).toBeInTheDocument()
  const configure = screen.getByRole('button', { name: /configure provider/i })
  await userEvent.click(configure)
  // the empty form is revealed; the secret is required (Save disabled until typed)
  expect(await screen.findByLabelText(/issuer/i)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /save/i })).toBeDisabled()
})

test('200 shows the view with an enabled pill and a secret-set caption, secret input empty', async () => {
  server.use(http.get('/v1/sys/oidc', () => HttpResponse.json(VIEW)))
  mount()
  expect(await screen.findByDisplayValue('https://iss')).toBeInTheDocument()
  expect(screen.getByDisplayValue('cid')).toBeInTheDocument()
  expect(screen.getByDisplayValue('https://app/cb')).toBeInTheDocument()
  expect(screen.getByDisplayValue(/openid/)).toBeInTheDocument()
  // enabled pill
  expect(screen.getByText(/^enabled$/i)).toBeInTheDocument()
  // secret-set informational caption
  expect(screen.getByText(/client secret is currently set/i)).toBeInTheDocument()
  // the secret input is NEVER prefilled
  const secret = screen.getByLabelText(/client secret/i) as HTMLInputElement
  expect(secret.value).toBe('')
})

test('save is disabled until a secret is (re-)entered, then sends it', async () => {
  let body: any
  server.use(
    http.get('/v1/sys/oidc', () => HttpResponse.json(VIEW)),
    http.put('/v1/sys/oidc', async ({ request }) => { body = await request.json(); return HttpResponse.json({ ok: true }) }),
  )
  mount()
  const save = await screen.findByRole('button', { name: /save/i })
  expect(save).toBeDisabled()                        // no secret typed yet (full-replace requires it)
  await userEvent.type(screen.getByLabelText(/client secret/i), 'new-secret')
  expect(save).toBeEnabled()
  await userEvent.click(save)
  await waitFor(() => expect(body).toBeTruthy())
  expect(body.client_secret).toBe('new-secret')      // typed secret is sent on every save
  expect(body.issuer).toBe('https://iss')
  expect(body.scopes).toEqual(['openid', 'email'])   // parsed back to an array
})

test('delete confirms then calls DELETE', async () => {
  let deleted = false
  server.use(
    http.get('/v1/sys/oidc', () => HttpResponse.json(VIEW)),
    http.delete('/v1/sys/oidc', () => { deleted = true; return new HttpResponse(null, { status: 204 }) }),
  )
  mount()
  await userEvent.click(await screen.findByRole('button', { name: /delete provider/i }))
  await userEvent.click(await screen.findByRole('button', { name: /^delete$/i }))
  await waitFor(() => expect(deleted).toBe(true))
})
