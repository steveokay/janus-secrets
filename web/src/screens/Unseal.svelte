<script lang="ts">
  import { session } from '../lib/session.svelte'
  import { errorMessage } from '../lib/api'
  import JanusMark from '../components/JanusMark.svelte'
  import Guilloche from '../components/Guilloche.svelte'

  let share = $state('')
  let error = $state('')
  let busy = $state(false)
  let done = $state(false)
  let justAccepted = $state(false)

  const threshold = $derived(session.threshold)

  async function submit(e: SubmitEvent) {
    e.preventDefault()
    error = ''
    busy = true
    try {
      done = await session.submitShare(share.trim())
      share = ''
      justAccepted = true
      setTimeout(() => (justAccepted = false), 700)
    } catch (err) {
      error = errorMessage(err, 'Share rejected — malformed key share.')
    } finally {
      busy = false
    }
  }

  async function kms() {
    error = ''
    busy = true
    try {
      done = await session.unsealKms()
    } catch (err) {
      error = errorMessage(err, 'KMS auto-unseal failed.')
    } finally {
      busy = false
    }
  }
</script>

<div class="gate">
  <div class="rosette" aria-hidden="true">
    <Guilloche size={720} rings={18} opacity={0.1} />
  </div>

  <div class="plate ceremony rise">
    <header>
      <JanusMark size={46} />
      <p class="label" style="margin-top: 0.9rem">Janus · Secrets Registry</p>
      <h1>The vault is sealed.</h1>
      <p class="sub">
        The master key is not in memory.
        {#if session.sealType === 'shamir'}
          Present <strong>{threshold} of {session.totalShares}</strong> key shares to
          reconstruct it. Every share submission is recorded in the audit ledger.
        {:else}
          Recover it with a single AWS KMS decrypt.
        {/if}
      </p>
    </header>

    {#if session.sealType === 'shamir'}
      <div class="keyholes" role="status" aria-label={`${session.sharesSubmitted} of ${threshold} shares accepted`}>
        {#each Array(threshold) as _, i}
          <div class="keyhole" class:filled={done || i < session.sharesSubmitted} class:pop={justAccepted && i === session.sharesSubmitted - 1}>
            <svg width="26" height="26" viewBox="0 0 26 26" fill="none" aria-hidden="true">
              <circle cx="13" cy="10" r="4.4" stroke="currentColor" stroke-width="1.8" />
              <path d="M11.4 13.4 L10.2 20 h5.6 L14.6 13.4" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round" />
            </svg>
            <span class="folio">Share {i + 1}</span>
          </div>
        {/each}
      </div>
    {/if}

    {#if done}
      <div class="unsealed-wrap">
        <span class="stamp ok stamped">Unsealed — master key reconstructed</span>
        <p class="folio" style="margin-top: 1rem">Opening the atrium…</p>
      </div>
    {:else if session.sealType === 'shamir'}
      <form onsubmit={submit}>
        <label class="label" for="share">Key share {Math.min(session.sharesSubmitted + 1, threshold)} of {threshold}</label>
        <input
          id="share"
          class="field-ruled"
          type="password"
          bind:value={share}
          placeholder="paste key share — never stored, never logged"
          autocomplete="off"
          spellcheck="false"
        />
        {#if error}<p class="error">{error}</p>{/if}
        <div class="actions">
          <button class="btn btn-primary" type="submit" disabled={busy || !share.trim()}>
            {busy ? 'Verifying…' : 'Present share'}
          </button>
        </div>
      </form>
    {:else}
      {#if error}<p class="error">{error}</p>{/if}
      <div class="actions" style="justify-content: center">
        <button class="btn btn-primary" onclick={kms} disabled={busy}>
          {busy ? 'Unsealing…' : 'Unseal via AWS KMS'}
        </button>
      </div>
    {/if}

    <footer class="folio ceremony-foot">
      SYS/UNSEAL · {session.sealType === 'shamir' ? `Shamir ${threshold}-of-${session.totalShares}` : 'AWS KMS auto-unseal'} · all routes return 503 until unsealed
    </footer>
  </div>
</div>

<style>
  .gate {
    min-height: 100vh;
    display: grid;
    place-items: center;
    position: relative;
    overflow: hidden;
    padding: var(--s5);
  }
  .rosette {
    position: absolute;
    inset: 0;
    display: grid;
    place-items: center;
    color: var(--ink);
    pointer-events: none;
  }

  .ceremony {
    position: relative;
    width: min(560px, 100%);
    padding: var(--s7) var(--s7) var(--s5);
    text-align: center;
  }
  .ceremony header h1 { margin: var(--s3) 0 var(--s3); }
  .sub { color: var(--ink-soft); font-size: var(--text-sm); max-width: 40ch; margin: 0 auto; }

  .keyholes {
    display: flex;
    justify-content: center;
    gap: var(--s6);
    margin: var(--s6) 0;
  }
  .keyhole {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--s2);
    color: var(--ink-ghost);
    transition: color var(--t-med);
  }
  .keyhole.filled { color: var(--verdigris); }
  .keyhole.pop svg { animation: pop var(--t-med) var(--ease-press); }
  @keyframes pop { 50% { transform: scale(1.35); } }

  form { text-align: left; max-width: 400px; margin: 0 auto; }
  form .label { display: block; margin-bottom: var(--s2); color: var(--ink-soft); }
  .error { color: var(--vermilion); font-size: var(--text-sm); margin-top: var(--s2); }
  .actions {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--s3);
    margin-top: var(--s5);
  }

  .unsealed-wrap { margin: var(--s6) 0; }
  .stamped { animation: stamp-down 500ms var(--ease-press) both; font-size: var(--text-sm); }

  .ceremony-foot {
    margin-top: var(--s6);
    padding-top: var(--s4);
    border-top: 1px dashed var(--rule);
  }
</style>
