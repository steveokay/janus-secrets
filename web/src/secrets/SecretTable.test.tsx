import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { SecretTable } from './SecretTable'
import type { MaskedSecret } from '../lib/endpoints'

const masked: Record<string, MaskedSecret> = {
  A: { value_version: 1, created_at: '2026-01-01T00:00:00Z', origin: 'own' },
  B: { value_version: 2, created_at: '2026-01-02T00:00:00Z', origin: 'own' },
}
function props(over = {}) {
  return {
    rows: ['A', 'B'], masked, buffer: {}, original: {}, editing: {}, revealed: {},
    sort: null, onSort: vi.fn(),
    selected: new Set<string>(), onToggleSelect: vi.fn(), onSelectAll: vi.fn(), active: null,
    onReveal: vi.fn(), onCopy: vi.fn(), onEdit: vi.fn(), onChangeValue: vi.fn(), onRemove: vi.fn(), onRevert: vi.fn(),
    lockedKeys: new Set<string>(), onToggleLock: vi.fn(), onOpenHistory: vi.fn(),
    ...over,
  }
}

test('clicking the Key header requests a key sort', async () => {
  const p = props()
  render(<SecretTable {...p} />)
  await userEvent.click(screen.getByRole('button', { name: /sort by key/i }))
  expect(p.onSort).toHaveBeenCalledWith('key')
})

test('header checkbox selects all visible', async () => {
  const p = props()
  render(<SecretTable {...p} />)
  await userEvent.click(screen.getByRole('checkbox', { name: /select all/i }))
  expect(p.onSelectAll).toHaveBeenCalledWith(['A', 'B'])
})
