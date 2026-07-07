import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { usePaletteItems, type PaletteItem } from './usePaletteItems'
import { CommandPalette } from './CommandPalette'

interface PaletteCtx { open: () => void; close: () => void; isOpen: boolean }
const Ctx = createContext<PaletteCtx | null>(null)

export function PaletteProvider({ children }: { children: React.ReactNode }) {
  const [isOpen, setOpen] = useState(false)
  const navigate = useNavigate()
  const items = usePaletteItems()

  const open = useCallback(() => setOpen(true), [])
  const close = useCallback(() => setOpen(false), [])

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && (e.key === 'k' || e.key === 'K')) {
        e.preventDefault()
        setOpen((o) => !o)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const onSelect = useCallback((item: PaletteItem) => {
    setOpen(false)
    navigate(item.to)
  }, [navigate])

  const value = useMemo(() => ({ open, close, isOpen }), [open, close, isOpen])
  return (
    <Ctx.Provider value={value}>
      {children}
      <CommandPalette open={isOpen} items={items} onClose={close} onSelect={onSelect} />
    </Ctx.Provider>
  )
}

export function usePalette(): PaletteCtx {
  const v = useContext(Ctx)
  if (!v) throw new Error('usePalette must be used within PaletteProvider')
  return v
}
