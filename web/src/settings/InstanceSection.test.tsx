import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ToastProvider } from '../ui/Toast'
import { InstanceSection } from './InstanceSection'

function mount() {
  return renderApp(
    <ToastProvider><InstanceSection /></ToastProvider>,
    { route: '/settings?section=instance', withAuth: false },
  )
}

test('renders seal status (type + unsealed) and Shamir threshold/shares', async () => {
  server.use(
    http.get('/v1/sys/seal-status', () =>
      HttpResponse.json({ initialized: true, sealed: false, type: 'shamir', threshold: 3, shares: 5 })),
  )
  mount()
  // wait for the query-driven status card (the method value only appears once loaded)
  expect(await screen.findByText(/shamir/i)).toBeInTheDocument()
  // the "Unsealed" pill (not the static Seal-card copy)
  expect(screen.getByText(/^unsealed$/i)).toBeInTheDocument()
  // threshold-of-shares surfaced for Shamir
  expect(screen.getByText(/3\s*of\s*5/i)).toBeInTheDocument()
})

test('sealing requires typing SEAL and calls the endpoint', async () => {
  let sealed = false
  const reload = vi.fn()
  vi.stubGlobal('location', { ...window.location, reload })
  server.use(
    http.get('/v1/sys/seal-status', () =>
      HttpResponse.json({ initialized: true, sealed: false, type: 'shamir', threshold: 1, shares: 1 })),
    http.post('/v1/sys/seal', () => { sealed = true; return HttpResponse.json({ sealed: true }) }),
  )
  mount()
  await userEvent.click(await screen.findByRole('button', { name: /seal instance/i }))
  const confirm = screen.getByRole('button', { name: /^seal$/i })
  expect(confirm).toBeDisabled()
  await userEvent.type(screen.getByLabelText(/type SEAL/i), 'SEAL')
  expect(confirm).toBeEnabled()
  await userEvent.click(confirm)
  await waitFor(() => expect(sealed).toBe(true))
  await waitFor(() => expect(reload).toHaveBeenCalled())
  vi.unstubAllGlobals()
})

test('backup download surfaces an error toast on 403', async () => {
  server.use(
    http.get('/v1/sys/seal-status', () =>
      HttpResponse.json({ initialized: true, sealed: false, type: 'awskms' })),
    http.get('/v1/sys/backup', () =>
      HttpResponse.json({ error: { code: 'forbidden', message: 'You do not have permission to back up.' } }, { status: 403 })),
  )
  mount()
  await userEvent.click(await screen.findByRole('button', { name: /download backup/i }))
  expect(await screen.findByText(/permission to back up/i)).toBeInTheDocument()
})
