import { http, HttpResponse, JsonBodyType } from 'msw'
import { screen } from '@testing-library/react'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { HomeHeader } from './HomeHeader'

function mockMe(user: { kind: string; id: string; name: string }) {
  server.use(http.get('/v1/auth/me', () => HttpResponse.json(user)))
}
function mockVerify(body: JsonBodyType, status = 200) {
  server.use(http.get('/v1/audit/verify', () => HttpResponse.json(body, { status })))
}

test('greets with the capitalized email local part and the project count', async () => {
  mockMe({ kind: 'user', id: 'u1', name: 'steve@acme.dev' })
  mockVerify({ error: { code: 'forbidden', message: 'x' } }, 403)
  renderApp(<HomeHeader projectCount={3} />)
  expect(await screen.findByText(/Good (morning|afternoon|evening), Steve/)).toBeInTheDocument()
  expect(screen.getByText('3 projects')).toBeInTheDocument()
})

test('singular project count reads "1 project"', async () => {
  mockMe({ kind: 'user', id: 'u1', name: 'steve@acme.dev' })
  mockVerify({ error: { code: 'forbidden', message: 'x' } }, 403)
  renderApp(<HomeHeader projectCount={1} />)
  expect(await screen.findByText('1 project')).toBeInTheDocument()
})

test('service-token principals get a plain Welcome', async () => {
  mockMe({ kind: 'service_token', id: 't1', name: 'ci-deploy' })
  mockVerify({ error: { code: 'forbidden', message: 'x' } }, 403)
  renderApp(<HomeHeader projectCount={0} />)
  expect(await screen.findByText('Welcome')).toBeInTheDocument()
})

test('shows the chain-verified badge when the audit chain is intact', async () => {
  mockMe({ kind: 'user', id: 'u1', name: 'steve@acme.dev' })
  mockVerify({ valid: true, count: 12, head_seq: 12 })
  renderApp(<HomeHeader projectCount={0} />)
  expect(await screen.findByText('chain verified')).toBeInTheDocument()
})

test('shows the chain FAILED badge when verify reports a broken chain', async () => {
  mockMe({ kind: 'user', id: 'u1', name: 'steve@acme.dev' })
  mockVerify({ valid: false, count: 12, head_seq: 12, broken_at_seq: 4, reason: 'hash_mismatch' })
  renderApp(<HomeHeader projectCount={0} />)
  expect(await screen.findByText('chain FAILED')).toBeInTheDocument()
})

test('renders no badge at all when verify is forbidden (403)', async () => {
  mockMe({ kind: 'user', id: 'u1', name: 'steve@acme.dev' })
  mockVerify({ error: { code: 'forbidden', message: 'x' } }, 403)
  renderApp(<HomeHeader projectCount={0} />)
  await screen.findByText(/Good (morning|afternoon|evening), Steve/)
  expect(screen.queryByText(/chain/)).not.toBeInTheDocument()
})
