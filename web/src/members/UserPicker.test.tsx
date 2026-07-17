import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { UserPicker } from './UserPicker'

const candidates = [
  { id: 'u1', email: 'alice@x.io' },
  { id: 'u2', email: 'bob@x.io' },
]

describe('UserPicker', () => {
  it('filters candidates by email substring and selects on click', () => {
    const onChange = vi.fn()
    render(<UserPicker candidates={candidates} value="" onChange={onChange} />)
    const search = screen.getByLabelText(/search users/i)
    fireEvent.change(search, { target: { value: 'bob' } })
    expect(screen.queryByText('alice@x.io')).toBeNull()
    fireEvent.click(screen.getByText('bob@x.io'))
    expect(onChange).toHaveBeenCalledWith('u2')
  })

  it('shows a no-matches state', () => {
    render(<UserPicker candidates={candidates} value="" onChange={() => {}} />)
    fireEvent.change(screen.getByLabelText(/search users/i), { target: { value: 'zzz' } })
    expect(screen.getByText(/no users match/i)).toBeInTheDocument()
  })
})
