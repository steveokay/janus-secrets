import { http, HttpResponse } from 'msw'
import { server } from '../test/msw'
import { api, ApiError } from './api'

test('GET returns parsed JSON', async () => {
  // Generic client passthrough test; body mirrors the REAL /v1/auth/me shape
  // (never mock invented shapes on real endpoints — see fe-improvements.md).
  server.use(http.get('/v1/auth/me', () => HttpResponse.json({ kind: 'user', id: 'u1', name: 'a@b.io' })))
  await expect(api.get('/v1/auth/me')).resolves.toEqual({ kind: 'user', id: 'u1', name: 'a@b.io' })
})

test('error envelope becomes a typed ApiError', async () => {
  server.use(
    http.get('/v1/projects', () =>
      HttpResponse.json({ error: { code: 'forbidden', message: 'nope' } }, { status: 403 }),
    ),
  )
  await expect(api.get('/v1/projects')).rejects.toMatchObject({
    name: 'ApiError',
    status: 403,
    code: 'forbidden',
  } satisfies Partial<ApiError>)
})

test('POST sends JSON body and credentials', async () => {
  let sawBody: unknown
  let sawCreds: string | null = null
  server.use(
    http.post('/v1/projects', async ({ request }) => {
      sawBody = await request.json()
      sawCreds = request.credentials
      return HttpResponse.json({ id: 'p1' }, { status: 200 })
    }),
  )
  await api.post('/v1/projects', { slug: 'x', name: 'X' })
  expect(sawBody).toEqual({ slug: 'x', name: 'X' })
  expect(sawCreds).toBe('include')
})
