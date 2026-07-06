import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { UnsealPage } from './UnsealPage'

test('shamir: submitting shares advances progress then calls onUnsealed', async () => {
  const status = { initialized: true, sealed: true, type: 'shamir', threshold: 2, shares: 3, progress: { submitted: 0, required: 2 } }
  server.use(
    http.get('/v1/sys/seal-status', () => HttpResponse.json(status)),
    http.post('/v1/sys/unseal', () => {
      status.progress.submitted += 1
      if (status.progress.submitted >= status.threshold) status.sealed = false
      return HttpResponse.json(status)
    }),
  )
  const onUnsealed = vi.fn()
  renderApp(<UnsealPage onUnsealed={onUnsealed} />, { withAuth: false })

  await screen.findByText(/0 of 2/i)
  await userEvent.type(screen.getByLabelText(/unseal key share/i), 'share-1')
  await userEvent.click(screen.getByRole('button', { name: /submit share/i }))
  await screen.findByText(/1 of 2/i)
  await userEvent.type(screen.getByLabelText(/unseal key share/i), 'share-2')
  await userEvent.click(screen.getByRole('button', { name: /submit share/i }))
  await waitFor(() => expect(onUnsealed).toHaveBeenCalled())
})

test('kms: auto-unsealed status calls onUnsealed without a share input', async () => {
  server.use(http.get('/v1/sys/seal-status', () =>
    HttpResponse.json({ initialized: true, sealed: false, type: 'awskms' })))
  const onUnsealed = vi.fn()
  renderApp(<UnsealPage onUnsealed={onUnsealed} />, { withAuth: false })
  await waitFor(() => expect(onUnsealed).toHaveBeenCalled())
  expect(screen.queryByLabelText(/unseal key share/i)).toBeNull()
})
