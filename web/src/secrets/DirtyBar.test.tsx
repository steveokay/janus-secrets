import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { SecretEditor } from './SecretEditor'

function seed() {
  server.use(
    http.get('/v1/configs/c1/secrets', ({ request }) => {
      if (new URL(request.url).searchParams.get('reveal') === 'true')
        return HttpResponse.json({ version: 3, secrets: { DB_URL: 'postgres://a' } })
      return HttpResponse.json({ secrets: {
        DB_URL: { value_version: 3, created_at: '', origin: 'own' },
      } })
    }),
  )
}

test('dirty-bar summarizes pending edits and saves', async () => {
  seed()
  server.use(http.put('/v1/configs/c1/secrets', () => HttpResponse.json({ version: 4, id: 'v4', created_at: '' })))
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.click(screen.getByRole('button', { name: /remove db_url/i }))
  expect(screen.getByText(/1 removed/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /save as v4/i }))
  await waitFor(() => expect(screen.queryByText(/1 removed/i)).toBeNull())
})

test('review diff lists changed key names, no values', async () => {
  seed()
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.click(screen.getByRole('button', { name: /remove db_url/i }))
  await userEvent.click(screen.getByRole('button', { name: /review diff/i }))
  const dialog = await screen.findByRole('dialog', { name: /review changes/i })
  expect(dialog).toHaveTextContent('DB_URL')
  expect(dialog).toHaveTextContent(/removed/i)
  expect(screen.queryByText('postgres://a')).toBeNull() // no values in the diff
})
