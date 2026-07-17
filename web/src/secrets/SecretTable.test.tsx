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
    lockedKeys: new Set<string>(), onToggleLock: vi.fn(), onOpenHistory: vi.fn(), onChangeType: vi.fn(),
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

test('a json-typed row renders a textarea for the value when editing', async () => {
  const maskedJson: Record<string, MaskedSecret> = {
    A: { value_version: 1, created_at: '2026-01-01T00:00:00Z', origin: 'own', type: 'json' },
  }
  const p = props({ rows: ['A'], masked: maskedJson, editing: { A: true } })
  render(<SecretTable {...p} />)
  const field = screen.getByRole('textbox', { name: /value for a/i })
  expect(field.tagName).toBe('TEXTAREA')
  expect(field).toHaveClass('font-mono')
})

test('a string-typed row renders a single-line input for the value when editing', async () => {
  const p = props({ rows: ['A'], editing: { A: true } }) // masked.A has no type -> defaults to string
  render(<SecretTable {...p} />)
  const field = screen.getByRole('textbox', { name: /value for a/i })
  expect(field.tagName).toBe('INPUT')
})

test('a password-typed row renders a single-line input for the value when editing', async () => {
  const maskedPw: Record<string, MaskedSecret> = {
    A: { value_version: 1, created_at: '2026-01-01T00:00:00Z', origin: 'own', type: 'password' },
  }
  const p = props({ rows: ['A'], masked: maskedPw, editing: { A: true } })
  render(<SecretTable {...p} />)
  const field = screen.getByRole('textbox', { name: /value for a/i })
  expect(field.tagName).toBe('INPUT')
})

test('a type selector is present per row and reports changes via onChangeType', async () => {
  const p = props({ rows: ['A'] })
  render(<SecretTable {...p} />)
  const select = screen.getByRole('combobox', { name: /type for a/i })
  await userEvent.selectOptions(select, 'json')
  expect(p.onChangeType).toHaveBeenCalledWith('A', 'json')
})

test('the type selector reflects a buffered type override', () => {
  const p = props({ rows: ['A'], buffer: { A: { value: null, type: 'ssh_key' } } })
  render(<SecretTable {...p} />)
  const select = screen.getByRole('combobox', { name: /type for a/i }) as HTMLSelectElement
  expect(select.value).toBe('ssh_key')
})

test('a password-typed row shows a Generate button that emits a new value via onChangeValue', async () => {
  const maskedPw: Record<string, MaskedSecret> = {
    A: { value_version: 1, created_at: '2026-01-01T00:00:00Z', origin: 'own', type: 'password' },
  }
  const p = props({ rows: ['A'], masked: maskedPw, editing: { A: true } })
  render(<SecretTable {...p} />)
  await userEvent.click(screen.getByRole('button', { name: /generate/i }))
  expect(p.onChangeValue).toHaveBeenCalledWith('A', expect.any(String))
  const [, generated] = p.onChangeValue.mock.calls[0]
  expect(generated.length).toBeGreaterThan(0)
})

test('a json row with an invalid revealed value shows a non-blocking warning, not shown when valid', () => {
  const maskedJson: Record<string, MaskedSecret> = {
    A: { value_version: 1, created_at: '2026-01-01T00:00:00Z', origin: 'own', type: 'json' },
  }
  const invalid = props({ rows: ['A'], masked: maskedJson, revealed: { A: 'not json' } })
  const { unmount } = render(<SecretTable {...invalid} />)
  expect(screen.getByText(/not valid json/i)).toBeInTheDocument()
  unmount()

  const valid = props({ rows: ['A'], masked: maskedJson, revealed: { A: '{"a":1}' } })
  render(<SecretTable {...valid} />)
  expect(screen.queryByText(/not valid json/i)).not.toBeInTheDocument()
})

test('a json row with an unrevealed (masked) value shows no validation warning', () => {
  const maskedJson: Record<string, MaskedSecret> = {
    A: { value_version: 1, created_at: '2026-01-01T00:00:00Z', origin: 'own', type: 'json' },
  }
  const p = props({ rows: ['A'], masked: maskedJson })
  render(<SecretTable {...p} />)
  expect(screen.queryByText(/not valid json/i)).not.toBeInTheDocument()
})
