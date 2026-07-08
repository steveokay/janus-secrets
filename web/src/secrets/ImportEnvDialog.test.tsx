import { http, HttpResponse } from 'msw'
import { screen, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { SecretEditor } from './SecretEditor'

function seed() {
  server.use(
    http.get('/v1/configs/c1/secrets', ({ request }) => {
      if (new URL(request.url).searchParams.get('reveal') === 'true')
        return HttpResponse.json({ version: 3, secrets: { DB_URL: 'postgres://a' } })
      return HttpResponse.json({ secrets: { DB_URL: { value_version: 3, created_at: '', origin: 'own' } } })
    }),
    http.get('/v1/configs/c1/versions', () => HttpResponse.json({ versions: [
      { version: 3, message: '', created_by: '', created_at: '' },
    ] })),
  )
}

test('import .env stages parsed pairs into the buffer as pending rows', async () => {
  seed()
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.click(screen.getByRole('button', { name: /import \.env/i }))
  const ta = await screen.findByLabelText(/paste .*env/i)
  // KEY=VALUE only; comment ignored, malformed line skipped.
  fireEvent.change(ta, { target: { value: 'NEW_KEY=abc\n# a comment\nBAD KEY=x' } })
  expect(screen.getByText(/1 key/i)).toBeInTheDocument()
  expect(screen.getByText(/1 skipped/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /import 1/i }))
  // The imported key now shows as a pending added row.
  expect(await screen.findByText('NEW_KEY')).toBeInTheDocument()
  expect(screen.getByText('added')).toBeInTheDocument()
})
