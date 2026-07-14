import { vi } from 'vitest'
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
    // Mount now loads the config version from the value-free versions list, not
    // a reveal — every test needs this handler present.
    http.get('/v1/configs/c1/versions', () => HttpResponse.json({ versions: [
      { version: 3, message: '', created_by: '', created_at: '' },
    ] })),
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

test('mount reveals nothing — no reveal request, all values masked', async () => {
  seed()
  let revealHits = 0
  server.use(http.get('/v1/configs/c1/secrets/:key', () => { revealHits++; return HttpResponse.json({ key: 'x', value: 'y' }) }))
  server.use(http.get('/v1/configs/c1/secrets', ({ request }) => {
    const u = new URL(request.url)
    if (u.searchParams.get('reveal') === 'true') { revealHits++; return HttpResponse.json({ version: 3, secrets: {} }) }
    return HttpResponse.json({ secrets: { DB_URL: { value_version: 3, created_at: '', origin: 'own' } } })
  }))
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  expect(screen.getByText('••••••••••••')).toBeInTheDocument()
  expect(revealHits).toBe(0) // nothing revealed on mount
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

test('clicking the eye fetches that one key raw and unmasks only it', async () => {
  seed()
  server.use(http.get('/v1/configs/c1/secrets/DB_URL', ({ request }) => {
    expect(new URL(request.url).searchParams.get('raw')).toBe('true')
    return HttpResponse.json({ key: 'DB_URL', value: 'postgres://a' })
  }))
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.click(screen.getByRole('button', { name: /reveal db_url/i }))
  expect(await screen.findByText('postgres://a')).toBeInTheDocument()
})

test('editing a masked key fetches its raw original; same value is not dirty', async () => {
  seed()
  server.use(http.get('/v1/configs/c1/secrets/DB_URL', () => HttpResponse.json({ key: 'DB_URL', value: 'postgres://a' })))
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.click(screen.getByRole('button', { name: /edit db_url/i }))
  const input = await screen.findByRole('textbox', { name: /value for db_url/i })
  expect(input).toHaveValue('postgres://a') // prefilled from the fetched raw original
  // typing the same value back is a no-op (not dirty) — no dirty bar
  expect(screen.queryByRole('button', { name: /save as v/i })).toBeNull()
})

test('empty config shows the empty state', async () => {
  server.use(
    http.get('/v1/configs/cEmpty/secrets', ({ request }) => {
      if (new URL(request.url).searchParams.get('reveal') === 'true')
        return HttpResponse.json({ version: 0, secrets: {} })
      return HttpResponse.json({ secrets: {} })
    }),
    http.get('/v1/configs/cEmpty/versions', () => HttpResponse.json({ versions: [] })),
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
  server.use(http.get('/v1/configs/c1/secrets/DB_URL', () => HttpResponse.json({ key: 'DB_URL', value: 'postgres://a' })))
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.click(screen.getByRole('button', { name: /edit db_url/i }))
  expect(await screen.findByRole('textbox', { name: /value for db_url/i })).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /cancel edit db_url/i }))
  expect(screen.queryByRole('textbox', { name: /value for db_url/i })).toBeNull()
})

test('pressing Escape in an edit field cancels the edit', async () => {
  seed()
  server.use(http.get('/v1/configs/c1/secrets/DB_URL', () => HttpResponse.json({ key: 'DB_URL', value: 'postgres://a' })))
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.click(screen.getByRole('button', { name: /edit db_url/i }))
  const input = await screen.findByRole('textbox', { name: /value for db_url/i })
  await userEvent.type(input, '{Escape}')
  expect(screen.queryByRole('textbox', { name: /value for db_url/i })).toBeNull()
})

test('the secret table is wrapped in a horizontal-scroll container', async () => {
  seed()
  const { container } = renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  expect(container.querySelector('.overflow-x-auto')).not.toBeNull()
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

test('Reveal all reveals every row via one bulk request, and Hide all re-masks', async () => {
  seed()
  let bulk = 0
  server.use(http.get('/v1/configs/c1/secrets', ({ request }) => {
    const params = new URL(request.url).searchParams
    if (params.get('reveal') === 'true' && params.get('raw') === 'true') {
      bulk++
      return HttpResponse.json({ version: 3, secrets: { DB_URL: 'postgres://a', SENTRY_DSN: 'https://x' } })
    }
    return HttpResponse.json({ secrets: {
      DB_URL: { value_version: 3, created_at: '', origin: 'own' },
      SENTRY_DSN: { value_version: 1, created_at: '', origin: 'inherited' },
    } })
  }))
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.click(screen.getByRole('button', { name: /reveal all/i }))
  expect(await screen.findByText('postgres://a')).toBeInTheDocument()
  expect(bulk).toBe(1)
  await userEvent.click(screen.getByRole('button', { name: /hide all/i }))
  expect(screen.queryByText('postgres://a')).toBeNull()
})

test('window blur re-masks revealed values', async () => {
  seed()
  server.use(http.get('/v1/configs/c1/secrets', ({ request }) => {
    const params = new URL(request.url).searchParams
    if (params.get('reveal') === 'true' && params.get('raw') === 'true')
      return HttpResponse.json({ version: 3, secrets: { DB_URL: 'postgres://a' } })
    return HttpResponse.json({ secrets: { DB_URL: { value_version: 3, created_at: '', origin: 'own' } } })
  }))
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.click(screen.getByRole('button', { name: /reveal all/i }))
  await screen.findByText('postgres://a')
  window.dispatchEvent(new Event('blur'))
  await waitFor(() => expect(screen.queryByText('postgres://a')).toBeNull())
})

test('reveal-all fetches once (bulk raw); a pending edit survives the auto-re-mask on blur', async () => {
  seed()
  let bulk = 0
  server.use(http.get('/v1/configs/c1/secrets', ({ request }) => {
    const params = new URL(request.url).searchParams
    if (params.get('reveal') === 'true' && params.get('raw') === 'true') {
      bulk++
      return HttpResponse.json({ version: 3, secrets: { DB_URL: 'postgres://a' } })
    }
    return HttpResponse.json({ secrets: { DB_URL: { value_version: 3, created_at: '', origin: 'own' } } })
  }))
  server.use(http.get('/v1/configs/c1/secrets/DB_URL', () => HttpResponse.json({ key: 'DB_URL', value: 'postgres://a' })))
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  // make a pending edit first
  await userEvent.click(screen.getByRole('button', { name: /edit db_url/i }))
  const input = await screen.findByRole('textbox', { name: /value for db_url/i })
  await userEvent.clear(input); await userEvent.type(input, 'postgres://B')
  await screen.findByRole('button', { name: /save as v4/i }) // dirty; version 3 + 1
  // reveal all (bulk, once)
  await userEvent.click(screen.getByRole('button', { name: /reveal all/i }))
  expect(bulk).toBe(1)
  window.dispatchEvent(new Event('blur'))
  // pending edit survives blur — still dirty (original was preserved)
  await waitFor(() => expect(screen.getByRole('button', { name: /save as v4/i })).toBeInTheDocument())
})

test('a failed clipboard write surfaces a danger toast', async () => {
  seed()
  server.use(http.get('/v1/configs/c1/secrets/DB_URL', () => HttpResponse.json({ key: 'DB_URL', value: 'postgres://a' })))
  const orig = navigator.clipboard
  Object.defineProperty(navigator, 'clipboard', {
    value: { writeText: () => Promise.reject(new Error('denied')) },
    configurable: true,
  })
  try {
    renderApp(<ToastProvider><SecretEditor /></ToastProvider>, { route: '/projects/p1/configs/c1', withAuth: false })
    await screen.findByText('DB_URL')
    await userEvent.click(screen.getByRole('button', { name: /copy db_url/i }))
    expect(await screen.findByText('Copy failed')).toBeInTheDocument()
  } finally {
    Object.defineProperty(navigator, 'clipboard', { value: orig, configurable: true })
  }
})

test('the empty state offers an Add secret CTA that focuses the new-key input', async () => {
  server.use(
    http.get('/v1/configs/cEmpty/secrets', ({ request }) => {
      if (new URL(request.url).searchParams.get('reveal') === 'true')
        return HttpResponse.json({ version: 0, secrets: {} })
      return HttpResponse.json({ secrets: {} })
    }),
    http.get('/v1/configs/cEmpty/versions', () => HttpResponse.json({ versions: [] })),
  )
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/cEmpty', withAuth: false })
  await screen.findByText('No secrets yet')
  await userEvent.click(screen.getByRole('button', { name: /add secret/i }))
  expect(document.activeElement).toBe(screen.getByLabelText('new key'))
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

test('selecting a row shows the selection bar and bulk delete stages a removal', async () => {
  seed()
  server.use(http.get('/v1/configs/c1/secrets/DB_URL', () => HttpResponse.json({ key: 'DB_URL', value: 'postgres://a' })))
  renderApp(<ToastProvider><SecretEditor /></ToastProvider>, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.click(screen.getByRole('checkbox', { name: /select db_url/i }))
  expect(screen.getByText(/1 selected/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /^delete$/i }))
  expect(await screen.findByText(/deleted 1/i)).toBeInTheDocument()
})

test('bulk reveal fires one audited reveal per selected existing key', async () => {
  seed()
  const hits: string[] = []
  server.use(http.get('/v1/configs/c1/secrets/:key', ({ params }) => {
    hits.push(String(params.key)); return HttpResponse.json({ key: String(params.key), value: 'v' })
  }))
  renderApp(<ToastProvider><SecretEditor /></ToastProvider>, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.click(screen.getByRole('checkbox', { name: /select db_url/i }))
  await userEvent.click(screen.getByRole('button', { name: /^reveal$/i }))
  await waitFor(() => expect(hits).toEqual(['DB_URL']))
})

test('bulk copy reveals then writes .env text to clipboard', async () => {
  seed()
  server.use(http.get('/v1/configs/c1/secrets/DB_URL', () => HttpResponse.json({ key: 'DB_URL', value: 'postgres://a' })))
  const writeText = vi.fn().mockResolvedValue(undefined)
  const orig = navigator.clipboard
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
  try {
    renderApp(<ToastProvider><SecretEditor /></ToastProvider>, { route: '/projects/p1/configs/c1', withAuth: false })
    await screen.findByText('DB_URL')
    await userEvent.click(screen.getByRole('checkbox', { name: /select db_url/i }))
    await userEvent.click(screen.getByRole('button', { name: /copy \.env/i }))
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(expect.stringContaining('DB_URL=postgres://a')))
  } finally {
    Object.defineProperty(navigator, 'clipboard', { value: orig, configurable: true })
  }
})

test('bulk download shows a confirm, then reveals (audited) on confirm', async () => {
  seed()
  const hits: string[] = []
  server.use(http.get('/v1/configs/c1/secrets/DB_URL', () => {
    hits.push('DB_URL'); return HttpResponse.json({ key: 'DB_URL', value: 'postgres://a' })
  }))
  // jsdom lacks object-URL APIs — stub so the download path doesn't throw.
  Object.assign(URL, { createObjectURL: vi.fn(() => 'blob:x'), revokeObjectURL: vi.fn() })
  renderApp(<ToastProvider><SecretEditor /></ToastProvider>, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.click(screen.getByRole('checkbox', { name: /select db_url/i }))
  await userEvent.click(screen.getByRole('button', { name: /download \.env/i }))
  // ConfirmDialog appears; no reveal yet
  expect(await screen.findByText('Download secrets as .env?')).toBeInTheDocument()
  expect(hits).toEqual([])
  await userEvent.click(screen.getByRole('button', { name: /^download$/i }))
  await waitFor(() => expect(hits).toEqual(['DB_URL']))
  await waitFor(() => expect(URL.revokeObjectURL).toHaveBeenCalled())
})

test('keyboard nav: ArrowDown+e edits a row; / focuses the filter', async () => {
  seed()
  server.use(http.get('/v1/configs/c1/secrets/DB_URL', () => HttpResponse.json({ key: 'DB_URL', value: 'postgres://a' })))
  renderApp(<ToastProvider><SecretEditor /></ToastProvider>, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  // ensure no input is focused so the window keydown nav is live
  ;(document.activeElement as HTMLElement | null)?.blur?.()
  document.body.focus()
  await userEvent.keyboard('{ArrowDown}e')
  expect(await screen.findByRole('textbox', { name: /value for db_url/i })).toBeInTheDocument()
  // Escape out of the edit field first (focus returns to body), then '/'
  await userEvent.keyboard('{Escape}')
  ;(document.activeElement as HTMLElement | null)?.blur?.()
  document.body.focus()
  await userEvent.keyboard('/')
  expect(document.activeElement).toBe(screen.getByRole('searchbox', { name: /filter keys/i }))
})
