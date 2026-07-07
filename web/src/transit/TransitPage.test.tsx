import { http, HttpResponse } from 'msw'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { TransitPage } from './TransitPage'

const KEYS = [
  { name: 'app', type: 'aes256-gcm', latest_version: 3, min_decryption_version: 2, deletion_allowed: false, versions: [1, 2, 3] },
  { name: 'signer', type: 'ed25519', latest_version: 1, min_decryption_version: 1, deletion_allowed: true, versions: [1] },
]
function mockKeys(keys = KEYS) {
  server.use(http.get('/v1/transit/keys', () => HttpResponse.json({ keys })))
}

test('lists keys with type, version and min-decryption cues', async () => {
  mockKeys()
  renderApp(<TransitPage />, { route: '/transit', withAuth: false })
  const app = await screen.findByText('app')
  const row = app.closest('[data-key-row]') as HTMLElement
  expect(within(row).getByText('aes256-gcm')).toBeInTheDocument()
  expect(within(row).getByText(/v3/)).toBeInTheDocument()
  expect(within(row).getByText(/min.*2/i)).toBeInTheDocument()
  expect(screen.getByText('signer')).toBeInTheDocument()
  expect(screen.getByText('ed25519')).toBeInTheDocument()
})

test('empty state offers to create a key', async () => {
  mockKeys([])
  renderApp(<TransitPage />, { route: '/transit', withAuth: false })
  expect(await screen.findByText(/no transit keys/i)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /create.*key|new key/i })).toBeInTheDocument()
})

test('create-key modal posts name + type and refreshes', async () => {
  mockKeys([])
  let created: unknown
  server.use(http.post('/v1/transit/keys', async ({ request }) => {
    created = await request.json()
    return HttpResponse.json({ name: 'newkey', type: 'aes256-gcm', latest_version: 1, min_decryption_version: 1, deletion_allowed: false, versions: [1] }, { status: 201 })
  }))
  renderApp(<TransitPage />, { route: '/transit', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /create.*key|new key/i }))
  await userEvent.type(screen.getByLabelText(/name/i), 'newkey')
  await userEvent.click(screen.getByRole('button', { name: /^create/i }))
  expect(created).toEqual({ name: 'newkey', type: 'aes256-gcm' })
})

test('duplicate name surfaces the 409 conflict', async () => {
  mockKeys([])
  server.use(http.post('/v1/transit/keys', () =>
    HttpResponse.json({ error: { code: 'conflict', message: 'conflict' } }, { status: 409 })))
  renderApp(<TransitPage />, { route: '/transit', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /create.*key|new key/i }))
  await userEvent.type(screen.getByLabelText(/name/i), 'app')
  await userEvent.click(screen.getByRole('button', { name: /^create/i }))
  expect(await screen.findByRole('alert')).toHaveTextContent(/conflict/i)
})
