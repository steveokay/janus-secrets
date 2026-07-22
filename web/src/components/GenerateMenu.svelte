<script lang="ts">
  import {
    generatePassword,
    generateHex,
    generateBase64,
    PW_MIN,
    PW_MAX,
    BYTES_MIN,
    BYTES_MAX,
  } from '../lib/generate'

  // Fills the caller's row draft. The generated plaintext is never persisted
  // anywhere but the editor's dirty buffer; this component holds no value state.
  let { onGenerate }: { onGenerate: (value: string) => void } = $props()

  type Kind = 'password' | 'hex' | 'base64'

  // Last-used options persist for the session (in component state only — never
  // localStorage, and never a generated value).
  let open = $state(false)
  let kind = $state<Kind>('password')
  let pwLength = $state(24)
  let byteLength = $state(32)
  let symbols = $state(true)
  let excludeAmbiguous = $state(false)

  let rootEl = $state<HTMLElement | null>(null)

  const lengthLabel = $derived(kind === 'password' ? 'Length (chars)' : 'Bytes')
  const lengthMin = $derived(kind === 'password' ? PW_MIN : BYTES_MIN)
  const lengthMax = $derived(kind === 'password' ? PW_MAX : BYTES_MAX)
  const lengthValue = $derived(kind === 'password' ? pwLength : byteLength)

  function setLength(v: number) {
    if (kind === 'password') pwLength = v
    else byteLength = v
  }

  function run() {
    let out: string
    if (kind === 'password') out = generatePassword(pwLength, { symbols, excludeAmbiguous })
    else if (kind === 'hex') out = generateHex(byteLength)
    else out = generateBase64(byteLength)
    onGenerate(out)
    // stay open so the user can regenerate / tweak and re-roll in place
  }

  function toggle() {
    open = !open
  }

  function close() {
    open = false
  }

  function onWindowKeydown(e: KeyboardEvent) {
    if (open && e.key === 'Escape') {
      e.stopPropagation()
      close()
    }
  }

  function onWindowPointerdown(e: PointerEvent) {
    if (open && rootEl && !rootEl.contains(e.target as Node)) close()
  }
</script>

<svelte:window onkeydown={onWindowKeydown} onpointerdown={onWindowPointerdown} />

<span class="gen-anchor" bind:this={rootEl}>
  <button
    type="button"
    class="btn btn-ghost btn-sm gen-trigger"
    aria-label="Generate a random value"
    aria-haspopup="dialog"
    aria-expanded={open}
    title="Generate a random value"
    onclick={toggle}
  >
    <svg width="13" height="13" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <rect x="1.5" y="1.5" width="13" height="13" rx="2.5" stroke="currentColor" stroke-width="1.4" />
      <circle cx="5" cy="5" r="1.1" fill="currentColor" />
      <circle cx="11" cy="5" r="1.1" fill="currentColor" />
      <circle cx="8" cy="8" r="1.1" fill="currentColor" />
      <circle cx="5" cy="11" r="1.1" fill="currentColor" />
      <circle cx="11" cy="11" r="1.1" fill="currentColor" />
    </svg>
    Gen
  </button>

  {#if open}
    <div class="gen-pop plate" role="dialog" aria-label="Value generator">
      <div class="gen-row">
        <span class="gen-label">Type</span>
        <div class="seg" role="group" aria-label="Value type">
          <button type="button" class="seg-btn" class:on={kind === 'password'} onclick={() => (kind = 'password')}>Password</button>
          <button type="button" class="seg-btn" class:on={kind === 'hex'} onclick={() => (kind = 'hex')}>Hex</button>
          <button type="button" class="seg-btn" class:on={kind === 'base64'} onclick={() => (kind = 'base64')}>Base64</button>
        </div>
      </div>

      <div class="gen-row">
        <label class="gen-label" for="gen-length">{lengthLabel}</label>
        <input
          id="gen-length"
          class="input len-input"
          type="number"
          min={lengthMin}
          max={lengthMax}
          value={lengthValue}
          oninput={(e) => setLength(Number((e.currentTarget as HTMLInputElement).value))}
        />
      </div>

      {#if kind === 'password'}
        <div class="gen-row toggles">
          <label class="chk">
            <input type="checkbox" bind:checked={symbols} />
            Include symbols
          </label>
          <label class="chk">
            <input type="checkbox" bind:checked={excludeAmbiguous} />
            Exclude ambiguous
          </label>
        </div>
      {/if}

      <div class="gen-actions">
        <button type="button" class="btn btn-sm" onclick={close}>Close</button>
        <button type="button" class="btn btn-sm btn-primary" onclick={run}>Generate</button>
      </div>
    </div>
  {/if}
</span>

<style>
  .gen-anchor { position: relative; display: inline-flex; }

  .gen-trigger {
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
    color: var(--ink-soft);
  }
  .gen-trigger:hover { color: var(--archivist); }

  .gen-pop {
    position: absolute;
    top: calc(100% + 6px);
    right: 0;
    z-index: 40;
    width: 260px;
    padding: var(--s3) var(--s4);
    display: flex;
    flex-direction: column;
    gap: var(--s3);
    animation: rise-in var(--t-fast) var(--ease-out) both;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.18);
  }

  .gen-row { display: flex; flex-direction: column; gap: var(--s1); }
  .gen-label {
    font-family: var(--font-ui);
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--ink-soft);
    font-weight: 620;
  }

  .seg { display: flex; border: 1px solid var(--rule-strong); border-radius: var(--radius); overflow: hidden; }
  .seg-btn {
    flex: 1;
    padding: 0.32rem 0.4rem;
    font-family: var(--font-ui);
    font-size: var(--text-xs);
    background: var(--paper-high);
    color: var(--ink-soft);
    border: 0;
    border-right: 1px solid var(--rule);
    cursor: pointer;
  }
  .seg-btn:last-child { border-right: 0; }
  .seg-btn:hover { background: var(--paper-low); }
  .seg-btn.on { background: var(--ink); color: var(--paper-high); }

  .len-input { width: 100%; }

  .toggles { gap: var(--s2); }
  .chk {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    font-family: var(--font-ui);
    font-size: var(--text-sm);
    color: var(--ink);
    cursor: pointer;
  }
  .chk input { cursor: pointer; }

  .gen-actions { display: flex; justify-content: flex-end; gap: var(--s2); margin-top: var(--s1); }
</style>
