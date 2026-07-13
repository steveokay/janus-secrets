import { Card } from '../ui/Card'
import { cn } from '../ui/cn'
import { useTheme, Theme } from '../theme/ThemeProvider'

const OPTIONS: { key: Theme; label: string; hint: string }[] = [
  { key: 'light', label: 'Light', hint: 'Always light' },
  { key: 'dark', label: 'Dark', hint: 'Always dark' },
  { key: 'system', label: 'System', hint: 'Match OS preference' },
]

// Client-only: reuses the existing ThemeProvider (useTheme/setTheme). No backend
// call — the choice persists to localStorage inside the provider's setTheme.
export function AppearanceSection() {
  const { theme, setTheme } = useTheme()
  return (
    <Card className="p-4">
      <h3 className="text-[15px] font-semibold text-ink">Theme</h3>
      <p className="mb-3 text-[12.5px] text-ink-mute">
        Choose how Janus looks. Applies instantly and is remembered on this device.
      </p>
      <div role="group" aria-label="Theme" className="flex gap-1.5">
        {OPTIONS.map((o) => {
          const on = theme === o.key
          return (
            <button
              key={o.key}
              type="button"
              aria-pressed={on}
              aria-label={o.label}
              onClick={() => setTheme(o.key)}
              title={o.hint}
              className={cn(
                'flex-1 rounded-card border px-3 py-2.5 text-[12.5px] font-medium transition-nocturne',
                on
                  ? 'border-brand-line bg-brand-soft text-brand-text shadow-glow-soft'
                  : 'border-line bg-surface-3 text-ink-mute hover:border-brand-line hover:text-ink',
              )}
            >
              <span className="block font-semibold">{o.label}</span>
              <span className="mt-0.5 block text-[11px] text-ink-faint">{o.hint}</span>
            </button>
          )
        })}
      </div>
    </Card>
  )
}
