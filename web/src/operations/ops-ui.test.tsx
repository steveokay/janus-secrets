import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { StatusPill, RelTime, LastError, OpsTable } from './ops-ui'

test('StatusPill maps engine states to tones/text', () => {
  render(<StatusPill status="failed" />)
  expect(screen.getByText('failed')).toBeInTheDocument()
})

test('RelTime renders a relative string for a recent time', () => {
  const iso = new Date(Date.now() - 3 * 60_000).toISOString()
  render(<RelTime iso={iso} />)
  expect(screen.getByText(/3m ago|just now|2m ago/)).toBeInTheDocument()
})

test('LastError shows a warning marker only when text present', () => {
  const { rerender } = render(<LastError text={null} />)
  expect(screen.queryByLabelText('last error')).toBeNull()
  rerender(<LastError text="apply failed" />)
  expect(screen.getByLabelText('last error')).toBeInTheDocument()
})

test('LastError toggles the full error text inline on click', async () => {
  const full = 'apply failed: upstream provider rejected the request'
  render(<LastError text={full} />)
  const toggle = screen.getByLabelText('last error')
  // collapsed: full text not rendered, aria-expanded false
  expect(screen.queryByText(full)).toBeNull()
  expect(toggle).toHaveAttribute('aria-expanded', 'false')
  // expand
  await userEvent.click(toggle)
  expect(screen.getByText(full)).toBeInTheDocument()
  expect(toggle).toHaveAttribute('aria-expanded', 'true')
  // collapse again
  await userEvent.click(toggle)
  expect(screen.queryByText(full)).toBeNull()
})

test('OpsTable renders forbidden EmptyState when allForbidden', () => {
  render(
    <OpsTable columns={['A']} isLoading={false} isError={false} allForbidden isEmpty={false} forbiddenHint="ask an admin">
      <tr><td>x</td></tr>
    </OpsTable>,
  )
  expect(screen.getByText(/access required/i)).toBeInTheDocument()
})
