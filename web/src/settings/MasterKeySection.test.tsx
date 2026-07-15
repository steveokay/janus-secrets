import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
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
