import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderApp } from '../test/render'
import { AppearanceSection } from './AppearanceSection'

test('renders a Theme picker', async () => {
  renderApp(<AppearanceSection />, { route: '/settings?section=appearance', withAuth: false })
  expect(await screen.findByText(/theme/i)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /^light$/i })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /^dark$/i })).toBeInTheDocument()
})

test('choosing Dark applies the dark class and marks it selected', async () => {
  renderApp(<AppearanceSection />, { route: '/settings?section=appearance', withAuth: false })
  const dark = await screen.findByRole('button', { name: /^dark$/i })
  await userEvent.click(dark)
  expect(document.documentElement.classList.contains('dark')).toBe(true)
  expect(dark).toHaveAttribute('aria-pressed', 'true')
})

test('choosing Light removes the dark class', async () => {
  renderApp(<AppearanceSection />, { route: '/settings?section=appearance', withAuth: false })
  const light = await screen.findByRole('button', { name: /^light$/i })
  await userEvent.click(light)
  expect(document.documentElement.classList.contains('dark')).toBe(false)
  expect(light).toHaveAttribute('aria-pressed', 'true')
})
