import { http, HttpResponse } from 'msw'
import { server } from '../test/msw'
import { endpoints } from './endpoints'

test('sealStatus parses the shamir shape', async () => {
  server.use(
    http.get('/v1/sys/seal-status', () =>
      HttpResponse.json({ initialized: true, sealed: true, type: 'shamir', threshold: 3, shares: 5, progress: { submitted: 1, required: 3 } }),
    ),
  )
  await expect(endpoints.sealStatus()).resolves.toMatchObject({ sealed: true, threshold: 3, progress: { submitted: 1, required: 3 } })
})

test('listProjects unwraps the projects array', async () => {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 's', name: 'N' }] })),
  )
  await expect(endpoints.listProjects()).resolves.toEqual([{ id: 'p1', slug: 's', name: 'N' }])
})

test('saveSecrets posts the batch and returns the version', async () => {
  let sawBody: any
  server.use(
    http.put('/v1/configs/c1/secrets', async ({ request }) => {
      sawBody = await request.json()
      return HttpResponse.json({ version: 4, id: 'v4', created_at: '2026-07-06T00:00:00Z' })
    }),
  )
  const changes = [{ key: 'A', value: '1' }, { key: 'B', delete: true }]
  await expect(endpoints.saveSecrets('c1', changes, 'msg')).resolves.toMatchObject({ version: 4 })
  expect(sawBody).toEqual({ message: 'msg', changes })
})

test('listVersions unwraps versions with real shape', async () => {
  server.use(http.get('/v1/configs/c1/versions', () =>
    HttpResponse.json({ versions: [
      { version: 2, message: 'rotate keys', created_by: 'steve@acme.dev', created_at: '2026-07-06T10:00:00Z' },
      { version: 1, message: '', created_by: 'steve@acme.dev', created_at: '2026-07-05T10:00:00Z' },
    ] }),
  ))
  await expect(endpoints.listVersions('c1')).resolves.toHaveLength(2)
})

test('diffVersions returns key-name arrays', async () => {
  server.use(http.get('/v1/configs/c1/versions/diff', () =>
    HttpResponse.json({ a: 1, b: 2, added: ['NEW_KEY'], changed: ['DB_URL'], removed: [] }),
  ))
  await expect(endpoints.diffVersions('c1', 1, 2)).resolves.toMatchObject({ a: 1, b: 2, added: ['NEW_KEY'] })
})

test('rollback posts target_version and message', async () => {
  let body: unknown
  server.use(http.post('/v1/configs/c1/rollback', async ({ request }) => {
    body = await request.json()
    return HttpResponse.json({ version: 3, id: 'cv3', created_at: '2026-07-06T11:00:00Z' })
  }))
  await expect(endpoints.rollback('c1', 1, 'Rollback to v1')).resolves.toMatchObject({ version: 3 })
  expect(body).toEqual({ target_version: 1, message: 'Rollback to v1' })
})
