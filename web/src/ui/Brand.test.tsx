import { render, screen } from '@testing-library/react'
import { Brand } from './Brand'

test('renders the Janus mark and wordmark', () => {
  render(<Brand />)
  expect(screen.getByText('Janus')).toBeInTheDocument()
  expect(screen.getByRole('img', { name: /janus/i })).toBeInTheDocument()
})

test('mark-only mode omits the wordmark', () => {
  render(<Brand markOnly />)
  expect(screen.queryByText('Janus')).not.toBeInTheDocument()
})
