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

test('caller className overrides tone classes via cn()', () => {
  render(<Pill tone="success" className="bg-danger-soft">x</Pill>)
  const el = screen.getByText('x')
  expect(el).toHaveClass('bg-danger-soft')
  expect(el).not.toHaveClass('bg-success-soft')
})

test('no dot by default', () => {
  const { container } = render(<Pill tone="info">y</Pill>)
  expect(container.querySelector('[data-dot]')).toBeNull()
})
