import { Moon, Sun } from 'lucide-react'
import { useTheme } from '../theme/ThemeProvider'
import { Tooltip } from '../ui/Tooltip'

// Standalone quick toggle: flips between light and dark based on the RESOLVED
// theme (the user-menu radio still offers the explicit 3-way incl. System).
export function ThemeToggle() {
  const { resolved, setTheme } = useTheme()
  const next = resolved === 'dark' ? 'light' : 'dark'
  return (
    <Tooltip content={`Switch to ${next} theme`}>
      <button
        type="button"
        aria-label={`Switch to ${next} theme`}
        onClick={() => setTheme(next)}
        className="flex h-7 w-7 items-center justify-center rounded text-ink-mute hover:bg-line-soft hover:text-ink"
      >
        {resolved === 'dark' ? <Sun size={16} strokeWidth={1.7} /> : <Moon size={16} strokeWidth={1.7} />}
      </button>
    </Tooltip>
  )
}
