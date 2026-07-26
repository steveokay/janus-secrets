// Accessibility helpers shared across overlay/dialog surfaces.
//
// `trapFocus` is a Svelte `use:` action that turns any element into a focus
// trap for the lifetime it is mounted: it moves focus inside on mount, keeps
// Tab / Shift+Tab cycling within the node, and restores focus to whatever
// element was focused when it opened once the node is removed. Pair it with
// `role="dialog"` + `aria-modal="true"` + an accessible name on the same node.
//
// Esc handling and click-outside dismissal remain the component's job (they
// already exist per-surface); this action only owns focus containment +
// restoration so the behaviour is not duplicated in every modal.

const FOCUSABLE = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',')

function focusable(node: HTMLElement): HTMLElement[] {
  return Array.from(node.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
    (el) => el.offsetParent !== null || el === document.activeElement,
  )
}

// `enabled` lets a caller attach the trap to an element that is ALWAYS mounted
// and only sometimes modal — the shell's nav cover, which is a static sidebar on
// desktop and an off-canvas drawer below the breakpoint. Omit it (the modal
// case, where the node is conditionally rendered) and the trap is always on.
export function trapFocus(node: HTMLElement, enabled: boolean | undefined = true) {
  let on = enabled !== false
  const restore = document.activeElement as HTMLElement | null

  function focusFirst() {
    if (!on) return
    const els = focusable(node)
    // Prefer an element the component already focused (e.g. an input via
    // bind:this + .focus()); otherwise move to the first focusable, or the
    // dialog itself so keystrokes still land inside the trap.
    if (node.contains(document.activeElement) && document.activeElement !== node) return
    ;(els[0] ?? node).focus()
  }

  function onKeydown(e: KeyboardEvent) {
    if (!on || e.key !== 'Tab') return
    const els = focusable(node)
    if (els.length === 0) {
      // Nothing tabbable — keep focus on the container.
      e.preventDefault()
      node.focus()
      return
    }
    const first = els[0]
    const last = els[els.length - 1]
    const active = document.activeElement as HTMLElement | null

    if (e.shiftKey) {
      if (active === first || active === node || !node.contains(active)) {
        e.preventDefault()
        last.focus()
      }
    } else {
      if (active === last || active === node || !node.contains(active)) {
        e.preventDefault()
        first.focus()
      }
    }
  }

  // Defer the initial focus so components that focus their own input on open
  // (via setTimeout) win the race; if none does, we land on the first control.
  let raf = requestAnimationFrame(focusFirst)
  node.addEventListener('keydown', onKeydown)

  // Focus to restore when the trap is switched OFF (as opposed to unmounted) —
  // captured at the moment it switches on, since the always-mounted case has no
  // meaningful "before" at construction time.
  let restoreOnDisable: HTMLElement | null = null

  return {
    update(next: boolean | undefined) {
      const want = next !== false
      if (want === on) return
      on = want
      if (on) {
        restoreOnDisable = document.activeElement as HTMLElement | null
        cancelAnimationFrame(raf)
        raf = requestAnimationFrame(focusFirst)
      } else {
        cancelAnimationFrame(raf)
        if (restoreOnDisable && node.contains(document.activeElement)) {
          restoreOnDisable.focus?.()
        }
        restoreOnDisable = null
      }
    },
    destroy() {
      cancelAnimationFrame(raf)
      node.removeEventListener('keydown', onKeydown)
      // Only restore if focus is still inside the trap (avoid yanking focus
      // away if the user has since clicked elsewhere).
      if (restore && node.contains(document.activeElement)) {
        restore.focus?.()
      }
    },
  }
}
