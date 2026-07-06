import { createContext, useCallback, useContext, useState, ReactNode } from 'react'
import * as RT from '@radix-ui/react-toast'

type Push = (t: { title: string; tone?: 'success' | 'danger' }) => void
type Msg = { id: number; title: string; tone: 'success' | 'danger' }

const Ctx = createContext<Push>(() => {})
export const useToast = () => useContext(Ctx)

// App-level toast surface. NEVER pass secret values in titles.
export function ToastProvider({ children }: { children: ReactNode }) {
  const [msgs, setMsgs] = useState<Msg[]>([])
  const push = useCallback<Push>((t) => {
    setMsgs((s) => [...s, { id: Date.now() + Math.random(), title: t.title, tone: t.tone ?? 'success' }])
  }, [])
  return (
    <Ctx.Provider value={push}>
      <RT.Provider swipeDirection="right" duration={4000}>
        {children}
        {msgs.map((m) => (
          <RT.Root
            key={m.id}
            onOpenChange={(open) => { if (!open) setMsgs((s) => s.filter((x) => x.id !== m.id)) }}
            className="flex items-center gap-2.5 rounded-card bg-ink px-4 py-2.5 text-[12.5px] text-card shadow-pop"
          >
            <span aria-hidden className={m.tone === 'success' ? 'text-success' : 'text-danger'}>
              {m.tone === 'success' ? '✓' : '✕'}
            </span>
            <RT.Title>{m.title}</RT.Title>
          </RT.Root>
        ))}
        <RT.Viewport className="fixed bottom-4 right-4 z-50 flex w-[360px] max-w-[calc(100vw-2rem)] flex-col gap-2" />
      </RT.Provider>
    </Ctx.Provider>
  )
}
