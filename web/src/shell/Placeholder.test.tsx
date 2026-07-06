import { render, screen } from '@testing-library/react'
import { Placeholder } from './Placeholder'

test('names the feature and marks it as coming later', () => {
  render(<Placeholder feature="Audit viewer" />)
  expect(screen.getByText(/audit viewer/i)).toBeInTheDocument()
  expect(screen.getByText(/coming in a later/i)).toBeInTheDocument()
})
