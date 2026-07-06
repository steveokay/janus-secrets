import { useState } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ConfirmDialog } from './ConfirmDialog'

function Host({ onConfirm }: { onConfirm: () => void }) {
  const [open, setOpen] = useState(true)
  return (
    <ConfirmDialog
      open={open}
      onOpenChange={setOpen}
      title="Roll back to v2?"
      body="This creates a new version."
      confirmLabel="Roll back"
      onConfirm={onConfirm}
    />
  )
}

test('confirm fires callback; cancel closes without firing', async () => {
  const onConfirm = vi.fn()
  render(<Host onConfirm={onConfirm} />)
  expect(await screen.findByText('Roll back to v2?')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Roll back' }))
  expect(onConfirm).toHaveBeenCalledOnce()
})

test('cancel does not fire confirm', async () => {
  const onConfirm = vi.fn()
  render(<Host onConfirm={onConfirm} />)
  await userEvent.click(await screen.findByRole('button', { name: 'Cancel' }))
  expect(onConfirm).not.toHaveBeenCalled()
  expect(screen.queryByText('Roll back to v2?')).not.toBeInTheDocument()
})
