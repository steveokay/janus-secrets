import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { Playground } from './Playground'

const AES = { name: 'app', type: 'aes256-gcm', latest_version: 2, min_decryption_version: 1, deletion_allowed: false, versions: [1, 2] } as const
const ED = { name: 'signer', type: 'ed25519', latest_version: 1, min_decryption_version: 1, deletion_allowed: false, versions: [1] } as const

test('encrypt base64-encodes the text input and shows ciphertext', async () => {
  let sent: { plaintext: string } | undefined
  server.use(http.post('/v1/transit/encrypt/app', async ({ request }) => {
    sent = (await request.json()) as { plaintext: string }
    return HttpResponse.json({ ciphertext: 'janus:v2:Zm9v' })
  }))
  renderApp(<Playground keyMeta={AES} />, { route: '/transit', withAuth: false })
  await userEvent.type(screen.getByLabelText(/plaintext|text to encrypt/i), 'hello')
  await userEvent.click(screen.getByRole('button', { name: /encrypt/i }))
  expect(await screen.findByText('janus:v2:Zm9v')).toBeInTheDocument()
  expect(sent!.plaintext).toBe('aGVsbG8=') // base64('hello')
})

test('verify shows a valid/invalid badge without treating a bad sig as an error', async () => {
  server.use(http.post('/v1/transit/verify/signer', () => HttpResponse.json({ valid: false })))
  renderApp(<Playground keyMeta={ED} />, { route: '/transit', withAuth: false })
  await userEvent.type(screen.getByLabelText(/message|input/i), 'hi')
  await userEvent.type(screen.getByLabelText(/signature/i), 'janus:v1:AAAA')
  await userEvent.click(screen.getByRole('button', { name: /verify/i }))
  expect(await screen.findByText(/invalid/i)).toBeInTheDocument()
})

test('a 403 on a crypto op surfaces a guardrail message', async () => {
  server.use(http.post('/v1/transit/encrypt/app', () =>
    HttpResponse.json({ error: { code: 'forbidden', message: 'forbidden' } }, { status: 403 })))
  renderApp(<Playground keyMeta={AES} />, { route: '/transit', withAuth: false })
  await userEvent.type(screen.getByLabelText(/plaintext|text to encrypt/i), 'x')
  await userEvent.click(screen.getByRole('button', { name: /encrypt/i }))
  expect(await screen.findByRole('alert')).toBeInTheDocument()
})
