import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SortHeader } from './SortHeader'

function renderHeader(over: Partial<{ sortKey: string | null; sortDir: 'asc' | 'desc' }> = {}) {
  const toggleSort = vi.fn()
  render(
    <table>
      <thead>
        <tr>
          <SortHeader
            label="Name"
            sortKey="name"
            controls={{ sortKey: over.sortKey ?? null, sortDir: over.sortDir ?? 'asc', toggleSort }}
          />
        </tr>
      </thead>
    </table>,
  )
  return { toggleSort }
}

it('is inactive by default with aria-sort none', () => {
  renderHeader()
  expect(screen.getByRole('columnheader')).toHaveAttribute('aria-sort', 'none')
})

it('calls toggleSort with its key on click', async () => {
  const { toggleSort } = renderHeader()
  await userEvent.click(screen.getByRole('button', { name: /name/i }))
  expect(toggleSort).toHaveBeenCalledWith('name')
})

it('reflects the active ascending state in aria-sort', () => {
  renderHeader({ sortKey: 'name', sortDir: 'asc' })
  expect(screen.getByRole('columnheader')).toHaveAttribute('aria-sort', 'ascending')
})

it('reflects the active descending state in aria-sort', () => {
  renderHeader({ sortKey: 'name', sortDir: 'desc' })
  expect(screen.getByRole('columnheader')).toHaveAttribute('aria-sort', 'descending')
})
