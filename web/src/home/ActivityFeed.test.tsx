import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import type { AuditEvent } from '../lib/endpoints'
import { ActivityFeed } from './ActivityFeed'

// Builds a complete AuditEvent (real wire shape), with per-test overrides.
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
function mockEvents(body: any) {
  server.use(http.get('/v1/audit/events', () => HttpResponse.json(body)))
}

function mount() {
  return renderApp(<ActivityFeed />, { withAuth: false })
}

test('renders event rows with mono action, actor/resource line and result dots', async () => {
  mockEvents({
    events: [
      EV(2, { action: 'secret.write', resource: 'configs/c1', actor_name: 'steve@acme.dev', result: 'success' }),
      EV(1, { action: 'secret.reveal', resource: 'configs/c1', actor_name: 'bot@ci', result: 'denied' }),
    ],
    next_cursor: null,
  })
  mount()
  expect(await screen.findByText('secret.write')).toBeInTheDocument()
  expect(screen.getByText('secret.reveal')).toBeInTheDocument()
  expect(screen.getByText(/configs\/c1 · steve@acme.dev/)).toBeInTheDocument()
  expect(screen.getByText(/configs\/c1 · bot@ci/)).toBeInTheDocument()

  const deniedRow = screen.getByText('secret.reveal').closest('li')
  expect(deniedRow).not.toBeNull()
  const dot = deniedRow!.querySelector('span.bg-warning')
  expect(dot).toBeInTheDocument()
})

test('hides entirely when the events query errors (e.g. 403)', async () => {
  server.use(
    http.get('/v1/audit/events', () =>
      HttpResponse.json({ error: { code: 'forbidden', message: 'x' } }, { status: 403 })),
  )
  const { container } = renderApp(<ActivityFeed />, { withAuth: false })
  await waitFor(() => expect(container).toBeEmptyDOMElement())
})

test('zero events shows "No activity yet"', async () => {
  mockEvents({ events: [], next_cursor: null })
  mount()
  expect(await screen.findByText('No activity yet')).toBeInTheDocument()
})

test('"View all" links to /audit', async () => {
  mockEvents({ events: [], next_cursor: null })
  mount()
  const link = await screen.findByRole('link', { name: /view all/i })
  expect(link).toHaveAttribute('href', '/audit')
})

test('never renders event detail content', async () => {
  mockEvents({
    events: [EV(1, { detail: 'super secret payload description' })],
    next_cursor: null,
  })
  mount()
  await screen.findByText('secret.write')
  expect(screen.queryByText(/super secret payload description/)).not.toBeInTheDocument()
})
