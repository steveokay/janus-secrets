import { http, HttpResponse } from 'msw'
import { server } from '../test/msw'
import { endpoints, memberScopePath } from './endpoints'

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

test('listAuditEvents builds params and returns envelope', async () => {
  let url = ''
  server.use(http.get('/v1/audit/events', ({ request }) => {
    url = request.url
    return HttpResponse.json({ events: [
      { seq: 5, occurred_at: '2026-07-06T10:00:00.000000001Z', actor_kind: 'user', actor_id: 'u1',
        actor_name: 'steve@acme.dev', action: 'secret.write', resource: 'configs/c1', detail: null,
        result: 'success', result_code: null, ip: '127.0.0.1', prev_hash: 'aa', hash: 'bb' },
    ], next_cursor: 5 })
  }))
  const r = await endpoints.listAuditEvents({ actor: 'steve', result: 'denied', cursor: 9, limit: 2 })
  expect(r.next_cursor).toBe(5)
  const q = new URL(url).searchParams
  expect(q.get('actor')).toBe('steve')
  expect(q.get('result')).toBe('denied')
  expect(q.get('cursor')).toBe('9')
  expect(q.get('limit')).toBe('2')
  expect(q.get('from')).toBeNull()
})

test('verifyAudit returns the verify result', async () => {
  server.use(http.get('/v1/audit/verify', () =>
    HttpResponse.json({ valid: true, count: 42, head_seq: 42, head_hash: 'ff' })))
  await expect(endpoints.verifyAudit()).resolves.toMatchObject({ valid: true, count: 42 })
})

test('auditExportUrl carries filters and format', () => {
  const u = endpoints.auditExportUrl({ actor: 'steve', result: 'denied' }, 'csv')
  const q = new URL(u, 'http://x').searchParams
  expect(u.startsWith('/v1/audit/export?')).toBe(true)
  expect(q.get('format')).toBe('csv')
  expect(q.get('actor')).toBe('steve')
  expect(q.get('result')).toBe('denied')
})

test('memberScopePath covers all three scopes', () => {
  expect(memberScopePath({ kind: 'instance' })).toBe('/v1/instance/members')
  expect(memberScopePath({ kind: 'project', pid: 'p1' })).toBe('/v1/projects/p1/members')
  expect(memberScopePath({ kind: 'environment', pid: 'p1', eid: 'e1' })).toBe('/v1/projects/p1/environments/e1/members')
})

test('mintToken posts the exact request body', async () => {
  let body: unknown
  server.use(http.post('/v1/tokens', async ({ request }) => {
    body = await request.json()
    return HttpResponse.json({ token: 'janus_svc_abc', id: 't1', name: 'ci',
      scope: { kind: 'config', id: 'c1' }, access: 'read', expires_at: null })
  }))
  const r = await endpoints.mintToken({ name: 'ci', scope: { kind: 'config', id: 'c1' }, access: 'read', ttl_seconds: 3600 })
  expect(r.token).toBe('janus_svc_abc')
  expect(body).toEqual({ name: 'ci', scope: { kind: 'config', id: 'c1' }, access: 'read', ttl_seconds: 3600 })
})

test('listTokens unwraps and tolerates omitted optionals', async () => {
  server.use(http.get('/v1/tokens', () => HttpResponse.json({ tokens: [
    { id: 't1', name: 'ci', scope_kind: 'config', scope_id: 'c1', access: 'read',
      created_by: 'u1', created_at: '2026-07-06T10:00:00Z' },
  ] })))
  const list = await endpoints.listTokens()
  expect(list[0].expires_at).toBeUndefined()
  expect(list[0].revoked_at).toBeUndefined()
})

test('putMember sends role to the scoped path', async () => {
  let body: unknown, hit = false
  server.use(http.put('/v1/projects/p1/members/u2', async ({ request }) => {
    hit = true; body = await request.json()
    return new HttpResponse(null, { status: 204 })
  }))
  await endpoints.putMember({ kind: 'project', pid: 'p1' }, 'u2', 'developer')
  expect(hit).toBe(true)
  expect(body).toEqual({ role: 'developer' })
})

test('listTrash returns grouped soft-deleted entities', async () => {
  server.use(
    http.get('/v1/trash', () =>
      HttpResponse.json({
        projects: [{ id: 'p1', slug: 'web', name: 'Web', deleted_at: '2026-07-14T10:00:00Z' }],
        environments: [{ id: 'e1', slug: 'prod', name: 'Prod', project_id: 'p1', project_name: 'Web', deleted_at: '2026-07-14T10:00:00Z' }],
        configs: [{ id: 'c1', name: 'root', environment_id: 'e1', environment_name: 'Prod', project_id: 'p1', project_name: 'Web', deleted_at: '2026-07-14T10:00:00Z' }],
      }),
    ),
  )
  const t = await endpoints.listTrash()
  expect(t.projects[0].name).toBe('Web')
  expect(t.configs[0].environment_name).toBe('Prod')
})

test('restoreConfig POSTs to the restore route', async () => {
  let hit = ''
  server.use(
    http.post('/v1/configs/:cid/restore', ({ params }) => {
      hit = String(params.cid)
      return HttpResponse.json({ id: 'c1', environment_id: 'e1', name: 'root', inherits_from: null, created_at: '2026-07-14T10:00:00Z' })
    }),
  )
  await endpoints.restoreConfig('c1')
  expect(hit).toBe('c1')
})

test('destroyConfig DELETEs with destroy=true', async () => {
  let url = ''
  server.use(
    http.delete('/v1/configs/:cid', ({ request }) => {
      url = new URL(request.url).search
      return new HttpResponse(null, { status: 204 })
    }),
  )
  await endpoints.destroyConfig('c1')
  expect(url).toContain('destroy=true')
})

test('deleteEnvironment and destroyEnvironment hit the env routes', async () => {
  let delUrl = ''
  server.use(
    http.delete('/v1/projects/:pid/environments/:eid', ({ request }) => {
      delUrl = new URL(request.url).pathname + new URL(request.url).search
      return new HttpResponse(null, { status: 204 })
    }),
  )
  await endpoints.deleteEnvironment('p1', 'e1')
  expect(delUrl).toBe('/v1/projects/p1/environments/e1')
  await endpoints.destroyEnvironment('p1', 'e1')
  expect(delUrl).toContain('destroy=true')
})
