<script lang="ts">
  import { router } from '../lib/router.svelte'
  import { dialog } from '../lib/dialog.svelte'
  import { shortcuts, CHORDS } from '../lib/shortcuts.svelte'

  // `g` pressed, waiting for the second key of the chord
  let pending = $state(false)
  let pendingTimer: ReturnType<typeof setTimeout> | undefined

  function isEditable(t: EventTarget | null): boolean {
    if (!(t instanceof HTMLElement)) return false
    return (
      t instanceof HTMLInputElement ||
      t instanceof HTMLTextAreaElement ||
      t instanceof HTMLSelectElement ||
      t.isContentEditable
    )
  }

  function clearPending() {
    pending = false
    if (pendingTimer) clearTimeout(pendingTimer)
  }

  function onKeydown(e: KeyboardEvent) {
    // Never fire while typing, while a dialog is up, or with modifiers held
    // (the palette's input is covered by the editable-target guard).
    if (e.ctrlKey || e.metaKey || e.altKey) { clearPending(); return }
    if (isEditable(e.target) || dialog.current) { clearPending(); return }

    if (shortcuts.helpOpen) {
      if (e.key === 'Escape' || e.key === '?') {
        e.preventDefault()
        shortcuts.closeHelp()
      }
      return
    }

    if (pending) {
      clearPending()
      const chord = CHORDS.find(c => c.keys === e.key.toLowerCase())
      if (chord) {
        e.preventDefault()
        router.go(chord.to)
      }
      return
    }

    if (e.key === '?') {
      e.preventDefault()
      shortcuts.openHelp()
    } else if (e.key.toLowerCase() === 'g' && !e.shiftKey) {
      e.preventDefault()
      pending = true
      pendingTimer = setTimeout(() => (pending = false), 1500)
    }
  }
</script>

<svelte:window onkeydown={onKeydown} />

{#if pending}
  <div class="chord-hint plate mono" role="status" aria-label="Waiting for chord key">
    g&thinsp;… <span class="folio">then a key to navigate — ? for the list</span>
  </div>
{/if}

{#if shortcuts.helpOpen}
  <div class="veil" role="presentation" onclick={() => shortcuts.closeHelp()}>
    <div class="help plate" role="dialog" aria-modal="true" aria-label="Keyboard shortcuts" onclick={(e) => e.stopPropagation()}>
      <header class="help-head">
        <h3>Keyboard shortcuts</h3>
        <button class="btn btn-ghost btn-sm" onclick={() => shortcuts.closeHelp()}>Close</button>
      </header>

      <div class="cols">
        <section>
          <h4 class="folio">Anywhere</h4>
          <dl>
            <div><dt><kbd class="key">ctrl</kbd><kbd class="key">K</kbd></dt><dd>Command palette</dd></div>
            <div><dt><kbd class="key">?</kbd></dt><dd>This help</dd></div>
            <div><dt><kbd class="key">esc</kbd></dt><dd>Close dialog / palette</dd></div>
          </dl>
          <h4 class="folio">Secret editor</h4>
          <dl>
            <div><dt><kbd class="key">ctrl</kbd><kbd class="key">↵</kbd></dt><dd>Apply the value being edited</dd></div>
            <div><dt><kbd class="key">↵</kbd></dt><dd>Newline inside a value</dd></div>
          </dl>
        </section>

        <section>
          <h4 class="folio">Go to — press <kbd class="key">g</kbd>, then…</h4>
          <dl class="chords">
            {#each CHORDS as c (c.keys)}
              <div><dt><kbd class="key">g</kbd><kbd class="key">{c.keys}</kbd></dt><dd>{c.label}</dd></div>
            {/each}
          </dl>
        </section>
      </div>
    </div>
  </div>
{/if}

<style>
  .veil {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.25);
    z-index: 100;
    display: flex;
    justify-content: center;
    padding-top: 10vh;
  }
  .help {
    width: min(620px, 92vw);
    height: fit-content;
    max-height: 78vh;
    overflow-y: auto;
    padding: var(--s5);
    animation: rise-in var(--t-fast) var(--ease-out) both;
  }
  .help-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    border-bottom: 2px solid var(--rule-strong);
    padding-bottom: var(--s3);
    margin-bottom: var(--s4);
  }
  .help-head h3 { font-family: var(--font-display); font-size: var(--text-lg); }

  .cols {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--s6);
  }
  @media (max-width: 560px) { .cols { grid-template-columns: 1fr; } }

  h4 {
    text-transform: uppercase;
    letter-spacing: 0.18em;
    font-size: 0.62rem;
    font-weight: 700;
    margin: 0 0 var(--s2);
  }
  section + section h4:first-child { margin-top: 0; }
  section h4:not(:first-child) { margin-top: var(--s4); }

  dl { margin: 0; }
  dl > div {
    display: flex;
    align-items: baseline;
    gap: var(--s3);
    padding: 0.28rem 0;
    border-bottom: 1px solid var(--rule-faint);
  }
  dt { min-width: 4.6rem; display: flex; gap: 0.25rem; }
  dd { margin: 0; font-size: var(--text-sm); color: var(--ink-soft); }

  .chord-hint {
    position: fixed;
    bottom: var(--s5);
    left: 50%;
    transform: translateX(-50%);
    z-index: 90;
    padding: var(--s2) var(--s4);
    font-size: var(--text-sm);
    animation: rise-in var(--t-fast) var(--ease-out) both;
  }
</style>
