import { render, screen } from '@testing-library/react'
import { Pill } from './Pill'

test('renders children with tone classes', () => {
  render(<Pill tone="success">Unsealed</Pill>)
  const el = screen.getByText('Unsealed')
  expect(el).toHaveClass('bg-success-soft', 'text-success')
})

test('renders a status dot when asked', () => {
  const { container } = render(<Pill tone="danger" dot>Sealed</Pill>)
  expect(container.querySelector('[data-dot]')).toBeInTheDocument()
})
