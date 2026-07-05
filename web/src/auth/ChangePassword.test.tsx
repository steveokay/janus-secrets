import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ChangePasswordForm } from './ChangePassword'

test('posts current + new password and calls onDone', async () => {
  let body: any
  server.use(http.post('/v1/auth/password', async ({ request }) => { body = await request.json(); return new HttpResponse(null, { status: 204 }) }))
  const onDone = vi.fn()
  renderApp(<ChangePasswordForm onDone={onDone} onClose={() => {}} />, { withAuth: false })
  await userEvent.type(screen.getByLabelText(/current password/i), 'old')
  await userEvent.type(screen.getByLabelText(/new password/i), 'newpw')
  await userEvent.click(screen.getByRole('button', { name: /change password/i }))
  await waitFor(() => expect(onDone).toHaveBeenCalled())
  expect(body).toEqual({ current_password: 'old', new_password: 'newpw' })
})
