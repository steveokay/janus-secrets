import { TableControls } from './useTableControls'

export function SortHeader({
  label,
  sortKey,
  controls,
  className,
}: {
  label: string
  sortKey: string
  controls: Pick<TableControls<unknown>, 'sortKey' | 'sortDir' | 'toggleSort'>
  className?: string
}) {
  const active = controls.sortKey === sortKey
  const ariaSort = active ? (controls.sortDir === 'asc' ? 'ascending' : 'descending') : 'none'
  const caret = active ? (controls.sortDir === 'asc' ? '▲' : '▼') : '↕'
  return (
    <th aria-sort={ariaSort} className={`py-1.5 ${className ?? ''}`}>
      <button
        type="button"
        onClick={() => controls.toggleSort(sortKey)}
        className="flex items-center gap-1 text-[10.5px] uppercase tracking-[.1em] text-ink-faint hover:text-ink transition-nocturne"
      >
        {label}
        <span aria-hidden="true" className={active ? 'text-ink' : 'text-ink-faint'}>
          {caret}
        </span>
      </button>
    </th>
  )
}
