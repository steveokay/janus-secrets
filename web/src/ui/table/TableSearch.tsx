export function TableSearch({
  value,
  onChange,
  matched,
  total,
  label,
  placeholder,
}: {
  value: string
  onChange: (v: string) => void
  matched: number
  total: number
  label: string
  placeholder?: string
}) {
  return (
    <div className="flex items-center gap-2">
      <input
        type="search"
        aria-label={label}
        value={value}
        placeholder={placeholder ?? 'Search…'}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Escape') onChange('')
        }}
        className="w-56 rounded border border-line bg-surface-3 px-3 py-1.5 text-[13px] text-ink focus:border-brand-line focus:shadow-glow-soft transition-nocturne"
      />
      {value.trim() !== '' && (
        <span className="text-[11.5px] text-ink-faint">
          {matched} of {total}
        </span>
      )}
    </div>
  )
}
