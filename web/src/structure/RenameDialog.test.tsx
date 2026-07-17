import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { RenameDialog } from './RenameDialog'

test('renders with the initial name pre-filled', () => {
  render(<RenameDialog title="Rename api-gateway" initial="api-gateway" onSubmit={() => {}} onClose={() => {}} />)
  expect(screen.getByRole('textbox', { name: /name/i })).toHaveValue('api-gateway')
})

test('typing a new name and clicking Save calls onSubmit with the trimmed name', async () => {
  const onSubmit = vi.fn()
  render(<RenameDialog title="Rename api-gateway" initial="api-gateway" onSubmit={onSubmit} onClose={() => {}} />)
  const input = screen.getByRole('textbox', { name: /name/i })
  await userEvent.clear(input)
  await userEvent.type(input, '  new-name  ')
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  expect(onSubmit).toHaveBeenCalledWith('new-name')
})

test('Save is disabled when the field is empty', async () => {
  render(<RenameDialog title="Rename api-gateway" initial="api-gateway" onSubmit={() => {}} onClose={() => {}} />)
  const input = screen.getByRole('textbox', { name: /name/i })
  await userEvent.clear(input)
  expect(screen.getByRole('button', { name: /save/i })).toBeDisabled()
})

test('Save is disabled when the name is unchanged from initial', () => {
  render(<RenameDialog title="Rename api-gateway" initial="api-gateway" onSubmit={() => {}} onClose={() => {}} />)
  expect(screen.getByRole('button', { name: /save/i })).toBeDisabled()
})

test('Cancel calls onClose', async () => {
  const onClose = vi.fn()
  render(<RenameDialog title="Rename api-gateway" initial="api-gateway" onSubmit={() => {}} onClose={onClose} />)
  await userEvent.click(screen.getByRole('button', { name: /cancel/i }))
  expect(onClose).toHaveBeenCalledOnce()
})

test('Escape calls onClose', async () => {
  const onClose = vi.fn()
  render(<RenameDialog title="Rename api-gateway" initial="api-gateway" onSubmit={() => {}} onClose={onClose} />)
  await userEvent.keyboard('{Escape}')
  expect(onClose).toHaveBeenCalled()
})
