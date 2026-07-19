<script lang="ts">
  import { session } from '../lib/session.svelte'
  import { errorMessage, type InitResult } from '../lib/api'
  import JanusMark from '../components/JanusMark.svelte'
  import Guilloche from '../components/Guilloche.svelte'

  let shares = $state(5)
  let threshold = $state(3)
  let adminEmail = $state('')
  let busy = $state(false)
  let error = $state('')
  let result = $state<InitResult | null>(null)
  let acknowledged = $state(false)

  async function submit(e: SubmitEvent) {
    e.preventDefault()
    error = ''
    if (threshold > shares) {
      error = 'Threshold cannot exceed the number of shares.'
      return
    }
    busy = true
    try {
      result = await session.init(shares, threshold, adminEmail.trim())
    } catch (err) {
      error = errorMessage(err, 'Initialization failed.')
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
    {#if !result}
      <header>
        <JanusMark size={46} />
        <p class="label" style="margin-top: 0.9rem">Janus · Secrets Registry</p>
        <h1>Found the registry.</h1>
        <p class="sub">
          This server has never been initialized. Generate the master key, split it into
          Shamir shares, and appoint the first registrar. This ceremony happens exactly once.
        </p>
      </header>

      <form onsubmit={submit}>
        <div class="grid2">
          <div class="field">
            <label class="label" for="shares">Key shares</label>
            <input id="shares" class="field-ruled" type="number" min="1" max="255" bind:value={shares} />
          </div>
          <div class="field">
            <label class="label" for="threshold">Unseal threshold</label>
            <input id="threshold" class="field-ruled" type="number" min="1" max="255" bind:value={threshold} />
          </div>
        </div>
        <div class="field">
          <label class="label" for="email">First registrar (admin email)</label>
          <input id="email" class="field-ruled" type="email" required bind:value={adminEmail}
            placeholder="you@company.dev" />
        </div>
        {#if error}<p class="error">{error}</p>{/if}
        <div class="actions">
          <button class="btn btn-stamp" type="submit" disabled={busy || !adminEmail.trim()}>
            {busy ? 'Performing ceremony…' : 'Initialize the vault'}
          </button>
        </div>
      </form>
    {:else}
      <header>
        <span class="stamp ok stamped">Initialized — shown exactly once</span>
        <h1 style="margin-top: 1rem">Guard these well.</h1>
        <p class="sub">
          The shares below reconstruct the master key ({threshold}-of-{result.shares?.length ?? shares}),
          and the one-time password signs in the first registrar. Janus stores none of them —
          once you leave this page they are gone.
        </p>
      </header>

      {#if result.shares?.length}
        <ol class="shares">
          {#each result.shares as sh, i}
            <li><span class="folio">Share {i + 1}</span><code class="mono">{sh}</code></li>
          {/each}
        </ol>
      {/if}

      {#if result.admin}
        <div class="admin sheet">
          <div><span class="folio">Registrar</span><code class="mono">{result.admin.email}</code></div>
          <div><span class="folio">One-time password</span><code class="mono">{result.admin.password}</code></div>
        </div>
      {/if}

      <label class="ack">
        <input type="checkbox" bind:checked={acknowledged} />
        I have copied the shares and the password to a safe place.
      </label>
      <div class="actions center">
        <button class="btn btn-primary" disabled={!acknowledged} onclick={() => session.proceedFromInit()}>
          Proceed to unseal
        </button>
      </div>
    {/if}

    <footer class="folio ceremony-foot">
      SYS/INIT · one-time ceremony · shares and passwords are never persisted or logged
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
    width: min(640px, 100%);
    padding: var(--s7) var(--s7) var(--s5);
    text-align: center;
  }
  .ceremony header h1 { margin: var(--s3) 0 var(--s3); }
  .sub { color: var(--ink-soft); font-size: var(--text-sm); max-width: 48ch; margin: 0 auto; }

  form { text-align: left; max-width: 440px; margin: var(--s5) auto 0; }
  .grid2 { display: grid; grid-template-columns: 1fr 1fr; gap: var(--s5); }
  .field { display: flex; flex-direction: column; gap: var(--s2); margin-bottom: var(--s4); }
  .error { color: var(--vermilion); font-size: var(--text-sm); margin-bottom: var(--s3); }
  .actions { margin-top: var(--s3); }
  .actions.center { text-align: center; }

  .shares {
    list-style: none;
    text-align: left;
    max-width: 520px;
    margin: var(--s5) auto;
    border-top: 1px solid var(--rule);
  }
  .shares li {
    display: grid;
    grid-template-columns: 90px 1fr;
    gap: var(--s3);
    align-items: center;
    padding: var(--s2) 0;
    border-bottom: 1px solid var(--rule-faint);
  }
  .shares code { font-size: var(--text-xs); word-break: break-all; }

  .admin {
    max-width: 520px;
    margin: 0 auto var(--s4);
    padding: var(--s3) var(--s4);
    text-align: left;
    display: flex;
    flex-direction: column;
    gap: var(--s2);
    border-left: 3px solid var(--vermilion);
  }
  .admin div { display: grid; grid-template-columns: 150px 1fr; gap: var(--s3); }
  .admin code { font-size: var(--text-sm); word-break: break-all; }

  .ack {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: var(--s2);
    font-size: var(--text-sm);
    color: var(--ink-soft);
    margin-bottom: var(--s4);
    cursor: pointer;
  }

  .stamped { animation: stamp-down 500ms var(--ease-press) both; font-size: var(--text-sm); }

  .ceremony-foot {
    margin-top: var(--s6);
    padding-top: var(--s4);
    border-top: 1px dashed var(--rule);
  }
</style>
