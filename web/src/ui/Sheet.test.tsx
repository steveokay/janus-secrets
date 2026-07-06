import { useState } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Sheet } from './Sheet'

function Host() {
  const [open, setOpen] = useState(true)
  return <Sheet open={open} onOpenChange={setOpen} title="Version history"><p>content here</p></Sheet>
}

test('renders title and children; close button dismisses', async () => {
  render(<Host />)
  expect(await screen.findByText('Version history')).toBeInTheDocument()
  expect(screen.getByText('content here')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /close/i }))
  expect(screen.queryByText('Version history')).not.toBeInTheDocument()
})
