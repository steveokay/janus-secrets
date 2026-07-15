import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ToastProvider } from '../ui/Toast'
import { KeyHistorySheet } from './KeyHistorySheet'

function mountSheet() {
  server.use(
    http.get('/v1/configs/:cid/secrets/:key/history', () =>
      HttpResponse.json({ key: 'API_KEY', history: [{ value_version: 2, created_at: 'b' }, { value_version: 1, created_at: 'a' }] }),
    ),
  )
  return renderApp(
    <ToastProvider><KeyHistorySheet cid="c1" secretKey="API_KEY" open onOpenChange={() => {}} /></ToastProvider>,
    { route: '/projects/p1/configs/c1', withAuth: false },
  )
}

test('lists versions newest-first, value-free by default', async () => {
  mountSheet()
  expect(await screen.findByText('v2')).toBeInTheDocument()
  expect(screen.getByText('v1')).toBeInTheDocument()
  expect(screen.queryByText('old-secret')).not.toBeInTheDocument()
})

test('revealing a version calls the audited versioned reveal and shows plaintext', async () => {
  let revealedVersion = ''
  server.use(http.get('/v1/configs/:cid/secrets/:key', ({ request }) => {
    revealedVersion = new URL(request.url).searchParams.get('version') ?? ''
    return HttpResponse.json({ key: 'API_KEY', value: 'old-secret', value_version: 1 })
  }))
  mountSheet()
  await userEvent.click(await screen.findByRole('button', { name: /reveal v1/i }))
  expect(await screen.findByText('old-secret')).toBeInTheDocument()
  expect(revealedVersion).toBe('1')
})
