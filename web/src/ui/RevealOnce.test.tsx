import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ToastProvider } from './Toast'
import { RevealOnce } from './RevealOnce'

test('shows secret, copies with toast, closes explicitly', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.assign(navigator, { clipboard: { writeText } })
  const onClose = vi.fn()
  render(
    <ToastProvider>
      <RevealOnce open onClose={onClose} title="Service token" secret="janus_svc_abc" hint="Shown once." />
    </ToastProvider>,
  )
  expect(await screen.findByText('janus_svc_abc')).toBeInTheDocument()
  expect(screen.getByText('Shown once.')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Copy' }))
  expect(writeText).toHaveBeenCalledWith('janus_svc_abc')
  const copiedToast = await screen.findByText(/won't be shown again/)
  expect(copiedToast).toBeInTheDocument()
  // The success toast title is a fixed string and must never leak the secret value.
  expect(copiedToast).toHaveTextContent("Copied — store it now, it won't be shown again")
  expect(copiedToast.textContent).not.toContain('janus_svc_abc')
  await userEvent.click(screen.getByRole('button', { name: /stored it/i }))
  expect(onClose).toHaveBeenCalled()
})

test('pressing Escape invokes onClose', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.assign(navigator, { clipboard: { writeText } })
  const onClose = vi.fn()
  render(
    <ToastProvider>
      <RevealOnce open onClose={onClose} title="Service token" secret="janus_svc_abc" hint="Shown once." />
    </ToastProvider>,
  )
  await screen.findByText('janus_svc_abc')
  await userEvent.keyboard('{Escape}')
  expect(onClose).toHaveBeenCalled()
})
