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
