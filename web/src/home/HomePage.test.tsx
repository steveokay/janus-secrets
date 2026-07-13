import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import type { AuditEvent, Reads24h } from '../lib/endpoints'
import { HomePage } from './HomePage'

// Composed seam test (N2 follow-up F2): App.test.tsx's `/` route only passes
// because unmocked queries error and every section hides itself. This file
// wires FULL happy-path handlers for every endpoint the four sections hit so
// they all render together, closing that gap.

const PROJECT = { id: 'p1', slug: 'api-gateway', name: 'api-gateway' }
const ENVIRONMENTS = [{ id: 'e1', slug: 'prod', name: 'prod' }]

const IN_2H = new Date(Date.now() + 2 * 3_600_000 + 60_000).toISOString()

const READS: Reads24h = {
  reads_24h: 42,
  top_configs: [{ config_id: 'c1', config_name: 'prod', reads: 42 }],
  top_tokens: [],
}

const ROTATION_POLICY = {
  id: 'r1', project_id: 'p1', config_id: 'c1', secret_key: 'DB_PASSWORD', type: 'postgres',
  interval_seconds: 3600, status: 'active', failure_count: 0, last_error: null,
  next_rotation_at: IN_2H, created_at: 'x',
}

const SYNC_TARGET = {
  id: 's1', project_id: 'p1', config_id: 'c1', provider: 'github', prune: false,
  interval_seconds: 3600, addr: { owner: 'o', repo: 'r' }, status: 'active',
  failure_count: 0, last_error: null, next_sync_at: IN_2H, managed_keys: [], created_at: 'x',
}

const VERIFY = { valid: true, count: 12, head_seq: 12 }

function EV(seq: number, over: Partial<AuditEvent> = {}): AuditEvent {
  return {
    seq,
    occurred_at: '2026-07-06T10:00:00.000000001Z',
    actor_kind: 'user',
    actor_id: 'u1',
    actor_name: 'steve@acme.dev',
    action: 'secret.write',
    resource: 'configs/c1',
    detail: null,
    result: 'success',
    result_code: null,
    ip: '127.0.0.1',
    prev_hash: 'aa',
    hash: 'bb',
    ...over,
  }
}

/** Every endpoint the four HomePage sections hit, with a happy-path response. */
function mockAll({ verifyHandler }: { verifyHandler?: () => void } = {}) {
  server.use(
    http.get('/v1/auth/me', () => HttpResponse.json({ kind: 'user', id: 'u1', name: 'steve@acme.dev' })),
    http.get('/v1/projects', () => HttpResponse.json({ projects: [PROJECT] })),
    http.get('/v1/projects/:pid/environments', () => HttpResponse.json({ environments: ENVIRONMENTS })),
    http.get('/v1/metrics/reads-24h', () => HttpResponse.json(READS)),
    http.get('/v1/audit/verify', () => {
      verifyHandler?.()
      return HttpResponse.json(VERIFY)
    }),
    http.get('/v1/audit/events', () => HttpResponse.json({ events: [EV(1)], next_cursor: null })),
    http.get('/v1/rotation/policies', ({ request }) => {
      const pid = new URL(request.url).searchParams.get('project_id') ?? ''
      return HttpResponse.json({ policies: pid === 'p1' ? [ROTATION_POLICY] : [] })
    }),
    http.get('/v1/sync/targets', ({ request }) => {
      const pid = new URL(request.url).searchParams.get('project_id') ?? ''
      return HttpResponse.json({ targets: pid === 'p1' ? [SYNC_TARGET] : [] })
    }),
  )
}

test('renders all four sections together: header, stats, projects, activity', async () => {
  mockAll()
  renderApp(<HomePage />)

  // HomeHeader: greeting + chain badge
  expect(await screen.findByText(/Good (morning|afternoon|evening), Steve/)).toBeInTheDocument()
  // "chain verified" renders twice: HomeHeader's pill badge AND StatCards'
  // AuditStat sub text — both are legitimate, independent consumers.
  expect((await screen.findAllByText('chain verified')).length).toBe(2)

  // StatCards: at least one card label + computed value
  expect(await screen.findByText('Reads 24h')).toBeInTheDocument()
  expect(screen.getByText('42')).toBeInTheDocument()

  // HomeProjects: the mocked project's card
  expect(await screen.findByRole('link', { name: /api-gateway/i })).toBeInTheDocument()

  // ActivityFeed: heading + the mocked event's action
  expect(await screen.findByText('Recent activity')).toBeInTheDocument()
  expect(screen.getByText('secret.write')).toBeInTheDocument()
})

test('the shared audit-verify query is deduped to exactly one network call', async () => {
  let verifyCalls = 0
  mockAll({ verifyHandler: () => { verifyCalls++ } })
  renderApp(<HomePage />)

  // Wait for both consumers of ['audit','verify'] to have mounted and
  // rendered from the same resolved query before counting.
  await waitFor(() => expect(screen.queryAllByText('chain verified').length).toBe(2)) // HomeHeader badge + StatCards sub
  expect(await screen.findByText('Audit events')).toBeInTheDocument() // StatCards audit card
  expect(await screen.findByText('12')).toBeInTheDocument() // AuditStat value from verify.count

  expect(verifyCalls).toBe(1)
})
