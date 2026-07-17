import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { AppLayout } from './AppLayout'

function boot() {
  server.use(
    http.get('/v1/auth/me', () => new HttpResponse(null, { status: 401 })),
    http.get('/v1/projects', () => HttpResponse.json({ projects: [] })),
  )
}

test('the mobile menu button opens an off-canvas sidebar overlay, closed by default', async () => {
  boot()
  renderApp(
    <AppLayout sealed={false} sidebar={<nav>sidebar content</nav>}>
      <p>page body</p>
    </AppLayout>,
  )
  // Closed by default — the overlay's own aria-label isn't present yet.
  expect(screen.queryByRole('complementary', { name: 'sidebar' })).toBeNull()
  await userEvent.click(screen.getByRole('button', { name: 'open sidebar' }))
  expect(screen.getByRole('complementary', { name: 'sidebar' })).toBeInTheDocument()
})

test('clicking the overlay backdrop closes the mobile sidebar', async () => {
  boot()
  const { container } = renderApp(
    <AppLayout sealed={false} sidebar={<nav>sidebar content</nav>}>
      <p>page body</p>
    </AppLayout>,
  )
  await userEvent.click(screen.getByRole('button', { name: 'open sidebar' }))
  expect(screen.getByRole('complementary', { name: 'sidebar' })).toBeInTheDocument()
  const backdrop = container.querySelector('[aria-hidden="true"].absolute.inset-0')
  expect(backdrop).toBeTruthy()
  await userEvent.click(backdrop!)
  expect(screen.queryByRole('complementary', { name: 'sidebar' })).toBeNull()
})

test('the desktop sidebar always renders (unaffected by the mobile overlay)', () => {
  boot()
  renderApp(
    <AppLayout sealed={false} sidebar={<nav>sidebar content</nav>}>
      <p>page body</p>
    </AppLayout>,
  )
  expect(screen.getAllByText('sidebar content').length).toBeGreaterThan(0)
})
