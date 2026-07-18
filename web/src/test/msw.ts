import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'

// Default promotion handlers so later UI tasks (diff modal, drag board) can
// render against realistic mocks without wiring them up per test. Individual
// tests may still override any of these via server.use(). The JSON shapes below
// mirror the Go promotion handlers EXACTLY (snake_case field names).
export const promotionHandlers = [
  // GET /v1/projects/{pid}/pipeline
  http.get('/v1/projects/:pid/pipeline', () =>
    HttpResponse.json({ environment_ids: ['env-dev', 'env-staging', 'env-prod'] }),
  ),
  // PUT /v1/projects/{pid}/pipeline
  http.put('/v1/projects/:pid/pipeline', async ({ request }) => {
    const body = (await request.json()) as { environment_ids: string[] }
    return HttpResponse.json({ environment_ids: body.environment_ids })
  }),
  // GET /v1/configs/{cid}/locked-keys
  http.get('/v1/configs/:cid/locked-keys', () => HttpResponse.json({ keys: ['DATABASE_URL'] })),
  // POST /v1/configs/{cid}/locked-keys
  http.post('/v1/configs/:cid/locked-keys', async ({ request }) => {
    const body = (await request.json()) as { key: string }
    return HttpResponse.json({ key: body.key, locked: true })
  }),
  // DELETE /v1/configs/{cid}/locked-keys/{key}
  http.delete('/v1/configs/:cid/locked-keys/:key', ({ params }) =>
    HttpResponse.json({ key: String(params.key), locked: false }),
  ),
  // GET /v1/promote/preview?from={cid}&to={cid}
  http.get('/v1/promote/preview', () =>
    HttpResponse.json({
      source_version: 3,
      target_exists: true,
      entries: [
        { key: 'NEW_FLAG', status: 'add', source_value: 'on', target_value: '', locked: false },
        { key: 'API_URL', status: 'change', source_value: 'https://prod', target_value: 'https://stg', locked: false },
        { key: 'DATABASE_URL', status: 'same', source_value: 'postgres://db', target_value: 'postgres://db', locked: true },
        { key: 'OLD_KEY', status: 'remove', source_value: '', target_value: 'legacy', locked: false },
      ],
    }),
  ),
  // POST /v1/promote
  http.post('/v1/promote', async ({ request }) => {
    const body = (await request.json()) as { selections?: { key: string; action: string }[] }
    const sels = body.selections ?? []
    return HttpResponse.json({
      target_version: 4,
      applied: sels.filter((s) => s.action === 'set').map((s) => s.key),
      skipped: sels.filter((s) => s.action !== 'set').map((s) => s.key),
    })
  }),
  // POST /v1/promote/requests
  http.post('/v1/promote/requests', () => HttpResponse.json({ id: 'req-1', status: 'pending' }, { status: 201 })),
  // GET /v1/promote/requests
  http.get('/v1/promote/requests', () => HttpResponse.json({ requests: [] })),
  // GET /v1/promote/requests/{id}
  http.get('/v1/promote/requests/:id', ({ params }) =>
    HttpResponse.json({
      id: String(params.id),
      project_id: 'proj-1',
      source_config_id: 'c-dev',
      source_version: 3,
      target_env_id: 'env-staging',
      target_config_id: 'c-stg',
      target_name: 'default',
      create_target: false,
      keys: ['NEW_FLAG'],
      selections: [{ key: 'NEW_FLAG', action: 'set' }],
      note: '',
      status: 'pending',
      requested_by: 'user-1',
      created_at: '2026-07-16T00:00:00Z',
      diff: {
        source_version: 3,
        target_exists: true,
        entries: [{ key: 'NEW_FLAG', status: 'add', locked: false }],
      },
    }),
  ),
  // POST /v1/promote/requests/{id}/approve
  http.post('/v1/promote/requests/:id/approve', () =>
    HttpResponse.json({ target_version: 4, applied: ['NEW_FLAG'], skipped: [] }),
  ),
  // POST /v1/promote/requests/{id}/reject
  http.post('/v1/promote/requests/:id/reject', () => HttpResponse.json({ status: 'rejected' })),
  // POST /v1/promote/requests/{id}/cancel
  http.post('/v1/promote/requests/:id/cancel', () => HttpResponse.json({ status: 'cancelled' })),
]

// Default owner-only master-key status so the Instance settings section (which
// mounts MasterKeySection) renders without an unhandled-request warning. Shape
// mirrors the Go GET /v1/sys/master-key handler EXACTLY (snake_case). Tests that
// exercise MasterKeySection override this via server.use().
export const masterKeyHandlers = [
  http.get('/v1/sys/master-key', () =>
    HttpResponse.json({
      unseal_type: 'awskms',
      master_key_version: 1,
      rotated_at: null,
      rekey_in_progress: false,
      submitted: 0,
      required: 0,
    }),
  ),
]

// Default empty histogram so AuditPage's embedded AuditHistogram doesn't spam
// unhandled-request warnings in tests that don't care about it. Tests
// exercising the histogram itself override via server.use()/vi.spyOn.
export const auditHistogramHandlers = [
  http.get('/v1/audit/histogram', () => HttpResponse.json({ buckets: [] })),
]

export const server = setupServer(...promotionHandlers, ...masterKeyHandlers, ...auditHistogramHandlers)
