import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { Modal } from './Modal'

test('open modal shows an aria-modal dialog with its label and content', () => {
  render(<Modal open label="Demo" onClose={() => {}}><p>body</p></Modal>)
  const dlg = screen.getByRole('dialog', { name: 'Demo' })
  expect(dlg).toHaveAttribute('aria-modal', 'true')
  expect(screen.getByText('body')).toBeInTheDocument()
})

test('Escape triggers onClose', async () => {
  const onClose = vi.fn()
  render(<Modal open label="Demo" onClose={onClose}><p>body</p></Modal>)
  await userEvent.keyboard('{Escape}')
  expect(onClose).toHaveBeenCalled()
})

test('closed modal renders nothing', () => {
  render(<Modal open={false} label="Demo" onClose={() => {}}><p>body</p></Modal>)
  expect(screen.queryByText('body')).toBeNull()
})
