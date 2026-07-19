<script lang="ts">
  import { api, errorMessage, type TokenMeta, type MintTokenResult } from '../lib/api'
  import { registry } from '../lib/registry.svelte'
  import { dialog } from '../lib/dialog.svelte'
  import { relTime, stampDate } from '../lib/util'

  let tokens = $state<TokenMeta[]>([])
  let userEmails = $state<Map<string, string>>(new Map())
  let loading = $state(true)
  let error = $state('')

  let minting = $state(false)
  let mintName = $state('')
  let mintScopeKind = $state<'config' | 'environment' | 'transit'>('config')
  let mintScopeId = $state('')
  let mintAccess = $state<'read' | 'readwrite' | 'transit'>('read')
  let mintTtl = $state('')
  let minted = $state<MintTokenResult | null>(null)
  let mintError = $state('')

  $effect(() => {
    void load()
  })

  async function load() {
    loading = true
    error = ''
    try {
      tokens = await api.listTokens()
      const users = await api.listUsers().catch(() => [])
      userEmails = new Map(users.map(u => [u.id, u.email]))
    } catch (err) {
      error = errorMessage(err, 'Could not list tokens.')
    } finally {
      loading = false
    }
  }

  const minter = (id: string) => userEmails.get(id) ?? (id.includes('-') ? `${id.slice(0, 8)}…` : id)

  const scopeOptions = $derived(
    mintScopeKind === 'config'
      ? registry.projects.flatMap(p => p.environments.flatMap(e => e.configs.map(c => ({ id: c.id, label: `${p.name} / ${e.slug} / ${c.name}` }))))
      : mintScopeKind === 'environment'
        ? registry.projects.flatMap(p => p.environments.map(e => ({ id: e.id, label: `${p.name} / ${e.slug}` })))
        : [{ id: 'transit', label: 'transit (instance)' }],
  )

  async function mint(e: SubmitEvent) {
    e.preventDefault()
    mintError = ''
    try {
      const access = mintScopeKind === 'transit' ? 'transit' : mintAccess
      minted = await api.mintToken({
        name: mintName.trim(),
        scope: { kind: mintScopeKind, id: mintScopeKind === 'transit' ? 'transit' : mintScopeId },
        access,
        ...(mintTtl ? { ttl_seconds: Number(mintTtl) * 3600 } : {}),
      })
      await load()
    } catch (err) {
      mintError = errorMessage(err, 'Mint failed.')
    }
  }

  async function revoke(t: TokenMeta) {
    const ok = await dialog.confirm({
      title: `Revoke ${t.name}?`,
      body: 'Machines using this token lose access immediately. This cannot be undone.',
      confirmLabel: 'Revoke',
      danger: true,
    })
    if (!ok) return
    try {
      await api.revokeToken(t.id)
      await load()
    } catch (err) {
      error = errorMessage(err, 'Revoke failed.')
    }
  }

  function scopeLabel(t: TokenMeta): string {
    if (t.scope_kind === 'transit') return 'transit'
    if (t.scope_kind === 'config') return registry.configLabel(t.scope_id)
    for (const p of registry.projects)
      for (const e of p.environments)
        if (e.id === t.scope_id) return `${p.name} / ${e.slug}`
    return t.scope_id
  }

  const active = $derived(tokens.filter(t => !t.revoked_at))
</script>

<div class="page-n">
  <header class="page-head rise">
    <div>
      <p class="folio">Office · machine credentials · HMAC stored, never the raw token</p>
      <h1>Service tokens</h1>
    </div>
    <button class="btn btn-primary" onclick={() => { minting = !minting; minted = null }}>+ Mint token</button>
  </header>
  <hr class="ledger-rule" />

  {#if minting}
    <div class="sheet mint rise">
      {#if minted}
        <div class="minted">
          <span class="stamp ok flat">Minted — shown exactly once</span>
          <p class="folio">Copy it now; Janus keeps only the HMAC.</p>
          <code class="token-once mono">{minted.token}</code>
          <button class="btn btn-sm" onclick={() => navigator.clipboard.writeText(minted!.token)}>Copy</button>
          <button class="btn btn-sm btn-ghost" onclick={() => { minting = false; minted = null }}>Done</button>
        </div>
      {:else}
        <form onsubmit={mint}>
          <div class="field">
            <label class="label" for="tk-name">Name</label>
            <input id="tk-name" class="input" bind:value={mintName} placeholder="atlas-prod-runtime" required />
          </div>
          <div class="field">
            <label class="label" for="tk-kind">Scope kind</label>
            <select id="tk-kind" class="select" bind:value={mintScopeKind}>
              <option value="config">config</option>
              <option value="environment">environment</option>
              <option value="transit">transit</option>
            </select>
          </div>
          {#if mintScopeKind !== 'transit'}
            <div class="field grow">
              <label class="label" for="tk-scope">Scope</label>
              <select id="tk-scope" class="select" bind:value={mintScopeId} required>
                <option value="" disabled>choose…</option>
                {#each scopeOptions as o}
                  <option value={o.id}>{o.label}</option>
                {/each}
              </select>
            </div>
            <div class="field">
              <label class="label" for="tk-access">Access</label>
              <select id="tk-access" class="select" bind:value={mintAccess}>
                <option value="read">read</option>
                <option value="readwrite">read / write</option>
              </select>
            </div>
          {/if}
          <div class="field">
            <label class="label" for="tk-ttl">TTL (hours, blank = none)</label>
            <input id="tk-ttl" class="input" type="number" min="1" bind:value={mintTtl} placeholder="∞" />
          </div>
          <button class="btn btn-stamp" type="submit" disabled={!mintName.trim() || (mintScopeKind !== 'transit' && !mintScopeId)}>Mint</button>
          {#if mintError}<p class="error">{mintError}</p>{/if}
        </form>
      {/if}
    </div>
  {/if}

  {#if error}<p class="error rise">{error}</p>{/if}

  <div class="sheet table-wrap rise" style="animation-delay: 60ms">
    <table class="ledger">
      <thead>
        <tr>
          <th>Token</th>
          <th>Scope</th>
          <th style="width: 110px">Access</th>
          <th style="width: 150px">Expiry</th>
          <th style="width: 90px"></th>
        </tr>
      </thead>
      <tbody>
        {#each active as t (t.id)}
          <tr>
            <td>
              <span class="t-name">{t.name}</span>
              <span class="folio">minted {stampDate(t.created_at)} by {minter(t.created_by)}</span>
            </td>
            <td class="mono scope">{scopeLabel(t)}</td>
            <td>
              {#if t.access === 'read'}<span class="pill pill-neutral">read</span>
              {:else if t.access === 'transit'}<span class="pill pill-info">transit</span>
              {:else}<span class="pill pill-info">read / write</span>{/if}
            </td>
            <td>
              {#if !t.expires_at}
                <span class="folio">non-expiring</span>
              {:else if new Date(t.expires_at).getTime() - Date.now() < 3600_000}
                <span class="expiring">expires {relTime(t.expires_at)}</span>
              {:else}
                <span class="folio">expires {relTime(t.expires_at)}</span>
              {/if}
            </td>
            <td class="row-actions">
              <button class="btn btn-ghost btn-sm revoke" onclick={() => revoke(t)}>Revoke</button>
            </td>
          </tr>
        {:else}
          <tr><td colspan="5" class="empty folio">{loading ? 'Reading…' : 'No service tokens minted yet.'}</td></tr>
        {/each}
      </tbody>
    </table>
  </div>

  <p class="foot-note folio">
    Tokens are shown once at creation — Janus stores only their HMAC-SHA256. Federated CI tokens
    (via GitHub Actions OIDC exchange) are short-lived and appear here until expiry.
  </p>
</div>

<style>
  .page-n { max-width: 1200px; margin: 0 auto; }
  .page-head { display: flex; justify-content: space-between; align-items: flex-end; gap: var(--s4); }
  .page-head h1 { margin-top: var(--s1); }

  .mint { padding: var(--s4) var(--s5); margin-top: var(--s4); border-left: 4px solid var(--vermilion); }
  .mint form { display: flex; align-items: flex-end; gap: var(--s4); flex-wrap: wrap; }
  .field { display: flex; flex-direction: column; gap: var(--s2); min-width: 140px; }
  .field.grow { flex: 1; min-width: 220px; }
  .error { color: var(--vermilion); font-size: var(--text-sm); width: 100%; }

  .minted { display: flex; align-items: center; gap: var(--s3); flex-wrap: wrap; }
  .token-once {
    background: var(--paper-low);
    border: 1px dashed var(--rule-strong);
    border-radius: var(--radius);
    padding: var(--s2) var(--s3);
    font-size: var(--text-xs);
    word-break: break-all;
    flex: 1;
    min-width: 260px;
  }

  .table-wrap { overflow-x: auto; margin-top: var(--s5); }
  .t-name { display: block; font-weight: 620; }
  .scope { font-size: var(--text-xs); color: var(--ink-soft); }
  .expiring { color: var(--ochre); font-size: var(--text-xs); font-weight: 650; text-transform: uppercase; letter-spacing: 0.06em; }
  .row-actions { text-align: right; }
  .revoke:hover { color: var(--vermilion); }
  .empty { text-align: center; padding: var(--s6) !important; }
  .foot-note { margin-top: var(--s3); max-width: 70ch; }
</style>
