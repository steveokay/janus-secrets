import { screen } from '@testing-library/react'
import { renderApp } from '../test/render'
import { SettingsPage } from './SettingsPage'

test('renders subnav and defaults to the Instance section', async () => {
  renderApp(<SettingsPage />, { route: '/settings', withAuth: false })
  expect(await screen.findByRole('heading', { name: /settings/i })).toBeInTheDocument()
  // all four sections are listed in the subnav
  for (const name of ['Instance', 'OIDC provider', 'CI federation', 'Appearance'])
    expect(screen.getByRole('link', { name })).toBeInTheDocument()
})

test('subnav switches sections via the URL', async () => {
  renderApp(<SettingsPage />, { route: '/settings?section=appearance', withAuth: false })
  expect(await screen.findByText(/theme/i)).toBeInTheDocument()
})
