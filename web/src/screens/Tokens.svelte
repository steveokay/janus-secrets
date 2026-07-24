<script lang="ts">
  import { api, errorMessage, type TokenMeta, type MintTokenResult } from '../lib/api'
  import { registry } from '../lib/registry.svelte'
  import { dialog } from '../lib/dialog.svelte'
  import { relTime, stampDate } from '../lib/util'

  let tokens = $state<TokenMeta[]>([])
  let userEmails = $state<Map<string, string>>(new Map())
  let loading = $state(true)
  let error = $state('')
  // Per-token new-IP note: token id → { ip, when } of the most recent
  // token.new_ip audit event (value-free — IP + timestamp only).
  let newIPByToken = $state<Map<string, { ip: string; when: string }>>(new Map())

  let minting = $state(false)
  let mintName = $state('')
  let mintScopeKind = $state<'config' | 'environment' | 'transit'>('config')
  let mintScopeId = $state('')
  let mintAccess = $state<'read' | 'readwrite' | 'transit'>('read')
  let mintTtl = $state('')
  let mintAllowlist = $state('')
  let minted = $state<MintTokenResult | null>(null)
  let mintError = $state('')

  $effect(() => {
    void load()
  })

  // Parse a comma/newline/space-separated CIDR list into a trimmed array.
  function parseAllowlist(raw: string): string[] {
    return raw.split(/[\s,]+/).map(s => s.trim()).filter(Boolean)
  }

  async function load() {
    loading = true
    error = ''
    try {
      tokens = await api.listTokens()
      const users = await api.listUsers().catch(() => [])
      userEmails = new Map(users.map(u => [u.id, u.email]))
      await loadNewIPs()
    } catch (err) {
      error = errorMessage(err, 'Could not list tokens.')
    } finally {
      loading = false
    }
  }

  // Recent new-IP sightings, from the value-free token.new_ip audit events.
  // Tolerant of a 403 (non-admins simply see no badges).
  async function loadNewIPs() {
    const m = new Map<string, { ip: string; when: string }>()
    try {
      const { events } = await api.listAuditEvents({ action: 'token.new_ip', limit: 100 })
      for (const e of events) {
        const id = e.resource.startsWith('tokens/') ? e.resource.slice('tokens/'.length) : ''
        if (!id || m.has(id)) continue // events are newest-first; keep the first (latest)
        const ip = e.detail?.startsWith('ip=') ? e.detail.slice(3) : (e.ip ?? '')
        m.set(id, { ip, when: e.occurred_at })
      }
    } catch {
      /* 403 or no audit access → no badges */
    }
    newIPByToken = m
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
      const allowlist = parseAllowlist(mintAllowlist)
      minted = await api.mintToken({
        name: mintName.trim(),
        scope: { kind: mintScopeKind, id: mintScopeKind === 'transit' ? 'transit' : mintScopeId },
        access,
        ...(mintTtl ? { ttl_seconds: Number(mintTtl) * 3600 } : {}),
        ...(allowlist.length ? { ip_allowlist: allowlist } : {}),
      })
      mintAllowlist = ''
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

  async function editAllowlist(t: TokenMeta) {
    const current = (t.ip_allowlist ?? []).join(', ')
    const raw = await dialog.prompt({
      title: `IP allowlist — ${t.name}`,
      body: 'Comma- or space-separated CIDRs (IPv4 or IPv6). Empty allows any IP. Requests from outside the list are rejected.',
      placeholder: '10.0.0.0/8, 2001:db8::/32',
      initial: current,
      confirmLabel: 'Save',
    })
    if (raw === null) return // cancelled
    try {
      await api.updateTokenAllowlist(t.id, parseAllowlist(raw))
      await load()
    } catch (err) {
      error = errorMessage(err, 'Could not update allowlist.')
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

  const STALE_MS = 90 * 24 * 3600_000 // 90 days
  // A token is stale if it has never authenticated or its last use is 90d+ old.
  function isStale(t: TokenMeta): boolean {
    if (!t.last_used_at) return true
    return Date.now() - new Date(t.last_used_at).getTime() >= STALE_MS
  }
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
          <div class="field grow">
            <label class="label" for="tk-allow">IP allowlist (CIDRs, blank = any IP)</label>
            <input id="tk-allow" class="input" bind:value={mintAllowlist} placeholder="10.0.0.0/8, 2001:db8::/32" />
          </div>
          <button class="btn btn-stamp" type="submit" disabled={!mintName.trim() || (mintScopeKind !== 'transit' && !mintScopeId)}>Mint</button>
          {#if mintError}<p class="error">{mintError}</p>{/if}
        </form>
      {/if}
    </div>
  {/if}

  {#if error}<p class="error rise">{error}</p>{/if}

  <div class="sheet table-wrap rise" style="animation-delay: 60ms">
    <table class="ledger" aria-label="Service tokens">
      <thead>
        <tr>
          <th scope="col">Token</th>
          <th scope="col">Scope</th>
          <th scope="col" style="width: 110px">Access</th>
          <th scope="col" style="width: 180px">IP allowlist</th>
          <th scope="col" style="width: 160px">Last used</th>
          <th scope="col" style="width: 150px">Expiry</th>
          <th scope="col" style="width: 150px"></th>
        </tr>
      </thead>
      <tbody>
        {#each active as t (t.id)}
          <tr>
            <td>
              <span class="t-name">
                {t.name}
                {#if newIPByToken.get(t.id)}
                  {@const n = newIPByToken.get(t.id)!}
                  <span class="pill pill-newip" title={`First seen from ${n.ip} ${relTime(n.when)}`}>new IP</span>
                {/if}
              </span>
              <span class="folio">minted {stampDate(t.created_at)} by {minter(t.created_by)}</span>
            </td>
            <td class="mono scope">{scopeLabel(t)}</td>
            <td>
              {#if t.access === 'read'}<span class="pill pill-neutral">read</span>
              {:else if t.access === 'transit'}<span class="pill pill-info">transit</span>
              {:else}<span class="pill pill-info">read / write</span>{/if}
            </td>
            <td>
              {#if t.ip_allowlist && t.ip_allowlist.length}
                <div class="allow">
                  {#each t.ip_allowlist as c}<code class="mono cidr">{c}</code>{/each}
                </div>
              {:else}
                <span class="folio">any IP</span>
              {/if}
            </td>
            <td>
              {#if !t.last_used_at}
                <span class="pill pill-stale" title="This token has never authenticated a request">never used</span>
              {:else}
                <span class="folio">{relTime(t.last_used_at)}</span>
                {#if isStale(t)}
                  <span class="pill pill-stale" title="No use in 90+ days">stale</span>
                {/if}
              {/if}
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
              {#if t.scope_kind !== 'transit'}
                <button class="btn btn-ghost btn-sm" onclick={() => editAllowlist(t)}>IPs</button>
              {/if}
              <button class="btn btn-ghost btn-sm revoke" onclick={() => revoke(t)}>Revoke</button>
            </td>
          </tr>
        {:else}
          <tr><td colspan="7" class="empty folio">{loading ? 'Reading…' : 'No service tokens minted yet.'}</td></tr>
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
  .pill-stale { color: var(--ochre); background: var(--ochre-wash); margin-left: var(--s2); }
  .pill-newip { color: var(--vermilion); background: var(--vermilion-wash); margin-left: var(--s2); }
  .allow { display: flex; flex-wrap: wrap; gap: var(--s1); }
  .cidr {
    font-size: var(--text-xs);
    background: var(--paper-low);
    border: 1px solid var(--rule);
    border-radius: var(--radius);
    padding: 1px var(--s2);
    color: var(--ink-soft);
  }
  .row-actions { text-align: right; }
  .revoke:hover { color: var(--vermilion); }
  .empty { text-align: center; padding: var(--s6) !important; }
  .foot-note { margin-top: var(--s3); max-width: 70ch; }
</style>
