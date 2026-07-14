import { http, HttpResponse } from 'msw'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { LeasesSheet } from './LeasesSheet'

const LEASE = { id: 'l1', role_id: 'role1', status: 'active', db_username: 'janus_ro_x', expires_at: new Date(Date.now() + 3600_000).toISOString(), max_expires_at: new Date(Date.now() + 7200_000).toISOString(), renewed_at: null, created_at: 'x' }

test('lists a role\'s leases and revokes one', async () => {
  server.use(http.get('/v1/dynamic/leases', ({ request }) => {
    expect(new URL(request.url).searchParams.get('role_id')).toBe('role1')
    return HttpResponse.json({ leases: [LEASE] })
  }))
  let revoked = false
  server.use(http.post('/v1/dynamic/leases/l1/revoke', () => { revoked = true; return HttpResponse.json({ revoked: true }) }))
  renderApp(<LeasesSheet roleId="role1" roleName="readonly" onClose={() => {}} />, { route: '/operations', withAuth: false })
  expect(await screen.findByText('janus_ro_x')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /revoke/i }))
  expect(revoked).toBe(true)
})

test('renew posts to /renew', async () => {
  server.use(http.get('/v1/dynamic/leases', () => HttpResponse.json({ leases: [LEASE] })))
  let hit = false
  server.use(http.post('/v1/dynamic/leases/l1/renew', () => { hit = true; return HttpResponse.json({ ...LEASE, renewed_at: new Date().toISOString() }) }))
  renderApp(<LeasesSheet roleId="role1" roleName="readonly" onClose={() => {}} />, { route: '/operations', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /renew/i }))
  expect(hit).toBe(true)
})

test('bulk revoke: select 2 active leases, confirm gates the revokes, then fires exactly one per id', async () => {
  const A = { ...LEASE, id: 'la', db_username: 'janus_a' }
  const B = { ...LEASE, id: 'lb', db_username: 'janus_b' }
  server.use(http.get('/v1/dynamic/leases', () => HttpResponse.json({ leases: [A, B] })))
  const revoked: string[] = []
  server.use(http.post('/v1/dynamic/leases/:id/revoke', ({ params }) => { revoked.push(params.id as string); return HttpResponse.json({ revoked: true }) }))
  renderApp(<LeasesSheet roleId="role1" roleName="readonly" onClose={() => {}} />, { route: '/operations', withAuth: false })

  await userEvent.click(await screen.findByLabelText('select janus_a'))
  await userEvent.click(screen.getByLabelText('select janus_b'))
  // The bulk bar's Revoke button (not a per-card one).
  await userEvent.click(within(screen.getByRole('toolbar', { name: /bulk actions/i })).getByRole('button', { name: /revoke/i }))

  // ConfirmDialog gates: no revoke until confirm.
  expect(await screen.findByRole('heading', { name: /revoke 2 leases\?/i })).toBeInTheDocument()
  expect(revoked).toEqual([])

  const dialog = screen.getByRole('alertdialog')
  await userEvent.click(within(dialog).getByRole('button', { name: /^revoke$/i }))
  await waitFor(() => expect(revoked.length).toBe(2))
  expect(new Set(revoked)).toEqual(new Set(['la', 'lb']))
})

test('a terminal lease cannot be bulk-selected (checkbox disabled)', async () => {
  const active = { ...LEASE, id: 'la', db_username: 'janus_active' }
  const gone = { ...LEASE, id: 'lg', status: 'revoked', db_username: 'janus_revoked' }
  server.use(http.get('/v1/dynamic/leases', () => HttpResponse.json({ leases: [active, gone] })))
  renderApp(<LeasesSheet roleId="role1" roleName="readonly" onClose={() => {}} />, { route: '/operations', withAuth: false })

  expect(await screen.findByLabelText('select janus_active')).toBeEnabled()
  expect(screen.getByLabelText('select janus_revoked')).toBeDisabled()
})
