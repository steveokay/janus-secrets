import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Tooltip } from './Tooltip'

test('reveals its content when the trigger is focused', async () => {
  render(<Tooltip content="Copy value"><button>icon</button></Tooltip>)
  await userEvent.tab() // focus the trigger button
  const matches = await screen.findAllByText('Copy value')
  expect(matches.length).toBeGreaterThan(0)
})
test('renders the trigger child', () => {
  render(<Tooltip content="x"><button>icon</button></Tooltip>)
  expect(screen.getByRole('button', { name: 'icon' })).toBeInTheDocument()
})
