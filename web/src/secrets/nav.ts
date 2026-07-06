import { useQuery } from '@tanstack/react-query'
import { endpoints } from '../lib/endpoints'

export const useProjects = () => useQuery({ queryKey: ['projects'], queryFn: endpoints.listProjects })
export const useEnvironments = (pid?: string) =>
  useQuery({ queryKey: ['envs', pid], queryFn: () => endpoints.listEnvironments(pid!), enabled: !!pid })
export const useConfigs = (pid?: string, eid?: string) =>
  useQuery({ queryKey: ['configs', pid, eid], queryFn: () => endpoints.listConfigs(pid!, eid!), enabled: !!pid && !!eid })
