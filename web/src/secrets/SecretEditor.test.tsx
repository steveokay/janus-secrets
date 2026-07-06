import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
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
