import { expect, test } from 'vitest'
import { classifyImport } from './importClassify'
import type { MaskedSecret } from '../lib/endpoints'

const masked: Record<string, MaskedSecret> = {
  DB_URL: { value_version: 1, created_at: '', origin: 'own' },
}

test('classifies add vs update, sorted by key, carries no values', () => {
  const rows = classifyImport({ ZED: 'z', DB_URL: 'x', API: 'a' }, masked)
  expect(rows).toEqual([
    { key: 'API', kind: 'add' },
    { key: 'DB_URL', kind: 'update' },
    { key: 'ZED', kind: 'add' },
  ])
})

test('empty pairs -> empty list', () => {
  expect(classifyImport({}, masked)).toEqual([])
})
