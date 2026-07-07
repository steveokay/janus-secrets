import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ToastProvider } from '../ui/Toast'
import { SecretEditor } from './SecretEditor'

function seed() {
  // MSW matches on path only and ignores the query string, so a single handler
  // must branch on the query param the way the real server does: masked list
  // when there's no ?reveal, raw own-values (+ config version) when raw=true.
  server.use(
    http.get('/v1/configs/c1/secrets', ({ request }) => {
      if (new URL(request.url).searchParams.get('reveal') === 'true')
        return HttpResponse.json({ version: 3, secrets: { DB_URL: 'postgres://a' } })
      return HttpResponse.json({ secrets: {
        DB_URL: { value_version: 3, created_at: '', origin: 'own' },
        SENTRY_DSN: { value_version: 1, created_at: '', origin: 'inherited' },
      } })
    }),
  )
}

test('renders masked rows with origin badges; no reveal on load', async () => {
  let revealed = false
  seed()
  server.use(http.get('/v1/configs/c1/secrets/DB_URL', () => { revealed = true; return HttpResponse.json({ key: 'DB_URL', value: 'postgres://a' }) }))
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  expect(await screen.findByText('DB_URL')).toBeInTheDocument()
  expect(screen.getByText('inherited')).toBeInTheDocument()
  expect(screen.getByText('own')).toBeInTheDocument()
  expect(screen.queryByText('postgres://a')).toBeNull() // masked by default
  expect(revealed).toBe(false) // masked list must not call the audited reveal
})

test('clicking reveal fetches the audited value and shows it', async () => {
  seed()
  let revealed = false
  server.use(http.get('/v1/configs/c1/secrets/DB_URL', () => { revealed = true; return HttpResponse.json({ key: 'DB_URL', value: 'postgres://a' }) }))
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.click(screen.getByRole('button', { name: /reveal db_url/i }))
  await waitFor(() => expect(revealed).toBe(true))
  expect(await screen.findByText('postgres://a')).toBeInTheDocument()
})

test('empty config shows the empty state', async () => {
  server.use(
    http.get('/v1/configs/cEmpty/secrets', ({ request }) => {
      if (new URL(request.url).searchParams.get('reveal') === 'true')
        return HttpResponse.json({ version: 0, secrets: {} })
      return HttpResponse.json({ secrets: {} })
    }),
  )
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/cEmpty', withAuth: false })
  expect(await screen.findByText('No secrets yet')).toBeInTheDocument()
  // AddKeyRow must still be present so the user can add the first key:
  expect(screen.getByLabelText('new key')).toBeInTheDocument()
})

test('a key added via AddKeyRow shows an added row with a discard action', async () => {
  seed()
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.type(screen.getByLabelText('new key'), 'NEW_KEY')
  await userEvent.type(screen.getByLabelText('new value'), 'v')
  await userEvent.click(screen.getByRole('button', { name: /add key/i }))
  expect(await screen.findByText('NEW_KEY')).toBeInTheDocument()
  expect(screen.getByText('added')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /discard new_key/i })).toBeInTheDocument()
})

test('cancelling an in-progress edit exits without changes', async () => {
  seed()
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.click(screen.getByRole('button', { name: /edit db_url/i }))
  expect(screen.getByRole('textbox', { name: /value for db_url/i })).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /cancel edit db_url/i }))
  expect(screen.queryByRole('textbox', { name: /value for db_url/i })).toBeNull()
})

test('the key filter narrows visible rows', async () => {
  seed()
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.type(screen.getByRole('searchbox', { name: /filter keys/i }), 'sentry')
  expect(screen.queryByText('DB_URL')).toBeNull()
  expect(screen.getByText('SENTRY_DSN')).toBeInTheDocument()
})

test('toolbar exposes Import .env and History', async () => {
  seed()
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  expect(screen.getByRole('button', { name: /import \.env/i })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /history/i })).toBeInTheDocument()
})

test('History button opens the version sheet', async () => {
  seed()
  server.use(
    http.get('/v1/configs/c1/versions', () => HttpResponse.json({ versions: [
      { version: 1, message: 'first', created_by: 'x@y.io', created_at: '2026-07-04T10:00:00Z' },
    ] })),
  )
  renderApp(<ToastProvider><SecretEditor /></ToastProvider>, { route: '/projects/p1/configs/c1', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /history/i }))
  expect(await screen.findByText('Version history')).toBeInTheDocument()
  expect(await screen.findByText('first')).toBeInTheDocument()
})
