import { useState } from 'react'
import { useAuth } from '../auth/AuthProvider'
import { ChangePasswordForm } from '../auth/ChangePassword'

export function TopBar({ sealed }: { sealed: boolean }) {
  const { user, logout } = useAuth()
  const [showPw, setShowPw] = useState(false)
  return (
    <header className="flex items-center justify-between border-b px-4 py-2">
      <span className="font-semibold text-blue-700">Janus</span>
      <div className="flex items-center gap-4 text-sm">
        <span>{sealed ? '🔒 sealed' : '🔓 unsealed'}</span>
        {user && (
          <span className="flex items-center gap-2">
            {user.email}
            <button onClick={() => setShowPw(true)} className="rounded border px-2 py-0.5">Change password</button>
            <button onClick={() => void logout()} className="rounded border px-2 py-0.5">Log out</button>
          </span>
        )}
      </div>
      {showPw && <ChangePasswordForm onDone={() => setShowPw(false)} onClose={() => setShowPw(false)} />}
    </header>
  )
}
