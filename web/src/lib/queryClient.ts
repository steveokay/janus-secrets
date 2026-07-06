import { QueryClient } from '@tanstack/react-query'
import { ApiError } from './api'

// onAuthEvent is set by the app root; the client calls it on global auth/seal
// signals so a single place owns "redirect to /login" and "redirect to /unseal".
let onAuthEvent: (kind: 'unauthorized' | 'sealed') => void = () => {}
export function setAuthEventHandler(fn: (kind: 'unauthorized' | 'sealed') => void) {
  onAuthEvent = fn
}

function route(err: unknown) {
  if (err instanceof ApiError) {
    if (err.status === 401) onAuthEvent('unauthorized')
    else if (err.status === 503) onAuthEvent('sealed')
  }
}

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: (count, err) => !(err instanceof ApiError) && count < 2,
      staleTime: 5_000,
    },
  },
})
queryClient.getQueryCache().subscribe((e) => {
  if (e.type === 'updated' && e.query.state.status === 'error') route(e.query.state.error)
})
queryClient.getMutationCache().subscribe((e) => {
  if (e.type === 'updated' && e.mutation.state.status === 'error') route(e.mutation.state.error)
})
