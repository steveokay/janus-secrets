import { http, HttpResponse } from 'msw'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { AuditEvent } from '../lib/endpoints'
import { AuditPage } from './AuditPage'

// Builds a complete 13-field AuditEvent row (real wire shape — mirrors the
// Task 3 endpoints.test.ts fixture), with per-test overrides.
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

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function mockVerify(body: any) {
  server.use(http.get('/v1/audit/verify', () => HttpResponse.json(body)))
}
// eslint-disable-next-line @typescript-eslint/no-explicit-any
function mockEvents(body: any) {
  server.use(http.get('/v1/audit/events', () => HttpResponse.json(body)))
}
function mount() {
  return renderApp(<AuditPage />, { route: '/projects/p1/audit', withAuth: false })
}

test('chain-verified badge shows count', async () => {
  mockVerify({ valid: true, count: 42, head_seq: 42 })
  mockEvents({ events: [], next_cursor: null })
  mount()
  expect(await screen.findByText(/Chain verified · 42 events/)).toBeInTheDocument()
})

test('chain-broken badge shows the break point', async () => {
  mockVerify({ valid: false, count: 40, head_seq: 40, broken_at_seq: 17, reason: 'hash_mismatch' })
  mockEvents({ events: [], next_cursor: null })
  mount()
  expect(await screen.findByText(/Chain broken at #17/)).toBeInTheDocument()
})

test('table renders rows with result-pill mapping and truncated cells', async () => {
  mockVerify({ valid: true, count: 2, head_seq: 2 })
  mockEvents({
    events: [
      EV(2, { action: 'secret.write', resource: 'configs/c1', result: 'success' }),
      EV(1, {
        action: 'auth.login',
        resource: 'auth',
        result: 'denied',
        detail: 'bad password attempt from a suspicious location that is quite long',
      }),
    ],
    next_cursor: null,
  })
  mount()
  expect(await screen.findByText('secret.write')).toBeInTheDocument()
  expect(screen.getByText('auth.login')).toBeInTheDocument()
  const table = screen.getByRole('table')
  expect(within(table).getByText('success')).toBeInTheDocument()
  expect(within(table).getByText('denied')).toBeInTheDocument()
  const resourceCell = screen.getByTitle('configs/c1')
  expect(resourceCell).toBeInTheDocument()
  const detailCell = screen.getByTitle('bad password attempt from a suspicious location that is quite long')
  expect(detailCell).toBeInTheDocument()
})

test('load more walks the cursor to a second page then hides the button', async () => {
  server.use(
    http.get('/v1/audit/verify', () => HttpResponse.json({ valid: true, count: 3, head_seq: 3 })),
    http.get('/v1/audit/events', ({ request }) => {
      const cursor = new URL(request.url).searchParams.get('cursor')
      if (!cursor) {
        return HttpResponse.json({ events: [EV(3), EV(2)], next_cursor: 2 })
      }
      expect(cursor).toBe('2')
      return HttpResponse.json({ events: [EV(1, { action: 'auth.login' })], next_cursor: null })
    }),
  )
  mount()
  expect(await screen.findByText('Load more')).toBeInTheDocument()
  await userEvent.click(screen.getByText('Load more'))
  expect(await screen.findByText('auth.login')).toBeInTheDocument()
  expect(screen.queryByText('Load more')).not.toBeInTheDocument()
})

test('Apply commits filter draft to the events query params', async () => {
  mockVerify({ valid: true, count: 1, head_seq: 1 })
  let url = ''
  server.use(http.get('/v1/audit/events', ({ request }) => {
    url = request.url
    return HttpResponse.json({ events: [EV(1)], next_cursor: null })
  }))
  mount()
  await screen.findByText(/Chain verified/)
  await userEvent.type(screen.getByLabelText('actor filter'), 'steve')
  await userEvent.selectOptions(screen.getByLabelText('result filter'), 'denied')
  await userEvent.click(screen.getByText('Apply'))
  await screen.findByText('secret.write')
  const q = new URL(url).searchParams
  expect(q.get('actor')).toBe('steve')
  expect(q.get('result')).toBe('denied')
})

test('export CSV anchor carries the applied filters and format', async () => {
  mockVerify({ valid: true, count: 1, head_seq: 1 })
  mockEvents({ events: [EV(1)], next_cursor: null })
  mount()
  await screen.findByText(/Chain verified/)
  await userEvent.type(screen.getByLabelText('actor filter'), 'steve')
  await userEvent.selectOptions(screen.getByLabelText('result filter'), 'denied')
  await userEvent.click(screen.getByText('Apply'))
  await screen.findByText('secret.write')
  const link = screen.getByText('Export CSV').closest('a')!
  expect(link.getAttribute('href')).toContain('format=csv')
  expect(link.getAttribute('href')).toContain('actor=steve')
  expect(link.getAttribute('href')).toContain('result=denied')
})

test('403 on events shows the "Audit access required" empty state', async () => {
  mockVerify({ valid: true, count: 1, head_seq: 1 })
  server.use(http.get('/v1/audit/events', () =>
    HttpResponse.json({ error: { code: 'forbidden', message: 'x' } }, { status: 403 })))
  mount()
  expect(await screen.findByText('Audit access required')).toBeInTheDocument()
})

test('zero events shows the "no events match" empty state', async () => {
  mockVerify({ valid: true, count: 0, head_seq: 0 })
  mockEvents({ events: [], next_cursor: null })
  mount()
  await screen.findByText(/Chain verified/)
  expect(await screen.findByText('No events match these filters.')).toBeInTheDocument()
})
