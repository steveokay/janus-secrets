import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { TransitPage } from './TransitPage'

const KEY = { name: 'app', type: 'aes256-gcm', latest_version: 3, min_decryption_version: 1, deletion_allowed: false, versions: [1, 2, 3] }
function mock(key = KEY) { server.use(http.get('/v1/transit/keys', () => HttpResponse.json({ keys: [key] }))) }

test('rotate posts and refreshes', async () => {
  mock()
  let rotated = false
  server.use(http.post('/v1/transit/keys/app/rotate', () => { rotated = true; return HttpResponse.json({ ...KEY, latest_version: 4, versions: [1, 2, 3, 4] }) }))
  renderApp(<TransitPage />, { route: '/transit', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /actions for app|app actions|more/i }))
  await userEvent.click(await screen.findByRole('menuitem', { name: /rotate/i }))
  expect(rotated).toBe(true)
})

test('delete of a protected key surfaces the 409 verbatim', async () => {
  mock()
  server.use(http.delete('/v1/transit/keys/app', () =>
    HttpResponse.json({ error: { code: 'conflict', message: 'deletion not allowed for this key' } }, { status: 409 })))
  renderApp(<TransitPage />, { route: '/transit', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /actions for app|app actions|more/i }))
  await userEvent.click(await screen.findByRole('menuitem', { name: /delete/i }))
  await userEvent.click(await screen.findByRole('button', { name: /^delete|confirm/i }))
  expect(await screen.findByText(/deletion not allowed for this key/i)).toBeInTheDocument()
})

test('configure posts min_decryption_version within bounds', async () => {
  mock()
  let cfg: unknown
  server.use(http.post('/v1/transit/keys/app/config', async ({ request }) => { cfg = await request.json(); return HttpResponse.json({ ...KEY, min_decryption_version: 2 }) }))
  renderApp(<TransitPage />, { route: '/transit', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /actions for app|app actions|more/i }))
  await userEvent.click(await screen.findByRole('menuitem', { name: /configure/i }))
  const input = await screen.findByLabelText(/min.*decryption/i)
  await userEvent.clear(input); await userEvent.type(input, '2')
  await userEvent.click(screen.getByRole('button', { name: /save|apply/i }))
  expect(cfg).toMatchObject({ min_decryption_version: 2 })
})
