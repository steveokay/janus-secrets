import { useState } from 'react'

export function UserPicker({ candidates, value, onChange }: {
  candidates: { id: string; email: string }[]
  value: string
  onChange: (id: string) => void
}) {
  const [query, setQuery] = useState('')
  const selected = candidates.find((c) => c.id === value)
  const q = query.trim().toLowerCase()
  const matches = q === '' ? candidates : candidates.filter((c) => c.email.toLowerCase().includes(q))

  return (
    <div className="mt-1">
      <input
        type="search"
        aria-label="search users"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        placeholder={selected ? selected.email : 'Search users…'}
        className="w-full rounded border border-line bg-surface-3 px-3 py-2 text-[13px] font-normal text-ink focus:border-brand-line focus:shadow-glow-soft transition-nocturne"
      />
      {selected && (
        <p className="mt-1 text-[11.5px] text-ink-faint">
          Selected: <span className="text-ink">{selected.email}</span>
        </p>
      )}
      <div className="mt-1 max-h-40 overflow-y-auto rounded border border-line bg-surface-3">
        {matches.length === 0 ? (
          <p className="px-3 py-2 text-[12.5px] text-ink-mute">No users match “{query}”.</p>
        ) : (
          matches.map((c) => (
            <button
              key={c.id}
              type="button"
              onClick={() => onChange(c.id)}
              aria-pressed={c.id === value}
              className={
                'block w-full px-3 py-2 text-left text-[13px] transition-nocturne hover:bg-row-hover ' +
                (c.id === value ? 'bg-row-hover text-ink' : 'text-ink')
              }
            >
              {c.email}
            </button>
          ))
        )}
      </div>
    </div>
  )
}
