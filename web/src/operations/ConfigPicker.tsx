import { useProjectConfigMap, type ProjectFilter } from './useAggregated'
import { Select } from '../ui/Select'

// A labeled config <Select> fed by the 403-tolerant useProjectConfigMap.
// Options render config NAMES/PATHS only (project / env / config) — never any
// secret value — and are valued by configId. Shared by the ops create Sheets.
export function ConfigPicker({ filter, value, onChange, label = 'Config' }: {
  filter: ProjectFilter; value: string; onChange: (id: string) => void; label?: string
}) {
  const { map, isLoading } = useProjectConfigMap(filter)
  const opts = [...map.values()]
    .map((c) => ({ id: c.configId, label: `${c.projectName} / ${c.envName} / ${c.configName}` }))
    .sort((a, b) => a.label.localeCompare(b.label))
  return (
    <Select label={label} value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="">{isLoading ? 'Loading…' : opts.length ? 'Select a config…' : 'No configs available'}</option>
      {opts.map((o) => <option key={o.id} value={o.id}>{o.label}</option>)}
    </Select>
  )
}
