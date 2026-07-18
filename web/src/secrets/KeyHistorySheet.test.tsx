import { useState } from 'react'
import { http, HttpResponse } from 'msw'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ToastProvider } from '../ui/Toast'
import { KeyHistorySheet } from './KeyHistorySheet'

function mountSheet() {
  server.use(
    http.get('/v1/configs/:cid/secrets/:key/history', () =>
      HttpResponse.json({ key: 'API_KEY', history: [{ value_version: 2, created_at: 'b' }, { value_version: 1, created_at: 'a' }] }),
    ),
  )
  return renderApp(
    <ToastProvider><KeyHistorySheet cid="c1" secretKey="API_KEY" open onOpenChange={() => {}} /></ToastProvider>,
    { route: '/projects/p1/configs/c1', withAuth: false },
  )
}

test('lists versions newest-first, value-free by default', async () => {
  mountSheet()
  expect(await screen.findByText('v2')).toBeInTheDocument()
  expect(screen.getByText('v1')).toBeInTheDocument()
  expect(screen.queryByText('old-secret')).not.toBeInTheDocument()
})

test('revealing a version calls the audited versioned reveal and shows plaintext', async () => {
  let revealedVersion = ''
  server.use(http.get('/v1/configs/:cid/secrets/:key', ({ request }) => {
    revealedVersion = new URL(request.url).searchParams.get('version') ?? ''
    return HttpResponse.json({ key: 'API_KEY', value: 'old-secret', value_version: 1 })
  }))
  mountSheet()
  await userEvent.click(await screen.findByRole('button', { name: /reveal v1/i }))
  expect(await screen.findByText('old-secret')).toBeInTheDocument()
  expect(revealedVersion).toBe('1')
})

test('copying a revealed version briefly shows an inline copied state', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
  server.use(http.get('/v1/configs/:cid/secrets/:key', () =>
    HttpResponse.json({ key: 'API_KEY', value: 'old-secret', value_version: 1 })))
  mountSheet()
  await user.click(await screen.findByRole('button', { name: /reveal v1/i }))
  await screen.findByText('old-secret')
  await user.click(screen.getByRole('button', { name: /copy v1/i }))
  expect(await screen.findByRole('button', { name: 'v1 copied' })).toBeInTheDocument()
  await act(async () => { await vi.advanceTimersByTimeAsync(1200) })
  expect(screen.getByRole('button', { name: /copy v1/i })).toBeInTheDocument()
  vi.useRealTimers()
})

// A controlled harness so the test can close and reopen the Sheet.
function ControlledHarness() {
  const [open, setOpen] = useState(true)
  return (
    <ToastProvider>
      <button onClick={() => setOpen((o) => !o)}>toggle</button>
      <KeyHistorySheet cid="c1" secretKey="API_KEY" open={open} onOpenChange={setOpen} />
    </ToastProvider>
  )
}

test('closing the sheet clears revealed plaintext (no residual in state/cache)', async () => {
  server.use(
    http.get('/v1/configs/:cid/secrets/:key/history', () =>
      HttpResponse.json({ key: 'API_KEY', history: [{ value_version: 1, created_at: 'a' }] }),
    ),
    http.get('/v1/configs/:cid/secrets/:key', () =>
      HttpResponse.json({ key: 'API_KEY', value: 'old-secret', value_version: 1 }),
    ),
  )
  renderApp(<ControlledHarness />, { route: '/projects/p1/configs/c1', withAuth: false })
  // Reveal the historical value.
  await userEvent.click(await screen.findByRole('button', { name: /reveal v1/i }))
  expect(await screen.findByText('old-secret')).toBeInTheDocument()
  // Close the Sheet via its own close control (the modal aria-hides the outer
  // toggle while open). Radix unmounts children, dropping local reveal state.
  await userEvent.click(screen.getByRole('button', { name: /close/i }))
  await waitFor(() => expect(screen.queryByText('old-secret')).not.toBeInTheDocument())
  // Reopen (toggle is accessible again now the dialog is closed): the value-free
  // history list returns, but the plaintext must NOT reappear without an
  // explicit re-reveal (state was reset by the remount).
  await userEvent.click(screen.getByRole('button', { name: 'toggle' }))
  expect(await screen.findByText('v1')).toBeInTheDocument()
  expect(screen.queryByText('old-secret')).not.toBeInTheDocument()
})
