import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import type { PaletteItem } from './usePaletteItems'
import { CommandPalette } from './CommandPalette'

const ITEMS: PaletteItem[] = [
  { id: 'project:p1', group: 'Projects', label: 'api-gateway', keywords: 'api-gateway gw', to: '/projects/p1' },
  { id: 'config:c1', group: 'Configs', label: 'production', sublabel: 'Production', keywords: 'production prod', to: '/x' },
  { id: 'secret:c1:DATABASE_URL', group: 'Secrets', label: 'DATABASE_URL', keywords: 'DATABASE_URL', to: '/x' },
]

function setup(onSelect = vi.fn()) {
  render(<CommandPalette open items={ITEMS} onClose={vi.fn()} onSelect={onSelect} />)
  return onSelect
}

test('filters items by fuzzy query', async () => {
  setup()
  await userEvent.type(screen.getByRole('combobox', { name: /search/i }), 'prod')
  expect(screen.getByText('production')).toBeInTheDocument()
  expect(screen.queryByText('api-gateway')).not.toBeInTheDocument()
})

test('Enter selects the highlighted item', async () => {
  const onSelect = setup()
  const input = screen.getByRole('combobox', { name: /search/i })
  await userEvent.type(input, 'data')
  await userEvent.keyboard('{Enter}')
  expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ id: 'secret:c1:DATABASE_URL' }))
})

test('ArrowDown moves highlight before Enter', async () => {
  const onSelect = setup()
  const input = screen.getByRole('combobox', { name: /search/i })
  await userEvent.type(input, '{ArrowDown}') // empty query → all items; 0→1 (production)
  await userEvent.keyboard('{Enter}')
  expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ id: 'config:c1' }))
})

test('shows empty state when nothing matches', async () => {
  setup()
  await userEvent.type(screen.getByRole('combobox', { name: /search/i }), 'zzzzz')
  expect(screen.getByText(/no matches/i)).toBeInTheDocument()
})

test('input aria-activedescendant tracks the active option', async () => {
  setup()
  const input = screen.getByRole('combobox', { name: /search/i })
  const options = screen.getAllByRole('option')
  expect(input).toHaveAttribute('aria-activedescendant', options[0].id)
  await userEvent.type(input, '{ArrowDown}')
  expect(input).toHaveAttribute('aria-activedescendant', options[1].id)
})

test('reopening the palette clears a stale query', async () => {
  const onSelect = vi.fn()
  const { rerender } = render(<CommandPalette open items={ITEMS} onClose={vi.fn()} onSelect={onSelect} />)
  await userEvent.type(screen.getByRole('combobox', { name: /search/i }), 'prod')
  expect(screen.getByRole('combobox', { name: /search/i })).toHaveValue('prod')
  rerender(<CommandPalette open={false} items={ITEMS} onClose={vi.fn()} onSelect={onSelect} />)
  rerender(<CommandPalette open items={ITEMS} onClose={vi.fn()} onSelect={onSelect} />)
  expect(screen.getByRole('combobox', { name: /search/i })).toHaveValue('')
})
