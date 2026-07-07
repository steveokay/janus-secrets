import { http, HttpResponse } from 'msw'
import { server } from '../test/msw'
import { endpoints } from './endpoints'

test('listTransitKeys unwraps {keys}', async () => {
  server.use(http.get('/v1/transit/keys', () =>
    HttpResponse.json({ keys: [
      { name: 'app', type: 'aes256-gcm', latest_version: 2, min_decryption_version: 1, deletion_allowed: false, versions: [1, 2] },
    ] })))
  const keys = await endpoints.listTransitKeys()
  expect(keys).toHaveLength(1)
  expect(keys[0]).toMatchObject({ name: 'app', type: 'aes256-gcm', latest_version: 2, versions: [1, 2] })
})

test('createTransitKey posts name + type', async () => {
  let body: unknown
  server.use(http.post('/v1/transit/keys', async ({ request }) => {
    body = await request.json()
    return HttpResponse.json({ name: 'k', type: 'ed25519', latest_version: 1, min_decryption_version: 1, deletion_allowed: false, versions: [1] }, { status: 201 })
  }))
  await endpoints.createTransitKey('k', 'ed25519')
  expect(body).toEqual({ name: 'k', type: 'ed25519' })
})

test('transitEncrypt posts base64 plaintext and returns ciphertext', async () => {
  server.use(http.post('/v1/transit/encrypt/app', async ({ request }) => {
    const b = (await request.json()) as { plaintext: string }
    expect(b.plaintext).toBe('aGVsbG8=')
    return HttpResponse.json({ ciphertext: 'janus:v2:Zm9v' })
  }))
  const r = await endpoints.transitEncrypt('app', 'aGVsbG8=')
  expect(r.ciphertext).toBe('janus:v2:Zm9v')
})
