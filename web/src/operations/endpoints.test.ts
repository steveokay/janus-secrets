import { http, HttpResponse } from 'msw'
import { server } from '../test/msw'
import { opsEndpoints } from './endpoints'

test('rotation.list unwraps {policies}', async () => {
  server.use(
    http.get('/v1/rotation/policies', ({ request }) => {
      expect(new URL(request.url).searchParams.get('project_id')).toBe('p1')
      return HttpResponse.json({ policies: [{ id: 'r1', config_id: 'c1', secret_key: 'DB', type: 'postgres', status: 'active' }] })
    }),
  )
  const rows = await opsEndpoints.rotation.list('p1')
  expect(rows).toHaveLength(1)
  expect(rows[0].id).toBe('r1')
})

test('sync.list unwraps {targets}; dynamic.listLeases unwraps {leases}', async () => {
  server.use(http.get('/v1/sync/targets', () => HttpResponse.json({ targets: [{ id: 's1' }] })))
  server.use(http.get('/v1/dynamic/leases', () => HttpResponse.json({ leases: [{ id: 'l1' }] })))
  expect(await opsEndpoints.sync.list('p1')).toHaveLength(1)
  expect(await opsEndpoints.dynamic.listLeases('r1')).toHaveLength(1)
})

test('rotation.setInterval issues a PATCH with interval_seconds', async () => {
  let method = '', body: any
  server.use(
    http.patch('/v1/rotation/policies/r1', async ({ request }) => {
      method = request.method
      body = await request.json()
      return HttpResponse.json({ id: 'r1', interval_seconds: 900 })
    }),
  )
  await opsEndpoints.rotation.setInterval('r1', 900)
  expect(method).toBe('PATCH')
  expect(body).toEqual({ interval_seconds: 900 })
})
