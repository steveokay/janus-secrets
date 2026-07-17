import { useRbacMatrix } from './useRbacMatrix'
import { roleTone } from './matrix'
import type { MemberScope } from '../lib/endpoints'
import { Pill } from '../ui/Pill'
import { EmptyState } from '../ui/EmptyState'

export function RbacMatrix({ onPickScope }: { onPickScope: (scope: MemberScope) => void }) {
  const { model, isLoading, forbidden } = useRbacMatrix()

  if (forbidden) {
    return <EmptyState title="Member access required" hint="Ask an instance admin or owner for access." />
  }
  if (isLoading) {
    return (
      <div className="flex flex-col gap-1.5" aria-hidden="true">
        {[0, 1, 2].map((i) => (
          <div key={i} className="h-8 animate-pulse rounded bg-line-soft" />
        ))}
      </div>
    )
  }
  if (model.rows.length === 0) {
    return <EmptyState title="No role bindings yet" hint="Grant a user a role in a scope to see it here." />
  }

  return (
    <div>
      <p className="mb-2 text-[12px] text-ink-faint">
        Cells show explicit role bindings. Instance roles apply everywhere; project and environment roles are scoped.
      </p>
      <div className="overflow-x-auto rounded-card border border-line bg-surface-2 shadow-elev-1">
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-[10.5px] uppercase tracking-[.1em] text-ink-faint">
              <th className="sticky left-0 z-20 bg-surface-1 px-3 py-2">User</th>
              <th className="sticky top-0 z-10 bg-surface-1 px-3 py-2">Instance</th>
              {model.columns.map((c) => (
                <th key={c.id} className="sticky top-0 z-10 whitespace-nowrap bg-surface-1 px-3 py-2">
                  {c.name}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {model.rows.map((r) => (
              <tr key={r.userId} className="border-t border-line-soft transition-nocturne hover:bg-row-hover">
                <td className="sticky left-0 z-10 whitespace-nowrap bg-surface-2 px-3 py-1.5 font-medium text-ink">
                  {r.email}
                </td>
                <td className="px-3 py-1.5">
                  {model.instanceRole.has(r.userId) ? (
                    <button
                      type="button"
                      onClick={() => onPickScope({ kind: 'instance' })}
                      aria-label={`instance role for ${r.email}`}
                    >
                      <Pill tone={roleTone[model.instanceRole.get(r.userId)!]}>
                        {model.instanceRole.get(r.userId)}
                      </Pill>
                    </button>
                  ) : (
                    <span className="text-ink-faint">—</span>
                  )}
                </td>
                {model.columns.map((c) => {
                  const cell = model.projectCells.get(r.userId)?.get(c.id) ?? { role: undefined, envCount: 0 }
                  const empty = !cell.role && cell.envCount === 0
                  return (
                    <td key={c.id} className="px-3 py-1.5">
                      {empty ? (
                        <span className="text-ink-faint">—</span>
                      ) : (
                        <button
                          type="button"
                          onClick={() => onPickScope({ kind: 'project', pid: c.id })}
                          aria-label={`${c.name} role for ${r.email}`}
                          className="inline-flex items-center gap-1.5"
                        >
                          {cell.role && <Pill tone={roleTone[cell.role]}>{cell.role}</Pill>}
                          {cell.envCount > 0 && <Pill tone="muted">+{cell.envCount} env</Pill>}
                        </button>
                      )}
                    </td>
                  )
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
