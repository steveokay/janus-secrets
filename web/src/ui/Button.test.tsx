import { render, screen } from '@testing-library/react'
import { Button } from './Button'

test('primary variant renders brand background and is a button', () => {
  render(<Button>Save</Button>)
  const b = screen.getByRole('button', { name: 'Save' })
  expect(b.className).toContain('bg-brand')
})

test('loading disables the button and shows a spinner', () => {
  render(<Button loading>Save</Button>)
  const b = screen.getByRole('button', { name: /save/i })
  expect(b).toBeDisabled()
  expect(b.querySelector('svg')).toBeTruthy()
})

test('danger + sm variant applies the mapped classes', () => {
  render(<Button variant="danger" size="sm">Delete</Button>)
  const b = screen.getByRole('button', { name: 'Delete' })
  expect(b.className).toContain('text-danger')
  expect(b.className).toContain('text-[12px]')
})
