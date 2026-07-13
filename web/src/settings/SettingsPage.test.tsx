import { http, HttpResponse } from 'msw'
import { beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { SettingsPage } from './SettingsPage'

// The default (Instance) section fires GET /v1/sys/seal-status on mount; without
// a handler that's an unhandled request under onUnhandledRequest:'error'. awskms
// avoids rendering any Shamir threshold/shares in the shell test.
beforeEach(() => server.use(
  http.get('/v1/sys/seal-status', () =>
    HttpResponse.json({ initialized: true, sealed: false, type: 'awskms' })),
))

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
