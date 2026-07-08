import { render, screen } from '@testing-library/react'
import { AuthCard } from './AuthCard'

test('renders the Janus mark and its children', () => {
  render(<AuthCard><h1>Sign in</h1></AuthCard>)
  expect(screen.getByRole('heading', { name: 'Sign in' })).toBeInTheDocument()
  expect(screen.getByRole('img', { name: /janus logo/i })).toBeInTheDocument()
})
