import { useQuery } from '@tanstack/react-query'
import { endpoints } from '../lib/endpoints'

// retry: false — a viewer without AuditRead gets a 403; fail fast so the strip
// can hide itself immediately instead of retrying.
export const useReads24h = () =>
  useQuery({ queryKey: ['metrics', 'reads-24h'], queryFn: endpoints.metricsReads24h, retry: false })

export const useProjectReads24h = (pid: string) =>
  useQuery({
    queryKey: ['metrics', 'reads-24h', pid],
    queryFn: () => endpoints.projectMetricsReads24h(pid),
    retry: false,
  })
