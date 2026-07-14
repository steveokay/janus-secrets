import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { OpsSelectionBar } from './OpsSelectionBar'

test('renders count and fires a danger action + clear', async () => {
  const onDelete = vi.fn()
  const onClear = vi.fn()
  render(
    <OpsSelectionBar
      count={2}
      onClear={onClear}
      actions={[{ label: 'Delete', tone: 'danger', onClick: onDelete }]}
    />,
  )
  expect(screen.getByText('2 selected')).toBeInTheDocument()

  await userEvent.click(screen.getByRole('button', { name: /delete/i }))
  expect(onDelete).toHaveBeenCalledTimes(1)

  await userEvent.click(screen.getByRole('button', { name: /clear/i }))
  expect(onClear).toHaveBeenCalledTimes(1)
})
