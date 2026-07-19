/* Instance-wide aggregation over the per-scope ops list endpoints.
   The API lists rotation/sync per project, dynamic roles per config, and
   leases per role — these fan out and flatten, tolerating per-scope 403s. */

import { api, type RotationPolicy, type SyncTargetApi, type DynamicRole, type ApiLease } from './api'
import type { ViewProject } from './registry.svelte'

export async function listAllRotations(projects: ViewProject[]): Promise<RotationPolicy[]> {
  const per = await Promise.all(projects.map(p => api.listRotationPolicies(p.id).catch(() => [])))
  return per.flat()
}

export async function listAllSyncs(projects: ViewProject[]): Promise<SyncTargetApi[]> {
  const per = await Promise.all(projects.map(p => api.listSyncTargets(p.id).catch(() => [])))
  return per.flat()
}

export async function listAllDynamicRoles(projects: ViewProject[]): Promise<DynamicRole[]> {
  const cids = projects.flatMap(p => p.environments.flatMap(e => e.configs.map(c => c.id)))
  const per = await Promise.all(cids.map(cid => api.listDynamicRoles(cid).catch(() => [])))
  return per.flat()
}

export async function listAllLeases(roles: DynamicRole[]): Promise<ApiLease[]> {
  const per = await Promise.all(roles.map(r => api.listLeases(r.id).catch(() => [])))
  return per.flat()
}
