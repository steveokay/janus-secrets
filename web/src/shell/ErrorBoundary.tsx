import { Component, ReactNode } from 'react'
import { Button } from '../ui/Button'

// Tiny non-crypto hash → a short, stable reference id for the crashed render.
// Purely for support triage; carries no secret material.
function digest(msg: string): string {
  let h = 0
  for (let i = 0; i < msg.length; i++) h = (h * 31 + msg.charCodeAt(i)) | 0
  return (h >>> 0).toString(36).slice(0, 8)
}

export class ErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null }
  static getDerivedStateFromError(error: Error) {
    return { error }
  }
  componentDidCatch(error: Error) {
    console.error('UI crash:', error)
  }
  render() {
    const { error } = this.state
    if (!error) return this.props.children
    const id = digest(`${error.name}:${error.message}`)
    return (
      <div className="mx-auto mt-16 flex max-w-md flex-col items-center gap-3 rounded-card border border-line bg-surface-2 p-6 text-center shadow-elev-1">
        <p className="text-[15px] font-semibold text-ink">Something broke</p>
        <p className="text-[12.5px] text-ink-mute">
          An unexpected error occurred rendering this page. Reference{' '}
          <span className="font-mono text-ink-body">{id}</span>.
        </p>
        <div className="mt-1 flex gap-2">
          <Button variant="primary" onClick={() => window.location.reload()}>
            Reload
          </Button>
          <Button
            variant="secondary"
            onClick={() =>
              void navigator.clipboard?.writeText(
                `[${id}] ${error.name}: ${error.message}\n${error.stack ?? ''}`,
              )
            }
          >
            Copy details
          </Button>
        </div>
      </div>
    )
  }
}
