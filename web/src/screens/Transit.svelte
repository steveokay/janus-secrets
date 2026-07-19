<script lang="ts">
  import { api, errorMessage, type ApiTransitKey } from '../lib/api'

  let keys = $state<ApiTransitKey[]>([])
  let selected = $state<string | null>(null)
  let loading = $state(true)
  let error = $state('')

  let creating = $state(false)
  let newName = $state('')
  let newType = $state<'aes256-gcm' | 'ed25519'>('aes256-gcm')

  let plaintext = $state('')
  let output = $state('')
  let opError = $state('')

  const sel = $derived(keys.find(k => k.name === selected) ?? null)

  $effect(() => {
    void load()
  })

  async function load() {
    loading = true
    error = ''
    try {
      keys = await api.listTransitKeys()
      if (!selected && keys.length) selected = keys[0].name
    } catch (err) {
      error = errorMessage(err, 'Could not list transit keys.')
    } finally {
      loading = false
    }
  }

  async function create(e: SubmitEvent) {
    e.preventDefault()
    error = ''
    try {
      await api.createTransitKey(newName.trim(), newType)
      creating = false
      selected = newName.trim()
      newName = ''
      await load()
    } catch (err) {
      error = errorMessage(err, 'Could not create the key.')
    }
  }

  async function rotate() {
    if (!sel) return
    try {
      await api.rotateTransitKey(sel.name)
      await load()
    } catch (err) {
      opError = errorMessage(err, 'Rotate failed.')
    }
  }

  async function run() {
    if (!sel || !plaintext) return
    opError = ''
    output = ''
    try {
      if (sel.type === 'ed25519') {
        const res = await api.transitSign(sel.name, plaintext)
        output = res.signature
      } else {
        const res = await api.transitEncrypt(sel.name, plaintext)
        output = res.ciphertext
      }
    } catch (err) {
      opError = errorMessage(err, 'Operation failed.')
    }
  }
</script>

<div class="page-n">
  <header class="page-head rise">
    <div>
      <p class="folio">Instruments · encryption as a service · key material never leaves the server</p>
      <h1>Transit</h1>
    </div>
    <button class="btn btn-primary" onclick={() => (creating = !creating)}>+ Create key</button>
  </header>
  <hr class="ledger-rule" />

  {#if creating}
    <form class="sheet create rise" onsubmit={create}>
      <div class="field grow">
        <label class="label" for="tk-name">Key name</label>
        <input id="tk-name" class="input mono" bind:value={newName} placeholder="pii-fields" required />
      </div>
      <div class="field">
        <label class="label" for="tk-type">Type</label>
        <select id="tk-type" class="select" bind:value={newType}>
          <option value="aes256-gcm">aes256-gcm — encrypt / decrypt</option>
          <option value="ed25519">ed25519 — sign / verify</option>
        </select>
      </div>
      <button class="btn btn-stamp" type="submit" disabled={!newName.trim()}>Create</button>
    </form>
  {/if}

  {#if error}<p class="error rise">{error}</p>{/if}

  <div class="layout">
    <div class="keys rise" style="animation-delay: 50ms">
      {#each keys as k (k.name)}
        <button class="key-card sheet" class:on={selected === k.name} onclick={() => { selected = k.name; output = ''; opError = '' }}>
          <div class="key-top">
            <span class="key-name mono">{k.name}</span>
            <span class="pill pill-neutral">{k.type}</span>
          </div>
          <div class="key-vers" aria-label={`versions 1 to ${k.latest_version}`}>
            {#each Array(k.latest_version) as _, i}
              <span
                class="ver-notch"
                class:live={i + 1 >= k.min_decryption_version}
                class:latest={i + 1 === k.latest_version}
                title={`v${i + 1}${i + 1 < k.min_decryption_version ? ' — below min_decryption_version' : ''}`}
              >v{i + 1}</span>
            {/each}
          </div>
          <div class="folio">
            min-decrypt v{k.min_decryption_version} · {k.deletion_allowed ? 'deletion allowed' : 'deletion locked'}
          </div>
        </button>
      {:else}
        <p class="folio">{loading ? 'Reading…' : 'No transit keys yet — create the first one.'}</p>
      {/each}
    </div>

    {#if sel}
      <div class="console sheet rise" style="animation-delay: 110ms">
        <div class="console-head">
          <h3 class="mono">{sel.name}</h3>
          <div class="console-actions">
            <button class="btn btn-sm" onclick={rotate}>Rotate → v{sel.latest_version + 1}</button>
          </div>
        </div>

        <div class="bench">
          <label class="label" for="pt">{sel.type === 'ed25519' ? 'Payload to sign' : 'Plaintext to encrypt'}</label>
          <textarea id="pt" class="input" rows="3" bind:value={plaintext}
            placeholder="Data passes through — Janus never persists it."></textarea>
          <button class="btn btn-primary bench-go" onclick={run} disabled={!plaintext}>
            {sel.type === 'ed25519' ? 'Sign' : 'Encrypt'}
          </button>
          {#if opError}<p class="error">{opError}</p>{/if}
          {#if output}
            <label class="label" for="ct">{sel.type === 'ed25519' ? 'Signature' : 'Ciphertext envelope'}</label>
            <code id="ct" class="envelope mono">{output}</code>
          {/if}
        </div>

        <p class="foot-note folio">
          Envelope format <code>janus:v&lt;N&gt;:&lt;base64&gt;</code>. Decryption refuses versions below
          min_decryption_version. Data-plane ops are not audited; management ops are.
        </p>
      </div>
    {/if}
  </div>
</div>

<style>
  .page-n { max-width: 1200px; margin: 0 auto; }
  .page-head { display: flex; justify-content: space-between; align-items: flex-end; gap: var(--s4); }
  .page-head h1 { margin-top: var(--s1); }
  .error { color: var(--vermilion); font-size: var(--text-sm); margin-top: var(--s3); }

  .create {
    display: flex;
    align-items: flex-end;
    gap: var(--s4);
    padding: var(--s4) var(--s5);
    margin-top: var(--s4);
    border-left: 4px solid var(--vermilion);
    flex-wrap: wrap;
  }
  .field { display: flex; flex-direction: column; gap: var(--s2); }
  .field.grow { flex: 1; min-width: 200px; }

  .layout {
    display: grid;
    grid-template-columns: 380px 1fr;
    gap: var(--s5);
    margin-top: var(--s5);
    align-items: start;
  }

  .keys { display: flex; flex-direction: column; gap: var(--s3); }
  .key-card {
    text-align: left;
    padding: var(--s3) var(--s4);
    cursor: pointer;
    font-family: var(--font-ui);
    color: var(--ink);
    border-left: 4px solid transparent;
    transition: box-shadow var(--t-fast), border-color var(--t-fast);
  }
  .key-card.on { border-color: var(--rule-strong); box-shadow: var(--shadow-plate); border-left-color: var(--vermilion); }
  .key-top { display: flex; justify-content: space-between; align-items: center; margin-bottom: var(--s2); gap: var(--s2); }
  .key-name { font-weight: 600; font-size: var(--text-sm); word-break: break-all; }

  .key-vers { display: flex; gap: 4px; margin-bottom: var(--s2); flex-wrap: wrap; }
  .ver-notch {
    font-family: var(--font-mono);
    font-size: 0.6rem;
    padding: 0.06rem 0.3rem;
    border: 1px solid var(--rule);
    border-radius: 2px;
    color: var(--ink-ghost);
    text-decoration: line-through;
  }
  .ver-notch.live { color: var(--verdigris); border-color: var(--verdigris); text-decoration: none; }
  .ver-notch.latest { background: var(--verdigris); color: var(--paper-high); font-weight: 600; }

  .console { padding: var(--s5); }
  .console-head { display: flex; justify-content: space-between; align-items: center; flex-wrap: wrap; gap: var(--s3); }
  .console-actions { display: flex; gap: var(--s2); }

  .bench { margin-top: var(--s4); display: flex; flex-direction: column; gap: var(--s2); align-items: flex-start; }
  .bench textarea { resize: vertical; }
  .bench-go { margin: var(--s2) 0; }
  .envelope {
    display: block;
    width: 100%;
    background: var(--paper-low);
    border: 1px dashed var(--rule-strong);
    border-radius: var(--radius);
    padding: var(--s3);
    font-size: var(--text-xs);
    word-break: break-all;
    color: var(--verdigris-deep);
  }

  .foot-note { margin-top: var(--s4); }

  @media (max-width: 900px) { .layout { grid-template-columns: 1fr; } }
</style>
