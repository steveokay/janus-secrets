<script lang="ts">
  import { dialog } from '../lib/dialog.svelte'
  import { trapFocus } from '../lib/a11y'

  let inputValue = $state('')
  let inputEl = $state<HTMLInputElement | null>(null)
  let confirmEl = $state<HTMLButtonElement | null>(null)

  const d = $derived(dialog.current)

  $effect(() => {
    if (d) {
      inputValue = d.initial ?? ''
      setTimeout(() => (d.kind === 'prompt' ? inputEl?.focus() : confirmEl?.focus()), 30)
    }
  })

  function confirm() {
    if (!d) return
    if (d.kind === 'prompt') dialog.settle(inputValue.trim())
    else dialog.settle(true)
  }

  function cancel() {
    if (!d) return
    dialog.settle(d.kind === 'prompt' ? null : false)
  }

  function onKeydown(e: KeyboardEvent) {
    if (!d) return
    if (e.key === 'Escape') { e.preventDefault(); cancel() }
    else if (e.key === 'Enter' && !(d.kind === 'prompt' && !inputValue.trim())) { e.preventDefault(); confirm() }
  }
</script>

<svelte:window onkeydown={onKeydown} />

{#if d}
  <div class="veil" role="presentation" onclick={(e) => { if (e.target === e.currentTarget) cancel() }}>
    <div class="modal plate" class:danger={d.danger} role="dialog" aria-modal="true" aria-label={d.title}
      tabindex="-1" use:trapFocus>
      <h3>{d.title}</h3>
      {#if d.body}<p class="body">{d.body}</p>{/if}

      {#if d.kind === 'prompt'}
        <label class="p-label">
          {#if d.label}<span class="label">{d.label}</span>{/if}
          <input bind:this={inputEl} class="field-ruled mono" bind:value={inputValue}
            placeholder={d.placeholder ?? ''} spellcheck="false" autocomplete="off" />
        </label>
      {/if}

      <div class="actions">
        {#if d.kind !== 'notice'}
          <button class="btn" onclick={cancel}>Cancel</button>
        {/if}
        <button
          bind:this={confirmEl}
          class="btn {d.danger ? 'btn-stamp' : 'btn-primary'}"
          disabled={d.kind === 'prompt' && !inputValue.trim()}
          onclick={confirm}
        >
          {d.confirmLabel ?? (d.kind === 'notice' ? 'Understood' : 'Confirm')}
        </button>
      </div>
    </div>
  </div>
{/if}

<style>
  .veil {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.3);
    z-index: 120;
    display: grid;
    place-items: center;
    padding: var(--s5);
  }
  .modal {
    width: min(440px, 92vw);
    padding: var(--s5) var(--s6);
    animation: rise-in var(--t-fast) var(--ease-out) both;
    border-top: 4px solid var(--archivist);
  }
  .modal.danger { border-top-color: var(--vermilion); }
  .modal h3 { margin-bottom: var(--s2); }
  .body { color: var(--ink-soft); font-size: var(--text-sm); margin-bottom: var(--s3); }

  .p-label { display: flex; flex-direction: column; gap: var(--s2); margin: var(--s3) 0 var(--s2); }

  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--s3);
    margin-top: var(--s4);
  }
</style>
