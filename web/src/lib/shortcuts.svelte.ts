/* Keyboard shortcuts: the `g`-chord navigation table and the help-modal
   state, shared by ShortcutsHelp (which owns the key handling) and the
   command palette (which offers "Keyboard shortcuts" as an action). */

export interface Chord {
  keys: string      // second key of the `g`-chord
  label: string
  to: string
}

export const CHORDS: Chord[] = [
  { keys: 'h', label: 'Overview', to: '/' },
  { keys: 'p', label: 'Projects', to: '/projects' },
  { keys: 't', label: 'Transit', to: '/transit' },
  { keys: 'o', label: 'Operations', to: '/operations' },
  { keys: 'i', label: 'Integrations', to: '/integrations' },
  { keys: 'a', label: 'Audit ledger', to: '/audit' },
  { keys: 'r', label: 'Approvals', to: '/approvals' },
  { keys: 'k', label: 'Service tokens', to: '/tokens' },
  { keys: 'm', label: 'Members', to: '/members' },
  { keys: 'n', label: 'Notifications', to: '/notifications' },
  { keys: 's', label: 'Settings', to: '/settings' },
  { keys: 'x', label: 'Trash', to: '/trash' },
]

let helpOpen = $state(false)

export const shortcuts = {
  get helpOpen() { return helpOpen },
  openHelp() { helpOpen = true },
  closeHelp() { helpOpen = false },
  toggleHelp() { helpOpen = !helpOpen },
}
