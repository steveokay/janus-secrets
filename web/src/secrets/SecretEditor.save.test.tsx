import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { SecretEditor } from './SecretEditor'

test('editing a value then Save posts one batch and shows the new version', async () => {
  let put: any
  server.use(
    http.get('/v1/configs/c1/secrets', ({ request }) => {
      if (new URL(request.url).searchParams.get('reveal') === 'true')
        return HttpResponse.json({ version: 1, secrets: { LOG_LEVEL: 'info' } })
      return HttpResponse.json({ secrets: { LOG_LEVEL: { value_version: 1, created_at: '', origin: 'own' } } })
    }),
    http.put('/v1/configs/c1/secrets', async ({ request }) => {
      put = await request.json()
      return HttpResponse.json({ version: 2, id: 'v2', created_at: '' })
    }),
  )
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('LOG_LEVEL')
  await userEvent.click(screen.getByRole('button', { name: /edit log_level/i }))
  const input = screen.getByRole('textbox', { name: /value for log_level/i })
  await userEvent.clear(input)
  await userEvent.type(input, 'debug')
  expect(screen.getByText(/1 changed/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /save as v2/i }))
  await waitFor(() => expect(put).toEqual({ message: '', changes: [{ key: 'LOG_LEVEL', value: 'debug' }] }))
})

test('a newly added key renders a visible, editable, saveable row', async () => {
  let put: any
  server.use(
    http.get('/v1/configs/c1/secrets', ({ request }) => {
      if (new URL(request.url).searchParams.get('reveal') === 'true')
        return HttpResponse.json({ version: 6, secrets: {} })
      return HttpResponse.json({ secrets: {} })
    }),
    http.put('/v1/configs/c1/secrets', async ({ request }) => {
      put = await request.json()
      return HttpResponse.json({ version: 7, id: 'v7', created_at: '' })
    }),
  )
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  // Add a new key via the add row.
  await userEvent.type(await screen.findByLabelText(/new key/i), 'FEATURE_X')
  await userEvent.type(screen.getByLabelText(/new value/i), 'on')
  await userEvent.click(screen.getByRole('button', { name: /add key/i }))

  // The pending add is now a visible, editable row (not buffer-invisible).
  const row = await screen.findByLabelText(/value for FEATURE_X/i)
  expect(row).toHaveValue('on')
  expect(screen.getByText(/\+1 added/i)).toBeInTheDocument()
  // Version label reflects the real config version (6 → 7), not a value-version.
  await userEvent.click(screen.getByRole('button', { name: /save as v7/i }))
  await waitFor(() => expect(put).toEqual({ message: '', changes: [{ key: 'FEATURE_X', value: 'on' }] }))
})

test('a pending add can be cancelled before save', async () => {
  server.use(
    http.get('/v1/configs/c1/secrets', ({ request }) => {
      if (new URL(request.url).searchParams.get('reveal') === 'true')
        return HttpResponse.json({ version: 2, secrets: {} })
      return HttpResponse.json({ secrets: {} })
    }),
  )
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await userEvent.type(await screen.findByLabelText(/new key/i), 'OOPS')
  await userEvent.type(screen.getByLabelText(/new value/i), 'x')
  await userEvent.click(screen.getByRole('button', { name: /add key/i }))
  await screen.findByLabelText(/value for OOPS/i)
  await userEvent.click(screen.getByRole('button', { name: /remove OOPS/i }))
  expect(screen.queryByLabelText(/value for OOPS/i)).toBeNull()
})
