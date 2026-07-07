import { render, screen } from '@testing-library/react'
import { Card } from './Card'
import { Skeleton } from './Skeleton'

test('Card renders children on the card surface', () => {
  render(<Card>hi</Card>)
  const el = screen.getByText('hi')
  expect(el.className).toContain('bg-card')
  expect(el.className).toContain('rounded-card')
})
test('Card merges an extra className', () => {
  render(<Card className="p-4">x</Card>)
  expect(screen.getByText('x').className).toContain('p-4')
})
test('Skeleton is decorative and animated', () => {
  const { container } = render(<Skeleton className="h-4 w-10" />)
  const el = container.firstChild as HTMLElement
  expect(el).toHaveAttribute('aria-hidden')
  expect(el.className).toContain('animate-pulse')
  expect(el.className).toContain('bg-line-soft')
  expect(el.className).toContain('h-4')
})
