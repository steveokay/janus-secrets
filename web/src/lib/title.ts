import { useEffect } from 'react'

// Sets "<section> · Janus" while mounted; restores the bare product name on unmount.
export function useTitle(section?: string) {
  useEffect(() => {
    document.title = section ? `${section} · Janus` : 'Janus'
    return () => {
      document.title = 'Janus'
    }
  }, [section])
}
