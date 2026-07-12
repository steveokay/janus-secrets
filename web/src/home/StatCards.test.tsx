import { http, HttpResponse } from 'msw'
import { screen, waitFor, within } from '@testing-library/react'
import type { Reads24h } from '../lib/endpoints'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { StatCards } from './StatCards'

const PROJECTS = [
  { id: 'p1', slug: 'acme', name: 'Acme' },
  { id: 'p2', slug: 'billing', name: 'Billing' },
]

// +2h plus a minute of slack so relativeTime's floor still yields "in 2h"
// even after test-run latency.
const IN_2H = new Date(Date.now() + 2 * 3_600_000 + 60_000).toISOString()

function rotationPolicy(over: Record<string, unknown> = {}) {
  return {
    id: 'r1', project_id: 'p1', config_id: 'c1', secret_key: 'DB_PASSWORD', type: 'postgres',
    interval_seconds: 3600, status: 'active', failure_count: 0, last_error: null,
    next_rotation_at: IN_2H, created_at: 'x', ...over,
  }
}

function syncTarget(over: Record<string, unknown> = {}) {
  return {
    id: 's1', project_id: 'p1', config_id: 'c1', provider: 'github', prune: false,
    interval_seconds: 3600, addr: { owner: 'o', repo: 'r' }, status: 'active',
    failure_count: 0, last_error: null, next_sync_at: IN_2H, managed_keys: [], created_at: 'x', ...over,
  }
}

function mockAll({
  reads = { reads_24h: 0, top_configs: [], top_tokens: [] } as Reads24h,
  policiesByProject = {} as Record<string, unknown[]>,
  targetsByProject = {} as Record<string, unknown[]>,
  verify = { valid: true, count: 0, head_seq: 0 },
} = {}) {
  // The real handlers filter by ?project_id — the mocks must too, or the
  // per-project fan-out double-counts.
  server.use(
    http.get('/v1/metrics/reads-24h', () => HttpResponse.json(reads)),
    http.get('/v1/rotation/policies', ({ request }) => {
      const pid = new URL(request.url).searchParams.get('project_id') ?? ''
      return HttpResponse.json({ policies: policiesByProject[pid] ?? [] })
    }),
    http.get('/v1/sync/targets', ({ request }) => {
      const pid = new URL(request.url).searchParams.get('project_id') ?? ''
      return HttpResponse.json({ targets: targetsByProject[pid] ?? [] })
    }),
    http.get('/v1/audit/verify', () => HttpResponse.json(verify)),
  )
}

/** Scope queries to one Stat card via its label element's Card ancestor. */
function card(label: string) {
  const el = screen.getByText(label).parentElement
  expect(el).not.toBeNull()
  return within(el!)
}

test('happy path: all four cards render computed values', async () => {
  mockAll({
    reads: {
      reads_24h: 12345,
      top_configs: [
        { config_id: 'c1', config_name: 'prod', reads: 900 },
        { config_id: 'c2', config_name: 'staging', reads: 40 },
      ],
      top_tokens: [],
    },
    policiesByProject: {
      p1: [rotationPolicy(), rotationPolicy({ id: 'r2', status: 'failed', failure_count: 3 })],
      p2: [],
    },
    targetsByProject: { p1: [syncTarget(), syncTarget({ id: 's2' })] },
    verify: { valid: true, count: 4321, head_seq: 4321 },
  })
  renderApp(<StatCards projects={PROJECTS} />, { withAuth: false })

  // Reads 24h: formatted total + top-config rows (names + counts only)
  expect(await screen.findByText('12,345')).toBeInTheDocument()
  expect(card('Reads 24h').getByText('prod')).toBeInTheDocument()

  // Rotations: 1 failed among 2 → "1 failing"
  expect(await screen.findByText('1 failing')).toBeInTheDocument()

  // Syncs: 2 active, none failed → "2 healthy" + next sync sub
  expect(await screen.findByText('2 healthy')).toBeInTheDocument()
  expect(card('Syncs').getByText('next: in 2h')).toBeInTheDocument()

  // Audit chain: event count, verified sub
  expect(await screen.findByText('4,321')).toBeInTheDocument()
  expect(card('Audit events').getByText('chain verified')).toBeInTheDocument()
})

test('active rotation whose next run is in the past shows "overdue", not "next: X ago"', async () => {
  const PAST = new Date(Date.now() - 3 * 3_600_000).toISOString()
  mockAll({
    policiesByProject: { p1: [rotationPolicy({ next_rotation_at: PAST })] },
  })
  renderApp(<StatCards projects={PROJECTS} />, { withAuth: false })

  expect(await screen.findByText('1 healthy')).toBeInTheDocument()
  expect(card('Rotations').getByText('overdue')).toBeInTheDocument()
  expect(screen.queryByText(/next:/)).not.toBeInTheDocument()
})

test('rotation 403 for all projects hides only the rotation card', async () => {
  mockAll({
    reads: { reads_24h: 7, top_configs: [], top_tokens: [] },
    targetsByProject: { p1: [syncTarget()] },
    verify: { valid: true, count: 9, head_seq: 9 },
  })
  server.use(
    http.get('/v1/rotation/policies', () =>
      HttpResponse.json({ error: { code: 'forbidden', message: 'x' } }, { status: 403 })),
  )
  renderApp(<StatCards projects={PROJECTS} />, { withAuth: false })

  expect(await screen.findByText('Reads 24h')).toBeInTheDocument()
  expect(await screen.findByText('1 healthy')).toBeInTheDocument()
  expect(await screen.findByText('Audit events')).toBeInTheDocument()
  await waitFor(() => expect(screen.queryByText('Rotations')).not.toBeInTheDocument())
})

test('zero data everywhere renders zeros without crashing', async () => {
  mockAll()
  renderApp(<StatCards projects={PROJECTS} />, { withAuth: false })

  await waitFor(() => expect(card('Reads 24h').getByText('0')).toBeInTheDocument())
  // Rotation with zero policies shows a plain 0 (no failing/healthy text)
  await waitFor(() => expect(card('Rotations').getByText('0')).toBeInTheDocument())
  expect(screen.queryByText(/failing|healthy/)).not.toBeInTheDocument()
  await waitFor(() => expect(card('Syncs').getByText('0')).toBeInTheDocument())
  await waitFor(() => expect(card('Audit events').getByText('0')).toBeInTheDocument())
})
