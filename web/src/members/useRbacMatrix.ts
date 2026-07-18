import { useQueries, useQuery } from '@tanstack/react-query'
import { endpoints, memberScopePath, type Member, type MemberScope, type UserInfo } from '../lib/endpoints'
import { ApiError } from '../lib/api'
import { assembleMatrix, type MatrixModel, type ScopeMembers } from './matrix'

interface Result {
  model: MatrixModel
  isLoading: boolean
  forbidden: boolean
}

/**
 * Fans out listMembers across the instance scope + every project + every
 * environment (403-tolerant per scope), then assembles the read-only RBAC
 * matrix. Reuses the existing query keys (projects/envs/members/users) so
 * List-view edits elsewhere in the app auto-refresh this view too.
 */
export function useRbacMatrix(): Result {
  const projectsQ = useQuery({ queryKey: ['projects'], queryFn: endpoints.listProjects })
  const projects = (projectsQ.data ?? []).map((p) => ({ id: p.id, name: p.name }))

  const envListQs = useQueries({
    queries: projects.map((p) => ({
      queryKey: ['envs', p.id],
      queryFn: () => endpoints.listEnvironments(p.id),
    })),
  })
  const envScopes: MemberScope[] = []
  projects.forEach((p, i) => {
    for (const e of envListQs[i]?.data ?? []) envScopes.push({ kind: 'environment', pid: p.id, eid: e.id })
  })

  const scopes: MemberScope[] = [
    { kind: 'instance' },
    ...projects.map((p) => ({ kind: 'project', pid: p.id }) as MemberScope),
    ...envScopes,
  ]

  const memberQs = useQueries({
    queries: scopes.map((s) => ({
      queryKey: ['members', memberScopePath(s)],
      queryFn: () => endpoints.listMembers(s),
      retry: false,
    })),
  })

  const usersQ = useQuery({ queryKey: ['users'], queryFn: endpoints.listUsers, retry: false })

  const instanceErr = memberQs[0]?.error
  const forbidden = instanceErr instanceof ApiError && instanceErr.status === 403

  const scopeMembers: ScopeMembers[] = scopes
    .map((scope, i) => ({ scope, members: (memberQs[i]?.data ?? []) as Member[] }))
    .filter((sm) => sm.members.length > 0)

  const model = assembleMatrix(scopeMembers, projects, usersQ.data as UserInfo[] | undefined)

  const isLoading =
    projectsQ.isLoading ||
    envListQs.some((q) => q.isLoading) ||
    memberQs.some((q) => q.isLoading)

  return { model, isLoading, forbidden }
}
