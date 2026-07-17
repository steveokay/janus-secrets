import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useSearchParams } from 'react-router-dom'
import {
  endpoints,
  Member,
  MemberRole,
  MemberScope,
  UserInfo,
  memberScopePath,
} from '../lib/endpoints'
import { ApiError, apiErrorTitle } from '../lib/api'
import { useProjects, useEnvironments } from '../secrets/nav'
import { Pill } from '../ui/Pill'
import { Button } from '../ui/Button'
import { Sheet } from '../ui/Sheet'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { EmptyState } from '../ui/EmptyState'
import { RevealOnce } from '../ui/RevealOnce'
import { useToast } from '../ui/Toast'
import { useTitle } from '../lib/title'
import { useTableControls } from '../ui/table/useTableControls'
import { SortHeader } from '../ui/table/SortHeader'
import { TableSearch } from '../ui/table/TableSearch'
import { UserPicker } from './UserPicker'
import { RbacMatrix } from './RbacMatrix'

const ROLES: MemberRole[] = ['viewer', 'developer', 'admin', 'owner']

type ScopeKind = MemberScope['kind']

// Resolve a member's user_id to an email using the (best-effort) users list;
// falls back to a truncated id when the users list is unavailable or the user
// is unknown to it.
function displayName(uid: string, byId: Map<string, UserInfo>): string {
  return byId.get(uid)?.email ?? uid.slice(0, 8)
}

function AddMemberSheet({ scope, members, users, onClose }: {
  scope: MemberScope
  members: Member[]
  users: UserInfo[]
  onClose: () => void
}) {
  const toast = useToast()
  const qc = useQueryClient()
  const existing = new Set(members.map((m) => m.user_id))
  const candidates = users.filter((u) => !u.disabled && !existing.has(u.id))
  const [uid, setUid] = useState('')
  const [role, setRole] = useState<MemberRole>('viewer')

  const mutation = useMutation({
    mutationFn: () => endpoints.putMember(scope, uid, role),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['members', memberScopePath(scope)] })
      toast({ title: 'Member added' })
      onClose()
    },
    onError: (e) => toast({ title: apiErrorTitle(e), tone: 'danger' }),
  })

  return (
    <Sheet open onOpenChange={(o) => { if (!o) onClose() }} title="Add member">
      <form
        onSubmit={(e) => { e.preventDefault(); mutation.mutate() }}
        className="flex flex-col gap-3"
      >
        <label className="text-[12px] font-semibold">
          User
          <UserPicker
            candidates={candidates.map((u) => ({ id: u.id, email: u.email }))}
            value={uid}
            onChange={setUid}
          />
        </label>
        <label className="text-[12px] font-semibold">
          Role
          <select
            aria-label="role"
            value={role}
            onChange={(e) => setRole(e.target.value as MemberRole)}
            className="mt-1 w-full rounded border border-line bg-surface-3 px-3 py-2 text-[13px] font-normal text-ink focus:border-brand-line focus:shadow-glow-soft transition-nocturne"
          >
            {ROLES.map((r) => <option key={r} value={r}>{r}</option>)}
          </select>
        </label>
        {mutation.isError && (
          <p role="alert" className="text-[12.5px] text-danger">{apiErrorTitle(mutation.error)}</p>
        )}
        <div className="flex justify-end gap-2 pt-2">
          <Button type="button" variant="secondary" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" size="sm" disabled={!uid || mutation.isPending}>
            Add
          </Button>
        </div>
      </form>
    </Sheet>
  )
}

function CreateUserSheet({ onClose, onCreated }: {
  onClose: () => void
  onCreated: (password: string) => void
}) {
  const toast = useToast()
  const qc = useQueryClient()
  const [email, setEmail] = useState('')

  const mutation = useMutation({
    mutationFn: () => endpoints.createUser(email),
    onSuccess: (r) => {
      void qc.invalidateQueries({ queryKey: ['users'] })
      // Neutral confirmation only — the initial password is shown once via
      // RevealOnce, NEVER in a toast title.
      toast({ title: 'User created' })
      onCreated(r.password)
    },
    onError: (e) => toast({ title: apiErrorTitle(e), tone: 'danger' }),
  })

  return (
    <Sheet open onOpenChange={(o) => { if (!o) onClose() }} title="Create user">
      <form
        onSubmit={(e) => { e.preventDefault(); mutation.mutate() }}
        className="flex flex-col gap-3"
      >
        <label className="text-[12px] font-semibold">
          Email
          <input
            aria-label="email"
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            required
            className="mt-1 w-full rounded border border-line bg-surface-3 px-3 py-2 text-[13px] font-normal text-ink focus:border-brand-line focus:shadow-glow-soft transition-nocturne"
          />
        </label>
        {mutation.isError && (
          <p role="alert" className="text-[12.5px] text-danger">{apiErrorTitle(mutation.error)}</p>
        )}
        <div className="flex justify-end gap-2 pt-2">
          <Button type="button" variant="secondary" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" size="sm" disabled={email.trim() === '' || mutation.isPending}>
            Create
          </Button>
        </div>
      </form>
    </Sheet>
  )
}

export function MembersPage() {
  useTitle('Members')
  const qc = useQueryClient()
  const toast = useToast()

  const [scopeKind, setScopeKind] = useState<ScopeKind>('instance')
  const [pid, setPid] = useState('')
  const [eid, setEid] = useState('')

  const [params, setParams] = useSearchParams()
  const view = params.get('view') === 'matrix' ? 'matrix' : 'list'
  const setView = (v: 'list' | 'matrix') => {
    const next = new URLSearchParams(params)
    if (v === 'matrix') next.set('view', 'matrix')
    else next.delete('view')
    setParams(next, { replace: true })
  }

  const projects = useProjects()
  const envs = useEnvironments(scopeKind === 'environment' ? pid || undefined : undefined)

  const scope: MemberScope | null =
    scopeKind === 'instance'
      ? { kind: 'instance' }
      : scopeKind === 'project'
        ? (pid ? { kind: 'project', pid } : null)
        : (pid && eid ? { kind: 'environment', pid, eid } : null)

  const scopePath = scope ? memberScopePath(scope) : 'none'

  const members = useQuery({
    queryKey: ['members', scopePath],
    queryFn: () => endpoints.listMembers(scope!),
    enabled: !!scope,
  })

  // Best-effort: a caller with member-read but not user-manage still gets a
  // usable page — emails degrade to id prefixes and the Users section hides.
  const users = useQuery({ queryKey: ['users'], queryFn: endpoints.listUsers, retry: false })
  const usersList = users.data ?? []
  const usersById = new Map(usersList.map((u) => [u.id, u]))
  const usersAvailable = users.isSuccess

  const [addOpen, setAddOpen] = useState(false)
  const [createOpen, setCreateOpen] = useState(false)
  const [newPassword, setNewPassword] = useState<string | null>(null)
  const [pendingRole, setPendingRole] = useState<{ uid: string; role: MemberRole; label: string } | null>(null)
  const [removeTarget, setRemoveTarget] = useState<{ uid: string; label: string } | null>(null)
  const [disableTarget, setDisableTarget] = useState<UserInfo | null>(null)

  const roleMut = useMutation({
    mutationFn: ({ uid, role }: { uid: string; role: MemberRole }) => endpoints.putMember(scope!, uid, role),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['members', scopePath] })
      toast({ title: 'Member updated' })
      setPendingRole(null)
    },
    onError: (e) => {
      toast({ title: apiErrorTitle(e), tone: 'danger' })
      setPendingRole(null)
    },
  })

  const removeMut = useMutation({
    mutationFn: (uid: string) => endpoints.deleteMember(scope!, uid),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['members', scopePath] })
      toast({ title: 'Member removed' })
      setRemoveTarget(null)
    },
    onError: (e) => {
      toast({ title: apiErrorTitle(e), tone: 'danger' })
      setRemoveTarget(null)
    },
  })

  const disableMut = useMutation({
    mutationFn: (id: string) => endpoints.disableUser(id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['users'] })
      toast({ title: 'User disabled' })
      setDisableTarget(null)
    },
    onError: (e) => {
      toast({ title: apiErrorTitle(e), tone: 'danger' })
      setDisableTarget(null)
    },
  })

  const forbidden = members.error instanceof ApiError && members.error.status === 403
  const rows = members.data ?? []

  const roleRank = (r: MemberRole) => ROLES.indexOf(r) // viewer(0) < developer(1) < admin(2) < owner(3)
  const memberControls = useTableControls(rows, {
    searchFields: (m) => [displayName(m.user_id, usersById)],
    comparators: {
      email: (a, b) => displayName(a.user_id, usersById).localeCompare(displayName(b.user_id, usersById)),
      role: (a, b) => roleRank(a.role) - roleRank(b.role),
    },
  })
  const userControls = useTableControls(usersList, {
    searchFields: (u) => [u.email],
    comparators: {
      email: (a, b) => a.email.localeCompare(b.email),
      status: (a, b) => Number(!!a.disabled) - Number(!!b.disabled), // active before disabled
    },
  })

  function handleScopeKind(k: ScopeKind) {
    setScopeKind(k)
    setPid('')
    setEid('')
  }

  return (
    <div>
      <div className="mb-3 flex items-start justify-between gap-3">
        <div>
          <h3 className="text-[15px] font-semibold text-ink">Members</h3>
          <p className="text-[12.5px] text-ink-faint">Role bindings scoped to the instance, a project, or an environment</p>
        </div>
        <div className="flex items-center gap-2">
          <div className="flex overflow-hidden rounded border border-line">
            <button
              type="button"
              aria-pressed={view === 'list'}
              onClick={() => setView('list')}
              className={`px-3 py-1.5 text-[12.5px] font-semibold transition-nocturne ${view === 'list' ? 'bg-surface-3 text-ink' : 'bg-surface-2 text-ink-mute hover:text-ink'}`}
            >
              List
            </button>
            <button
              type="button"
              aria-pressed={view === 'matrix'}
              onClick={() => setView('matrix')}
              className={`px-3 py-1.5 text-[12.5px] font-semibold transition-nocturne ${view === 'matrix' ? 'bg-surface-3 text-ink' : 'bg-surface-2 text-ink-mute hover:text-ink'}`}
            >
              Matrix
            </button>
          </div>
          {view === 'list' && scope && !forbidden && (
            <Button type="button" size="sm" onClick={() => setAddOpen(true)}>
              Add member
            </Button>
          )}
        </div>
      </div>

      {view === 'matrix' ? (
        <RbacMatrix
          onPickScope={(picked) => {
            setScopeKind(picked.kind)
            setPid(picked.kind === 'instance' ? '' : picked.pid)
            setEid(picked.kind === 'environment' ? picked.eid : '')
            setView('list')
          }}
        />
      ) : (
        <>
      <div className="mb-4 flex flex-wrap items-end gap-2">
        <label className="text-[12px] font-semibold text-ink-mute">
          Scope
          <select
            aria-label="scope"
            value={scopeKind}
            onChange={(e) => handleScopeKind(e.target.value as ScopeKind)}
            className="mt-1 block rounded border border-line bg-surface-3 px-3 py-1.5 text-[13px] font-normal text-ink focus:border-brand-line focus:shadow-glow-soft transition-nocturne"
          >
            <option value="instance">Instance</option>
            <option value="project">Project</option>
            <option value="environment">Environment</option>
          </select>
        </label>
        {scopeKind !== 'instance' && (
          <label className="text-[12px] font-semibold text-ink-mute">
            Project
            <select
              aria-label="project"
              value={pid}
              onChange={(e) => { setPid(e.target.value); setEid('') }}
              className="mt-1 block rounded border border-line bg-surface-3 px-3 py-1.5 text-[13px] font-normal text-ink focus:border-brand-line focus:shadow-glow-soft transition-nocturne"
            >
              <option value="">— select —</option>
              {(projects.data ?? []).map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
            </select>
          </label>
        )}
        {scopeKind === 'environment' && (
          <label className="text-[12px] font-semibold text-ink-mute">
            Environment
            <select
              aria-label="environment"
              value={eid}
              onChange={(e) => setEid(e.target.value)}
              disabled={!pid}
              className="mt-1 block rounded border border-line bg-surface-3 px-3 py-1.5 text-[13px] font-normal text-ink focus:border-brand-line focus:shadow-glow-soft transition-nocturne disabled:opacity-50"
            >
              <option value="">— select —</option>
              {(envs.data ?? []).map((e) => <option key={e.id} value={e.id}>{e.name}</option>)}
            </select>
          </label>
        )}
      </div>

      {forbidden ? (
        <EmptyState title="Member access required" hint="Ask an instance admin or owner for access." />
      ) : !scope ? (
        <p className="text-[12.5px] text-ink-mute">Pick a project{scopeKind === 'environment' ? ' and environment' : ''} to view members.</p>
      ) : members.isError ? (
        <p role="alert" className="text-[12.5px] text-danger">Couldn't load members.</p>
      ) : members.isLoading ? (
        <div className="flex flex-col gap-1.5" aria-hidden="true">
          {[0, 1, 2].map((i) => <div key={i} className="h-8 animate-pulse rounded bg-line-soft" />)}
        </div>
      ) : rows.length === 0 ? (
        <EmptyState title="No members yet" hint="Add a user and grant them a role in this scope." />
      ) : (
        <>
          <div className="mb-2">
            <TableSearch
              value={memberControls.query}
              onChange={memberControls.setQuery}
              matched={memberControls.matched}
              total={memberControls.total}
              label="search members"
              placeholder="Search members…"
            />
          </div>
          <table className="w-full rounded-card border border-line bg-surface-2 text-sm shadow-elev-1">
            <thead>
              <tr className="sticky top-0 z-10 bg-surface-1 text-left text-[10.5px] uppercase tracking-[.1em] text-ink-faint">
                <SortHeader label="Email" sortKey="email" controls={memberControls} />
                <SortHeader label="Role" sortKey="role" controls={memberControls} />
                <th className="py-1.5" />
              </tr>
            </thead>
            <tbody>
              {memberControls.matched === 0 ? (
                <tr>
                  <td colSpan={3} className="py-6 text-center text-[12.5px] text-ink-mute">
                    No members match “{memberControls.query}”.
                  </td>
                </tr>
              ) : (
                memberControls.view.map((m) => {
                  const label = displayName(m.user_id, usersById)
                  return (
                    <tr key={m.user_id} className="border-t border-line-soft hover:bg-row-hover transition-nocturne">
                      <td className="py-1.5">{label}</td>
                      <td className="py-1.5">
                        <select
                          aria-label={`role for ${label}`}
                          value={m.role}
                          onChange={(e) => setPendingRole({ uid: m.user_id, role: e.target.value as MemberRole, label })}
                          className="rounded border border-line bg-surface-3 px-2 py-1 text-[12.5px] text-ink focus:border-brand-line focus:shadow-glow-soft transition-nocturne"
                        >
                          {ROLES.map((r) => <option key={r} value={r}>{r}</option>)}
                        </select>
                      </td>
                      <td className="py-1.5 text-right">
                        <Button type="button" variant="danger" size="sm" onClick={() => setRemoveTarget({ uid: m.user_id, label })}>
                          Remove
                        </Button>
                      </td>
                    </tr>
                  )
                })
              )}
            </tbody>
          </table>
        </>
      )}
        </>
      )}

      {view === 'list' && scopeKind === 'instance' && usersAvailable && (
        <div className="mt-8">
          <div className="mb-3 flex items-start justify-between gap-3">
            <div>
              <h3 className="text-[15px] font-semibold text-ink">Users</h3>
              <p className="text-[12.5px] text-ink-faint">Local accounts on this instance</p>
            </div>
            <Button type="button" size="sm" onClick={() => setCreateOpen(true)}>
              Create user
            </Button>
          </div>
          <div className="mb-2">
            <TableSearch
              value={userControls.query}
              onChange={userControls.setQuery}
              matched={userControls.matched}
              total={userControls.total}
              label="search users"
              placeholder="Search users…"
            />
          </div>
          <table className="w-full rounded-card border border-line bg-surface-2 text-sm shadow-elev-1">
            <thead>
              <tr className="sticky top-0 z-10 bg-surface-1 text-left text-[10.5px] uppercase tracking-[.1em] text-ink-faint">
                <SortHeader label="Email" sortKey="email" controls={userControls} />
                <SortHeader label="Status" sortKey="status" controls={userControls} />
                <th className="py-1.5" />
              </tr>
            </thead>
            <tbody>
              {userControls.matched === 0 ? (
                <tr>
                  <td colSpan={3} className="py-6 text-center text-[12.5px] text-ink-mute">
                    No users match “{userControls.query}”.
                  </td>
                </tr>
              ) : (
                userControls.view.map((u) => (
                  <tr key={u.id} className="border-t border-line-soft hover:bg-row-hover transition-nocturne">
                    <td className="py-1.5">{u.email}</td>
                    <td className="py-1.5">{u.disabled ? <Pill tone="danger">disabled</Pill> : <Pill tone="success">active</Pill>}</td>
                    <td className="py-1.5 text-right">
                      {!u.disabled && (
                        <Button type="button" variant="danger" size="sm" onClick={() => setDisableTarget(u)}>
                          Disable
                        </Button>
                      )}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      )}

      {addOpen && scope && (
        <AddMemberSheet
          scope={scope}
          members={rows}
          users={usersList}
          onClose={() => setAddOpen(false)}
        />
      )}

      {createOpen && (
        <CreateUserSheet
          onClose={() => setCreateOpen(false)}
          onCreated={(pw) => { setCreateOpen(false); setNewPassword(pw) }}
        />
      )}

      {newPassword && (
        <RevealOnce
          open
          onClose={() => setNewPassword(null)}
          title="Initial password"
          secret={newPassword}
          hint="Shown once — share it with the user so they can sign in and change it."
        />
      )}

      {pendingRole && (
        <ConfirmDialog
          open
          onOpenChange={(o) => { if (!o) setPendingRole(null) }}
          title={`Change role to ${pendingRole.role}?`}
          body={`${pendingRole.label} will have the ${pendingRole.role} role in this scope.`}
          confirmLabel="Change role"
          onConfirm={() => roleMut.mutate({ uid: pendingRole.uid, role: pendingRole.role })}
        />
      )}

      {removeTarget && (
        <ConfirmDialog
          open
          onOpenChange={(o) => { if (!o) setRemoveTarget(null) }}
          title={`Remove ${removeTarget.label}?`}
          body="Their role binding in this scope is revoked immediately."
          confirmLabel="Remove"
          tone="danger"
          onConfirm={() => removeMut.mutate(removeTarget.uid)}
        />
      )}

      {disableTarget && (
        <ConfirmDialog
          open
          onOpenChange={(o) => { if (!o) setDisableTarget(null) }}
          title={`Disable ${disableTarget.email}?`}
          body="They can no longer sign in until re-enabled."
          confirmLabel="Disable"
          tone="danger"
          onConfirm={() => disableMut.mutate(disableTarget.id)}
        />
      )}
    </div>
  )
}
