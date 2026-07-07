import { render, screen } from '@testing-library/react'
import { Input } from './Input'
import { Textarea } from './Textarea'
import { Select } from './Select'

test('Input associates its label and shows error with aria-invalid', () => {
  render(<Input label="Slug" error="required" value="" onChange={() => {}} />)
  const field = screen.getByLabelText('Slug')
  expect(field).toHaveAttribute('aria-invalid', 'true')
  expect(screen.getByText('required')).toBeInTheDocument()
})
test('Input without error has no aria-invalid', () => {
  render(<Input label="Name" value="" onChange={() => {}} />)
  expect(screen.getByLabelText('Name')).not.toHaveAttribute('aria-invalid')
})
test('Textarea associates its label', () => {
  render(<Textarea label="Notes" value="" onChange={() => {}} />)
  expect(screen.getByLabelText('Notes').tagName).toBe('TEXTAREA')
})
test('Select renders options and its label', () => {
  render(<Select label="Base"><option value="a">a</option></Select>)
  expect(screen.getByLabelText('Base')).toBeInTheDocument()
  expect(screen.getByRole('option', { name: 'a' })).toBeInTheDocument()
})
