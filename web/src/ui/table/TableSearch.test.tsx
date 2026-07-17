import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TableSearch } from './TableSearch'

it('forwards typed input to onChange', async () => {
  const onChange = vi.fn()
  render(<TableSearch value="" onChange={onChange} matched={5} total={5} label="search tokens" />)
  await userEvent.type(screen.getByLabelText('search tokens'), 'ci')
  expect(onChange).toHaveBeenCalled()
  expect(onChange.mock.calls.at(-1)?.[0]).toBe('i') // last keystroke value (controlled input stays '')
})

it('hides the count when empty and shows "N of M" when searching', () => {
  const { rerender } = render(
    <TableSearch value="" onChange={() => {}} matched={5} total={5} label="search tokens" />,
  )
  expect(screen.queryByText(/of/)).toBeNull()
  rerender(<TableSearch value="ci" onChange={() => {}} matched={2} total={5} label="search tokens" />)
  expect(screen.getByText('2 of 5')).toBeInTheDocument()
})

it('clears on Escape', async () => {
  const onChange = vi.fn()
  render(<TableSearch value="ci" onChange={onChange} matched={2} total={5} label="search tokens" />)
  const input = screen.getByLabelText('search tokens')
  input.focus()
  await userEvent.keyboard('{Escape}')
  expect(onChange).toHaveBeenCalledWith('')
})
