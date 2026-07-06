import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { UserMenu } from './UserMenu'

function mockMe() {
  server.use(http.get('/v1/auth/me', () => HttpResponse.json({ email: 'steve@acme.dev' })))
}

test('shows initials; opens menu with email, change password, log out', async () => {
  mockMe()
  renderApp(<UserMenu />)
  const trigger = await screen.findByRole('button', { name: /user menu/i })
  expect(trigger).toHaveTextContent('ST') // first two letters of local part, uppercased
  await userEvent.click(trigger)
  expect(await screen.findByText('steve@acme.dev')).toBeInTheDocument()
  expect(screen.getByRole('menuitem', { name: /change password/i })).toBeInTheDocument()
  expect(screen.getByRole('menuitem', { name: /log out/i })).toBeInTheDocument()
})

test('change password opens the dialog', async () => {
  mockMe()
  renderApp(<UserMenu />)
  await userEvent.click(await screen.findByRole('button', { name: /user menu/i }))
  await userEvent.click(screen.getByRole('menuitem', { name: /change password/i }))
  expect(await screen.findByRole('heading', { name: /change password/i })).toBeInTheDocument()
})
