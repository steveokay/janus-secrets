import { render, screen } from '@testing-library/react'
import { EmptyState } from './EmptyState'

test('renders title, hint, icon and action', () => {
  render(
    <EmptyState
      icon={<svg data-testid="ico" />}
      title="Nothing here"
      hint="Try adding one."
      action={<button>Add</button>}
    />,
  )
  expect(screen.getByText('Nothing here')).toBeInTheDocument()
  expect(screen.getByText('Try adding one.')).toBeInTheDocument()
  expect(screen.getByTestId('ico')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Add' })).toBeInTheDocument()
})

test('omits icon wrap and hint when not provided', () => {
  const { container } = render(<EmptyState title="Bare" />)
  expect(screen.getByText('Bare')).toBeInTheDocument()
  expect(container.querySelector('.bg-brand-soft')).toBeNull()
})
