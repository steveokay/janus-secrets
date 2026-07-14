import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test } from 'vitest'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ToastProvider } from '../ui/Toast'
import { SecretEditor } from './SecretEditor'

// Masked list + versions + locked-keys (DB_URL locked, SENTRY_DSN not).
function seed() {
  server.use(
    http.get('/v1/configs/c1/secrets', ({ request }) => {
      if (new URL(request.url).searchParams.get('reveal') === 'true')
        return HttpResponse.json({ version: 3, secrets: { DB_URL: 'postgres://a' } })
      return HttpResponse.json({
        secrets: {
          DB_URL: { value_version: 3, created_at: '', origin: 'own' },
          SENTRY_DSN: { value_version: 1, created_at: '', origin: 'own' },
        },
      })
    }),
    http.get('/v1/configs/c1/versions', () =>
      HttpResponse.json({ versions: [{ version: 3, message: '', created_by: '', created_at: '' }] }),
    ),
    http.get('/v1/configs/c1/locked-keys', () => HttpResponse.json({ keys: ['DB_URL'] })),
  )
}

test('a promotion-locked key shows a lock glyph', async () => {
  seed()
  renderApp(<ToastProvider><SecretEditor /></ToastProvider>, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  // The locked key surfaces a lock indicator with an accessible label.
  expect(await screen.findByLabelText(/db_url is promotion-locked/i)).toBeInTheDocument()
})

test('clicking the lock toggle on an unlocked key locks it and toasts', async () => {
  seed()
  let lockedKey: string | null = null
  server.use(
    http.post('/v1/configs/c1/locked-keys', async ({ request }) => {
      const body = (await request.json()) as { key: string }
      lockedKey = body.key
      return HttpResponse.json({ key: body.key, locked: true })
    }),
  )
  renderApp(<ToastProvider><SecretEditor /></ToastProvider>, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('SENTRY_DSN')
  await userEvent.click(screen.getByRole('button', { name: /^lock sentry_dsn$/i }))
  await waitFor(() => expect(lockedKey).toBe('SENTRY_DSN'))
  expect(await screen.findByText(/locked sentry_dsn/i)).toBeInTheDocument()
})

test('clicking the lock toggle on a locked key unlocks it and toasts', async () => {
  seed()
  let unlockedKey: string | null = null
  server.use(
    http.delete('/v1/configs/c1/locked-keys/:key', ({ params }) => {
      unlockedKey = String(params.key)
      return HttpResponse.json({ key: String(params.key), locked: false })
    }),
  )
  renderApp(<ToastProvider><SecretEditor /></ToastProvider>, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.click(screen.getByRole('button', { name: /^unlock db_url$/i }))
  await waitFor(() => expect(unlockedKey).toBe('DB_URL'))
  expect(await screen.findByText(/unlocked db_url/i)).toBeInTheDocument()
})
