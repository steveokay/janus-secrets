import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ToastProvider } from '../ui/Toast'
import { MasterKeySection } from './MasterKeySection'

function mount() {
  return renderApp(
    <ToastProvider><MasterKeySection /></ToastProvider>,
    { route: '/settings?section=instance', withAuth: false },
  )
}

// Wire shape mirrors the Go handler for GET /v1/sys/master-key (snake_case).
const AWSKMS = {
  unseal_type: 'awskms',
  master_key_version: 3,
  rotated_at: '2026-07-15T00:00:00Z',
  rekey_in_progress: false,
  submitted: 0,
  required: 0,
}

const SHAMIR = {
  unseal_type: 'shamir',
  master_key_version: 2,
  rotated_at: null,
  rekey_in_progress: false,
  submitted: 0,
  required: 0,
}

test('awskms: renders the master-key version and a rotate button', async () => {
  server.use(http.get('/v1/sys/master-key', () => HttpResponse.json(AWSKMS)))
  mount()
  expect(await screen.findByText(/version 3/i)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /rotate master key/i })).toBeInTheDocument()
})

test('shamir: still renders the version and a rotate button', async () => {
  server.use(http.get('/v1/sys/master-key', () => HttpResponse.json(SHAMIR)))
  mount()
  expect(await screen.findByText(/version 2/i)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /rotate master key/i })).toBeInTheDocument()
})

test('403 renders an owner-only note and no rotate button', async () => {
  server.use(
    http.get('/v1/sys/master-key', () =>
      HttpResponse.json({ error: { code: 'forbidden', message: 'nope' } }, { status: 403 })),
  )
  mount()
  expect(await screen.findByText(/owner/i)).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /rotate master key/i })).not.toBeInTheDocument()
})

test('awskms: rotate flows through a danger confirm → toast + refetch', async () => {
  const user = userEvent.setup()
  // Status flips to version 4 on refetch after the rotate lands.
  let version = 3
  server.use(
    http.get('/v1/sys/master-key', () =>
      HttpResponse.json({ ...AWSKMS, master_key_version: version })),
    http.post('/v1/sys/master-key/rotate', () => {
      version = 4
      return HttpResponse.json({ master_key_version: 4 })
    }),
  )
  mount()
  await user.click(await screen.findByRole('button', { name: /rotate master key/i }))
  // Danger confirm dialog appears; confirm it.
  const confirm = await screen.findByRole('button', { name: /^rotate$/i })
  await user.click(confirm)
  // Success toast names the new version; status query refetches to version 4.
  expect(await screen.findByText(/rotated.*v4/i)).toBeInTheDocument()
  expect(await screen.findByText(/version 4/i)).toBeInTheDocument()
})

test('shamir: rotate runs init → submit ×3 → shows the new shares once', async () => {
  const user = userEvent.setup()
  let submitted = 0
  server.use(
    http.get('/v1/sys/master-key', () => HttpResponse.json(SHAMIR)),
    http.post('/v1/sys/master-key/rekey/init', () =>
      HttpResponse.json({ nonce: 'n1', required: 3, submitted: 0 })),
    http.post('/v1/sys/master-key/rekey/submit', () => {
      submitted += 1
      if (submitted < 3) {
        return HttpResponse.json({ complete: false, submitted, required: 3 })
      }
      return HttpResponse.json({
        complete: true,
        master_key_version: 4,
        new_shares: ['share-alpha', 'share-beta', 'share-gamma'],
      })
    }),
  )
  mount()
  await user.click(await screen.findByRole('button', { name: /rotate master key/i }))
  await user.click(await screen.findByRole('button', { name: /^rotate$/i }))

  // Share-submission modal opens (init has run: required 3).
  const shareField = await screen.findByLabelText(/key share/i)
  const addBtn = screen.getByRole('button', { name: /submit share/i })

  for (const s of ['old-1', 'old-2', 'old-3']) {
    await user.clear(shareField)
    await user.type(shareField, s)
    await user.click(addBtn)
  }

  // New shares render once, inside the RevealOnce surface. They are joined into
  // one "shown once" block, so match on the element carrying all three.
  // The three shares are joined into one leaf block; match the innermost element
  // (no element children) whose text contains the needle to avoid ancestor dupes.
  const hasShare = (needle: string) => (_: string, el: Element | null) =>
    el?.tagName === 'DIV' && el.querySelector('*') === null && (el.textContent ?? '').includes(needle)
  expect(await screen.findByText(hasShare('share-alpha'))).toBeInTheDocument()
  expect(screen.getByText(hasShare('share-beta'))).toBeInTheDocument()
  expect(screen.getByText(hasShare('share-gamma'))).toBeInTheDocument()

  // Closing the reveal removes the shares from the DOM.
  await user.click(screen.getByRole('button', { name: /stored/i }))
  await waitFor(() => {
    expect(screen.queryByText(hasShare('share-alpha'))).not.toBeInTheDocument()
  })
})

test('shamir: a rejected share shows an inline error and no shares', async () => {
  const user = userEvent.setup()
  server.use(
    http.get('/v1/sys/master-key', () => HttpResponse.json(SHAMIR)),
    http.post('/v1/sys/master-key/rekey/init', () =>
      HttpResponse.json({ nonce: 'n1', required: 3, submitted: 0 })),
    http.post('/v1/sys/master-key/rekey/submit', () =>
      HttpResponse.json({ error: { code: 'validation', message: 'bad share' } }, { status: 400 })),
  )
  mount()
  await user.click(await screen.findByRole('button', { name: /rotate master key/i }))
  await user.click(await screen.findByRole('button', { name: /^rotate$/i }))

  const shareField = await screen.findByLabelText(/key share/i)
  await user.type(shareField, 'wrong-share')
  await user.click(screen.getByRole('button', { name: /submit share/i }))

  // Inline error inside the modal; no shares are ever shown.
  expect(await screen.findByRole('alert')).toBeInTheDocument()
  expect(screen.queryByText('share-alpha')).not.toBeInTheDocument()
})
