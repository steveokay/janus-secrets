import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ToastProvider, useToast } from './Toast'

function Pusher() {
  const toast = useToast()
  return (
    <>
      <button onClick={() => toast({ title: 'Saved as v2' })}>ok</button>
      <button onClick={() => toast({ title: 'Failed.', tone: 'danger' })}>bad</button>
    </>
  )
}

test('pushes success and danger toasts', async () => {
  render(<ToastProvider><Pusher /></ToastProvider>)
  await userEvent.click(screen.getByRole('button', { name: 'ok' }))
  expect(await screen.findByText('Saved as v2')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'bad' }))
  expect(await screen.findByText('Failed.')).toBeInTheDocument()
})
