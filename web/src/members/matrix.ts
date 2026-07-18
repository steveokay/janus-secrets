import type { Tone } from '../ui/Pill'
import type { Member, MemberRole, MemberScope, UserInfo } from '../lib/endpoints'

export const roleTone: Record<MemberRole, Tone> = {
  viewer: 'muted',
  developer: 'info',
  admin: 'brand',
  owner: 'warning',
}

export interface ScopeMembers {
  scope: MemberScope
  members: Member[]
}

export interface Cell {
  role?: MemberRole
  envCount: number
}

export interface MatrixModel {
  rows: { userId: string; email: string }[]
  columns: { id: string; name: string }[]
  instanceRole: Map<string, MemberRole>
  /** userId -> pid -> Cell (every user row has an entry for every column). */
  projectCells: Map<string, Map<string, Cell>>
}

/**
 * Assemble the read-only RBAC matrix model from per-scope member lists.
 * EXPLICIT bindings only — no effective/inherited resolution. Env-level
 * bindings contribute to the owning project cell's envCount badge.
 */
export function assembleMatrix(
  scopeMembers: ScopeMembers[],
  projects: { id: string; name: string }[],
  users: UserInfo[] | undefined,
): MatrixModel {
  const instanceRole = new Map<string, MemberRole>()
  const projectCells = new Map<string, Map<string, Cell>>()
  const seenUsers = new Set<string>()

  const cellFor = (uid: string, pid: string): Cell => {
    let byPid = projectCells.get(uid)
    if (!byPid) {
      byPid = new Map()
      projectCells.set(uid, byPid)
      for (const p of projects) byPid.set(p.id, { role: undefined, envCount: 0 })
    }
    return byPid.get(pid)!
  }

  for (const { scope, members } of scopeMembers) {
    for (const m of members) {
      seenUsers.add(m.user_id)
      if (scope.kind === 'instance') {
        instanceRole.set(m.user_id, m.role)
      } else if (scope.kind === 'project') {
        cellFor(m.user_id, scope.pid).role = m.role
      } else {
        cellFor(m.user_id, scope.pid).envCount += 1
      }
    }
  }

  const byId = new Map((users ?? []).map((u) => [u.id, u]))
  const rowIds = users ? users.filter((u) => !u.disabled).map((u) => u.id) : [...seenUsers]
  const rows = rowIds
    .map((userId) => ({ userId, email: byId.get(userId)?.email ?? userId.slice(0, 8) }))
    .sort((a, b) => a.email.localeCompare(b.email))

  // Guarantee every row has a full column map so the component can index safely.
  for (const r of rows) if (!projectCells.has(r.userId)) cellFor(r.userId, projects[0]?.id ?? '')

  return { rows, columns: projects, instanceRole, projectCells }
}
