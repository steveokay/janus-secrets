import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query'
import { endpoints } from '../lib/endpoints'
import { promotion } from './endpoints'

// Requests pending approval for a project — the "needs your review" queue.
export function usePendingRequests(projectId: string) {
  return useQuery({
    queryKey: ['promote-requests', projectId, 'pending'],
    queryFn: () => promotion.requests.list({ project: projectId, status: 'pending' }),
  })
}

// The viewer's own requests (any status) for a project.
export function useMyRequests(projectId: string) {
  return useQuery({
    queryKey: ['promote-requests', projectId, 'mine'],
    queryFn: () => promotion.requests.list({ project: projectId, mine: true }),
  })
}

export function usePromotionRequest(id: string) {
  return useQuery({
    queryKey: ['promote-request', id],
    queryFn: () => promotion.requests.get(id),
    enabled: !!id,
  })
}

function invalidateAll(qc: ReturnType<typeof useQueryClient>) {
  void qc.invalidateQueries({ queryKey: ['promote-requests'] })
  void qc.invalidateQueries({ queryKey: ['promote-request'] })
}

export function useApproveRequest() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => promotion.requests.approve(id),
    onSuccess: () => invalidateAll(qc),
  })
}

export function useRejectRequest() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, note }: { id: string; note: string }) => promotion.requests.reject(id, note),
    onSuccess: () => invalidateAll(qc),
  })
}

export function useCancelRequest() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => promotion.requests.cancel(id),
    onSuccess: () => invalidateAll(qc),
  })
}

// Sum of pending promotion requests the viewer could approve, fanned out
// across every project (403-tolerant, mirrors the ops-console aggregate
// pattern) — sourced for the nav pending-count badge.
export function usePendingApprovalCount(): { count: number; isLoading: boolean } {
  const projectsQ = useQuery({ queryKey: ['projects'], queryFn: endpoints.listProjects })
  const projects = projectsQ.data ?? []

  const qs = useQueries({
    queries: projects.map((p) => ({
      queryKey: ['promote-requests', p.id, 'pending'],
      queryFn: () => promotion.requests.list({ project: p.id, status: 'pending' }),
      retry: false,
    })),
  })

  const count = qs.reduce((sum, q) => {
    if (q.error) return sum // 403 (or any per-project error) tolerated — excluded, not surfaced
    return sum + (q.data?.length ?? 0)
  }, 0)

  const isLoading = projectsQ.isLoading || qs.some((q) => q.isLoading)
  return { count, isLoading }
}
