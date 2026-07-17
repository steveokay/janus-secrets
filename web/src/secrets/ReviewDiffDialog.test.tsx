import { render, screen } from '@testing-library/react'
import { expect, test, vi } from 'vitest'
import { ReviewDiffDialog } from './ReviewDiffDialog'
import type { MaskedSecret } from '../lib/endpoints'
import type { Buffer } from './dirty'

function props(over = {}) {
  return {
    open: true,
    onClose: vi.fn(),
    buffer: {} as Buffer,
    masked: {} as Record<string, MaskedSecret>,
    original: {} as Record<string, string>,
    version: 3,
    saving: false,
    onSave: vi.fn(),
    ...over,
  }
}

test('shows a type change for an edited key whose type changed, value-free', () => {
  const masked: Record<string, MaskedSecret> = {
    API_KEY: { value_version: 1, created_at: '2026-01-01T00:00:00Z', origin: 'own', type: 'string' },
  }
  const original: Record<string, string> = { API_KEY: '{"a":1}' }
  const buffer: Buffer = { API_KEY: { value: '{"a":1}', type: 'json' } }

  render(<ReviewDiffDialog {...props({ masked, original, buffer })} />)

  // Row is listed under Changed
  expect(screen.getByText('API_KEY')).toBeInTheDocument()
  // Type change indicator: string -> json
  expect(screen.getByText(/string\s*→\s*json/)).toBeInTheDocument()

  // Value-free: never render the actual secret value
  expect(screen.queryByText('{"a":1}')).not.toBeInTheDocument()
})

test('does not show a type indicator when only the value changed', () => {
  const masked: Record<string, MaskedSecret> = {
    API_KEY: { value_version: 1, created_at: '2026-01-01T00:00:00Z', origin: 'own', type: 'string' },
  }
  const original: Record<string, string> = { API_KEY: 'old-value' }
  const buffer: Buffer = { API_KEY: { value: 'new-value' } }

  render(<ReviewDiffDialog {...props({ masked, original, buffer })} />)

  expect(screen.getByText('API_KEY')).toBeInTheDocument()
  expect(screen.queryByText(/→/)).not.toBeInTheDocument()
})

test('treats an absent server type as string when reporting a type change', () => {
  const masked: Record<string, MaskedSecret> = {
    NOTES: { value_version: 1, created_at: '2026-01-01T00:00:00Z', origin: 'own' },
  }
  const original: Record<string, string> = { NOTES: 'hello' }
  const buffer: Buffer = { NOTES: { value: 'hello', type: 'note' } }

  render(<ReviewDiffDialog {...props({ masked, original, buffer })} />)

  expect(screen.getByText(/string\s*→\s*note/)).toBeInTheDocument()
})
