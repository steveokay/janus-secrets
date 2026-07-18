import { describe, it, expect } from 'vitest'
import { assembleMatrix, roleTone } from './matrix'
import type { MemberScope } from '../lib/endpoints'

const inst = (uid: string, role: string) => ({ scope: { kind: 'instance' } as MemberScope, members: [{ user_id: uid, role }] as any })
const proj = (pid: string, uid: string, role: string) => ({ scope: { kind: 'project', pid } as MemberScope, members: [{ user_id: uid, role }] as any })
const env = (pid: string, eid: string, uid: string, role: string) => ({ scope: { kind: 'environment', pid, eid } as MemberScope, members: [{ user_id: uid, role }] as any })

describe('assembleMatrix', () => {
  const projects = [{ id: 'p1', name: 'App' }, { id: 'p2', name: 'Web' }]
  const users = [{ id: 'u1', email: 'alice@x.io', disabled: false }, { id: 'u2', email: 'bob@x.io', disabled: false }]

  it('places explicit instance + project roles in cells', () => {
    const m = assembleMatrix([inst('u1', 'owner'), proj('p1', 'u1', 'admin')], projects, users)
    expect(m.instanceRole.get('u1')).toBe('owner')
    expect(m.projectCells.get('u1')!.get('p1')).toEqual({ role: 'admin', envCount: 0 })
    expect(m.projectCells.get('u1')!.get('p2')).toEqual({ role: undefined, envCount: 0 })
  })

  it('counts environment bindings into the project cell badge', () => {
    const m = assembleMatrix([env('p1', 'e1', 'u2', 'developer'), env('p1', 'e2', 'u2', 'viewer')], projects, users)
    expect(m.projectCells.get('u2')!.get('p1')).toEqual({ role: undefined, envCount: 2 })
  })

  it('rows come from users, sorted by email', () => {
    const m = assembleMatrix([], projects, users)
    expect(m.rows.map((r) => r.email)).toEqual(['alice@x.io', 'bob@x.io'])
  })

  it('falls back to the union of binding user-ids when users list is absent', () => {
    const m = assembleMatrix([inst('zzz-user-id', 'viewer')], projects, undefined)
    expect(m.rows).toHaveLength(1)
    expect(m.rows[0].userId).toBe('zzz-user-id')
    expect(m.rows[0].email).toBe('zzz-user') // uid.slice(0,8)
  })

  it('roleTone maps each role to a token Pill tone', () => {
    expect(roleTone).toEqual({ viewer: 'muted', developer: 'info', admin: 'brand', owner: 'warning' })
  })
})
