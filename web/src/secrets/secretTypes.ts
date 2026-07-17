import { Key, KeyRound, Braces, TerminalSquare, BadgeCheck, FileText } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'

export type SecretType = 'string' | 'password' | 'json' | 'ssh_key' | 'certificate' | 'note'

export interface SecretTypeSpec {
  label: string
  icon: LucideIcon
  multiline: boolean
  monospace: boolean
  generate?: boolean
  /** Returns an error string when invalid, or null when ok. Warn-only. */
  validate?: (v: string) => string | null
}

export const SECRET_TYPES: Record<SecretType, SecretTypeSpec> = {
  string:      { label: 'Value',       icon: Key,            multiline: false, monospace: true },
  password:    { label: 'Password',    icon: KeyRound,       multiline: false, monospace: true, generate: true },
  json:        { label: 'JSON',        icon: Braces,         multiline: true,  monospace: true, validate: (v) => { try { JSON.parse(v); return null } catch { return 'Not valid JSON' } } },
  ssh_key:     { label: 'SSH key',     icon: TerminalSquare, multiline: true,  monospace: true },
  certificate: { label: 'Certificate', icon: BadgeCheck,     multiline: true,  monospace: true, validate: (v) => /-----BEGIN [^-]+-----[\s\S]*-----END [^-]+-----/.test(v) ? null : 'Missing PEM BEGIN/END block' },
  note:        { label: 'Note',        icon: FileText,       multiline: true,  monospace: true },
}

export const SECRET_TYPE_ORDER: SecretType[] = ['string', 'password', 'json', 'ssh_key', 'certificate', 'note']

export function normalizeType(t: string | undefined | null): SecretType {
  return t && (t in SECRET_TYPES) ? (t as SecretType) : 'string'
}
