import { describe, it, expect, beforeEach } from 'vitest'
import { BUILTIN_PRESETS, loadPresets, savePreset, removePreset } from './presets'

describe('audit presets', () => {
  beforeEach(() => localStorage.clear())
  it('built-ins produce filters (Failures 24h → result=error + a from)', () => {
    const f = BUILTIN_PRESETS.find((p) => /failures/i.test(p.name))!.filters()
    expect(f.result).toBe('error')
    expect(typeof f.from).toBe('string')
  })
  it('save/load/remove round-trips localStorage', () => {
    savePreset('Mine', { actor: 'alice' })
    expect(loadPresets().map((p) => p.name)).toContain('Mine')
    removePreset('Mine')
    expect(loadPresets().map((p) => p.name)).not.toContain('Mine')
  })
  it('corrupt storage degrades to empty list', () => {
    localStorage.setItem('janus.audit.presets', '{not json')
    expect(loadPresets()).toEqual([])
  })
})
