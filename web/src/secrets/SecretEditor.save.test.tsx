import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { SecretEditor } from './SecretEditor'

test('editing a value then Save posts one batch and shows the new version', async () => {
  let put: any
  server.use(
    http.get('/v1/configs/c1/secrets', () =>
      HttpResponse.json({ secrets: { LOG_LEVEL: { value_version: 1, created_at: '', origin: 'own' } } })),
    http.get('/v1/configs/c1/secrets?reveal=true&raw=true', () =>
      HttpResponse.json({ version: 1, secrets: { LOG_LEVEL: 'info' } })),
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
