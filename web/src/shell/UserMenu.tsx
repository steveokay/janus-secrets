import { useState } from 'react'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { useAuth } from '../auth/AuthProvider'
import { ChangePasswordForm } from '../auth/ChangePassword'
import { useTheme, type Theme } from '../theme/ThemeProvider'

const item =
  'flex w-full cursor-default select-none items-center rounded px-2.5 py-1.5 text-[13px] text-ink outline-none data-[highlighted]:bg-brand-soft data-[highlighted]:text-brand-text'
const radio =
  'relative flex w-full cursor-default select-none items-center rounded py-1.5 pl-7 pr-2.5 text-[13px] text-ink outline-none data-[highlighted]:bg-brand-soft data-[highlighted]:text-brand-text'

const THEMES: { value: Theme; label: string }[] = [
  { value: 'light', label: 'Light' },
  { value: 'dark', label: 'Dark' },
  { value: 'system', label: 'System' },
]

export function UserMenu() {
  const { user, logout } = useAuth()
  const { theme, setTheme } = useTheme()
  const [showPw, setShowPw] = useState(false)
  if (!user) return null
  const initials = (user.name.split('@')[0] || user.name).slice(0, 2).toUpperCase()

  return (
    <>
      <Menu.Root>
        <Menu.Trigger
          aria-label="user menu"
          className="flex h-7 w-7 items-center justify-center rounded-full border border-brand-line bg-brand-soft text-[11px] font-bold text-brand-text"
        >
          {initials}
        </Menu.Trigger>
        <Menu.Portal>
          <Menu.Content
            align="end"
            sideOffset={6}
            className="min-w-[210px] rounded-card border border-line bg-card p-1.5 shadow-pop"
          >
            <div className="px-2.5 pb-1.5 pt-1 text-[12px] text-ink-faint">{user.name}</div>
            <Menu.Separator className="my-1 h-px bg-line-soft" />
            <div className="px-2.5 pb-1 pt-1 text-[10.5px] font-bold uppercase tracking-[.12em] text-ink-faint">
              Theme
            </div>
            <Menu.RadioGroup value={theme} onValueChange={(v) => setTheme(v as Theme)}>
              {THEMES.map((t) => (
                <Menu.RadioItem key={t.value} value={t.value} className={radio}>
                  <Menu.ItemIndicator className="absolute left-2.5 text-brand-text">•</Menu.ItemIndicator>
                  {t.label}
                </Menu.RadioItem>
              ))}
            </Menu.RadioGroup>
            <Menu.Separator className="my-1 h-px bg-line-soft" />
            <Menu.Item className={item} onSelect={() => setShowPw(true)}>
              Change password
            </Menu.Item>
            <Menu.Item className={item} onSelect={() => void logout()}>
              Log out
            </Menu.Item>
          </Menu.Content>
        </Menu.Portal>
      </Menu.Root>
      {showPw && <ChangePasswordForm onDone={() => setShowPw(false)} onClose={() => setShowPw(false)} />}
    </>
  )
}
