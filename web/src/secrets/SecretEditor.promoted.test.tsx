import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ToastProvider } from '../ui/Toast'
import { SecretEditor } from './SecretEditor'

function seedSecrets() {
  server.use(
    http.get('/v1/configs/c1/secrets', ({ request }) => {
      if (new URL(request.url).searchParams.get('reveal') === 'true')
        return HttpResponse.json({ version: 4, secrets: {} })
      return HttpResponse.json({ secrets: {
        DB_URL: { value_version: 4, created_at: '', origin: 'own' },
      } })
    }),
  )
}

function render() {
  return renderApp(<ToastProvider><SecretEditor /></ToastProvider>, { route: '/projects/p1/configs/c1', withAuth: false })
}

test('editor header shows a promoted-from banner when the latest version is a promotion', async () => {
  seedSecrets()
  server.use(http.get('/v1/configs/c1/versions', () => HttpResponse.json({ versions: [
    { version: 3, message: 'edit', created_by: '', created_at: '' },
    { version: 4, message: 'promote staging', created_by: '', created_at: '',
      promoted_from_env: 'staging', promoted_from_version: 3 },
  ] })))
  render()
  await screen.findByText('DB_URL')
  expect(await screen.findByText(/v4 promoted from staging v3/i)).toBeInTheDocument()
})

test('editor header shows no promoted-from banner when the latest version is not a promotion', async () => {
  seedSecrets()
  server.use(http.get('/v1/configs/c1/versions', () => HttpResponse.json({ versions: [
    // provenance on an OLDER version only — latest (v4) is a plain edit.
    { version: 3, message: 'promote staging', created_by: '', created_at: '',
      promoted_from_env: 'staging', promoted_from_version: 2 },
    { version: 4, message: 'edit', created_by: '', created_at: '' },
  ] })))
  render()
  await screen.findByText('DB_URL')
  expect(screen.queryByText(/promoted from/i)).toBeNull()
})
