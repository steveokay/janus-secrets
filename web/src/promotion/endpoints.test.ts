import { http, HttpResponse } from 'msw'
import { server } from '../test/msw'
import { promotion } from './endpoints'

test('preview GETs /v1/promote/preview and returns diff entries with status', async () => {
  const diff = await promotion.preview('c-src', 'c-dst')
  expect(diff.source_version).toBe(3)
  expect(diff.target_exists).toBe(true)
  expect(diff.entries.length).toBeGreaterThan(0)
  const statuses = diff.entries.map((e) => e.status)
  expect(statuses).toContain('add')
  expect(diff.entries.some((e) => e.locked)).toBe(true)
})

test('preview passes from/to as query params', async () => {
  let from = '', to = ''
  server.use(
    http.get('/v1/promote/preview', ({ request }) => {
      const u = new URL(request.url)
      from = u.searchParams.get('from') ?? ''
      to = u.searchParams.get('to') ?? ''
      return HttpResponse.json({ source_version: 1, target_exists: false, entries: [] })
    }),
  )
  await promotion.preview('a b', 'c/d')
  expect(from).toBe('a b')
  expect(to).toBe('c/d')
})

test('apply POSTs body and returns {target_version, applied, skipped}', async () => {
  let method = '', body: any
  server.use(
    http.post('/v1/promote', async ({ request }) => {
      method = request.method
      body = await request.json()
      return HttpResponse.json({ target_version: 5, applied: ['A'], skipped: ['B'] })
    }),
  )
  const res = await promotion.apply({
    from_config: 'c-src',
    to_config: 'c-dst',
    source_version: 3,
    selections: [
      { key: 'A', action: 'set' },
      { key: 'B', action: 'remove' },
    ],
  })
  expect(method).toBe('POST')
  expect(body.from_config).toBe('c-src')
  expect(body.selections).toHaveLength(2)
  expect(res).toEqual({ target_version: 5, applied: ['A'], skipped: ['B'] })
})

test('pipeline.get GETs environment_ids; pipeline.set PUTs body', async () => {
  const got = await promotion.pipeline.get('p1')
  expect(Array.isArray(got.environment_ids)).toBe(true)

  let method = '', body: any
  server.use(
    http.put('/v1/projects/p1/pipeline', async ({ request }) => {
      method = request.method
      body = await request.json()
      return HttpResponse.json({ environment_ids: ['e1', 'e2'] })
    }),
  )
  const set = await promotion.pipeline.set('p1', ['e1', 'e2'])
  expect(method).toBe('PUT')
  expect(body).toEqual({ environment_ids: ['e1', 'e2'] })
  expect(set.environment_ids).toEqual(['e1', 'e2'])
})

test('locked.list returns keys; lock POSTs {key}; unlock DELETEs', async () => {
  const list = await promotion.locked.list('c1')
  expect(list.keys).toContain('DATABASE_URL')

  let lockBody: any
  server.use(
    http.post('/v1/configs/c1/locked-keys', async ({ request }) => {
      lockBody = await request.json()
      return HttpResponse.json({ key: 'API_KEY', locked: true })
    }),
  )
  const locked = await promotion.locked.lock('c1', 'API_KEY')
  expect(lockBody).toEqual({ key: 'API_KEY' })
  expect(locked).toEqual({ key: 'API_KEY', locked: true })

  let unlockMethod = '', unlockPath = ''
  server.use(
    http.delete('/v1/configs/c1/locked-keys/:key', ({ request, params }) => {
      unlockMethod = request.method
      unlockPath = String(params.key)
      return HttpResponse.json({ key: 'API KEY', locked: false })
    }),
  )
  const unlocked = await promotion.locked.unlock('c1', 'API KEY')
  expect(unlockMethod).toBe('DELETE')
  expect(unlockPath).toBe('API%20KEY')
  expect(unlocked.locked).toBe(false)
})
