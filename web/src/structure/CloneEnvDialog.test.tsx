import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { CloneEnvDialog } from './CloneEnvDialog'

test('renders slug and name inputs', () => {
  render(<CloneEnvDialog onSubmit={() => {}} onClose={() => {}} />)
  expect(screen.getByRole('textbox', { name: /slug/i })).toBeInTheDocument()
  expect(screen.getByRole('textbox', { name: /name/i })).toBeInTheDocument()
})

test('Save is disabled until slug is non-empty', async () => {
  render(<CloneEnvDialog onSubmit={() => {}} onClose={() => {}} />)
  expect(screen.getByRole('button', { name: /save/i })).toBeDisabled()
  await userEvent.type(screen.getByRole('textbox', { name: /slug/i }), 'dev-2')
  expect(screen.getByRole('button', { name: /save/i })).not.toBeDisabled()
})

test('submitting calls onSubmit with the entered slug and name', async () => {
  const onSubmit = vi.fn()
  render(<CloneEnvDialog onSubmit={onSubmit} onClose={() => {}} />)
  await userEvent.type(screen.getByRole('textbox', { name: /slug/i }), 'dev-2')
  await userEvent.type(screen.getByRole('textbox', { name: /name/i }), 'Development 2')
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  expect(onSubmit).toHaveBeenCalledWith('dev-2', 'Development 2')
})

test('Cancel calls onClose', async () => {
  const onClose = vi.fn()
  render(<CloneEnvDialog onSubmit={() => {}} onClose={onClose} />)
  await userEvent.click(screen.getByRole('button', { name: /cancel/i }))
  expect(onClose).toHaveBeenCalledOnce()
})

test('Escape calls onClose', async () => {
  const onClose = vi.fn()
  render(<CloneEnvDialog onSubmit={() => {}} onClose={onClose} />)
  await userEvent.keyboard('{Escape}')
  expect(onClose).toHaveBeenCalled()
})
