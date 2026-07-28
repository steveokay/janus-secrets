/* Keyboard shortcuts: the help-modal state, shared by ShortcutsHelp (which owns
   the key handling) and the command palette (which offers "Keyboard shortcuts"
   as an action). */

/* The `g`-chord table now lives with every other navigation destination in
   lib/nav.ts, gated by the caller's permissions — a chord that jumps to a
   screen the rail hides would route around the gating. Re-exported here so
   ShortcutsHelp keeps one import for both the chords and the modal state. */
export { chordsFor } from './nav'


let helpOpen = $state(false)

export const shortcuts = {
  get helpOpen() { return helpOpen },
  openHelp() { helpOpen = true },
  closeHelp() { helpOpen = false },
  toggleHelp() { helpOpen = !helpOpen },
}
