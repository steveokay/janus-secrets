import { useQuery } from '@tanstack/react-query'
import { promotion } from './endpoints'

// usePipeline fetches a project's ordered promotion pipeline (env ids in order).
// 403/unconfigured → empty list (promotion disabled), never an error surface.
export function usePipeline(pid: string) {
  return useQuery({
    queryKey: ['pipeline', pid],
    queryFn: () => promotion.pipeline.get(pid),
    retry: false,
  })
}
