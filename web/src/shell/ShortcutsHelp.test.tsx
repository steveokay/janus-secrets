import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ShortcutsHelp } from './ShortcutsHelp'

test('lists the app-wide and editor shortcut groups', () => {
  render(<ShortcutsHelp open onClose={() => {}} />)
  expect(screen.getByRole('dialog', { name: 'Keyboard shortcuts' })).toBeInTheDocument()
  expect(screen.getByText('Global')).toBeInTheDocument()
  expect(screen.getByText('Secret editor')).toBeInTheDocument()
  expect(screen.getByText('Open command palette')).toBeInTheDocument()
  expect(screen.getByText('Save pending changes')).toBeInTheDocument()
})

test('closed overlay renders nothing', () => {
  render(<ShortcutsHelp open={false} onClose={() => {}} />)
  expect(screen.queryByText('Keyboard shortcuts')).toBeNull()
})

test('Escape and the Modal close button both call onClose', async () => {
  const onClose = vi.fn()
  render(<ShortcutsHelp open onClose={onClose} />)
  await userEvent.click(screen.getByRole('button', { name: 'close' }))
  expect(onClose).toHaveBeenCalled()
})
