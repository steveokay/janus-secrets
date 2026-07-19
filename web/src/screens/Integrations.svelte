<script lang="ts">
  import {
    api, errorMessage,
    type OIDCProviderView, type FederationConfigView, type FederationBindingView, type SyncTargetApi,
  } from '../lib/api'
  import { registry } from '../lib/registry.svelte'
  import { listAllSyncs } from '../lib/ops'
  import { dialog } from '../lib/dialog.svelte'
  import { relTime } from '../lib/util'

  let oidc = $state<OIDCProviderView | null>(null)
  let fed = $state<FederationConfigView | null>(null)
  let bindings = $state<FederationBindingView[]>([])
  let syncs = $state<SyncTargetApi[]>([])
  let note = $state('')

  /* OIDC form */
  let editOidc = $state(false)
  let oName = $state('')
  let oIssuer = $state('')
  let oClientId = $state('')
  let oSecret = $state('')
  let oRedirect = $state('')
  let oError = $state('')

  /* federation form */
  let editFed = $state(false)
  let fIssuer = $state('https://token.actions.githubusercontent.com')
  let fAudience = $state('')
  let fError = $state('')

  /* binding form */
  let addingBinding = $state(false)
  let bName = $state('')
  let bRepo = $state('')
  let bScopeKind = $state<'config' | 'environment'>('config')
  let bScopeId = $state('')
  let bAccess = $state<'read' | 'readwrite'>('read')
  let bTtl = $state(900)
  let bError = $state('')

  $effect(() => {
    void load()
  })

  async function load() {
    oidc = await api.getOIDCConfig().catch(() => null)
    fed = await api.getFederationConfig().catch(() => null)
    bindings = await api.listFederationBindings().catch(() => [])
    syncs = await listAllSyncs(registry.projects).catch(() => [])
  }

  function flash(msg: string) {
    note = msg
    setTimeout(() => (note = ''), 3200)
  }

  function startOidcEdit() {
    editOidc = true
    oName = oidc?.name ?? ''
    oIssuer = oidc?.issuer ?? ''
    oClientId = oidc?.client_id ?? ''
    oRedirect = oidc?.redirect_url ?? `${location.origin}/v1/auth/oidc/callback`
    oSecret = ''
    oError = ''
  }

  async function saveOidc(e: SubmitEvent) {
    e.preventDefault()
    oError = ''
    try {
      await api.setOIDCConfig({
        name: oName.trim(), issuer: oIssuer.trim(), client_id: oClientId.trim(),
        client_secret: oSecret, scopes: ['openid', 'email', 'profile'],
        redirect_url: oRedirect.trim(), enabled: true,
      })
      editOidc = false
      flash('OIDC provider saved.')
      await load()
    } catch (err) {
      oError = errorMessage(err, 'Could not save the provider (the client secret is required on every save).')
    }
  }

  async function removeOidc() {
    const ok = await dialog.confirm({
      title: 'Remove the OIDC provider?',
      body: 'SSO sign-in stops working; email + password remains.',
      confirmLabel: 'Remove',
      danger: true,
    })
    if (!ok) return
    try {
      await api.deleteOIDCConfig()
      flash('OIDC provider removed.')
      await load()
    } catch (err) {
      flash(errorMessage(err, 'Remove failed.'))
    }
  }

  async function saveFed(e: SubmitEvent) {
    e.preventDefault()
    fError = ''
    try {
      await api.setFederationConfig({ issuer: fIssuer.trim(), audience: fAudience.trim(), enabled: true })
      editFed = false
      flash('Federation config saved.')
      await load()
    } catch (err) {
      fError = errorMessage(err, 'Could not save the federation config.')
    }
  }

  const scopeOptions = $derived(
    bScopeKind === 'config'
      ? registry.projects.flatMap(p => p.environments.flatMap(e => e.configs.map(c => ({ id: c.id, label: `${p.name} / ${e.slug} / ${c.name}` }))))
      : registry.projects.flatMap(p => p.environments.map(e => ({ id: e.id, label: `${p.name} / ${e.slug}` }))),
  )

  async function addBinding(e: SubmitEvent) {
    e.preventDefault()
    bError = ''
    try {
      await api.createFederationBinding({
        name: bName.trim(),
        match_claims: { repository: bRepo.trim() },
        scope_kind: bScopeKind, scope_id: bScopeId,
        access: bAccess, ttl_seconds: bTtl, enabled: true,
      })
      addingBinding = false
      bName = ''; bRepo = ''; bScopeId = ''
      flash('Trust binding created.')
      await load()
    } catch (err) {
      bError = errorMessage(err, 'Could not create the binding.')
    }
  }

  async function removeBinding(b: FederationBindingView) {
    const ok = await dialog.confirm({
      title: `Delete trust binding ${b.name}?`,
      body: `Workflows from ${b.match_claims.repository} can no longer federate.`,
      confirmLabel: 'Delete binding',
      danger: true,
    })
    if (!ok) return
    try {
      await api.deleteFederationBinding(b.id)
      flash('Binding deleted.')
      await load()
    } catch (err) {
      flash(errorMessage(err, 'Delete failed.'))
    }
  }
</script>

<div class="page-n">
  <header class="page-head rise">
    <div>
      <p class="folio">Instruments · the outside world — SSO, CI identity, outbound sync</p>
      <h1>Integrations</h1>
    </div>
    {#if note}<span class="pill pill-info">{note}</span>{/if}
  </header>
  <hr class="ledger-rule" />

  <!-- OIDC provider -->
  <section class="op-section rise">
    <div class="section-head">
      <h3>OIDC single sign-on</h3>
      <span class="folio">Authorization Code + PKCE · client secret master-key-wrapped, write-only</span>
    </div>
    <div class="sheet card">
      {#if editOidc}
        <form class="grid-form" onsubmit={saveOidc}>
          <label class="field"><span class="label">Display name</span><input class="input" bind:value={oName} placeholder="GitHub" required /></label>
          <label class="field"><span class="label">Issuer URL</span><input class="input mono" bind:value={oIssuer} placeholder="https://token.actions…" required /></label>
          <label class="field"><span class="label">Client ID</span><input class="input mono" bind:value={oClientId} required /></label>
          <label class="field"><span class="label">Client secret {oidc?.secret_set ? '(re-enter to save)' : ''}</span><input class="input mono" type="password" bind:value={oSecret} required /></label>
          <label class="field wide"><span class="label">Redirect URL</span><input class="input mono" bind:value={oRedirect} required /></label>
          {#if oError}<p class="error wide">{oError}</p>{/if}
          <div class="form-actions wide">
            <button class="btn btn-ghost" type="button" onclick={() => (editOidc = false)}>Cancel</button>
            <button class="btn btn-stamp" type="submit">Save provider</button>
          </div>
        </form>
      {:else if oidc}
        <div class="row">
          <div>
            <span class="t-name">{oidc.name} <span class="pill" class:pill-info={oidc.enabled} class:pill-neutral={!oidc.enabled}>{oidc.enabled ? 'active' : 'off'}</span></span>
            <span class="folio mono">{oidc.issuer} · client {oidc.client_id} · secret {oidc.secret_set ? 'set' : 'missing'}</span>
          </div>
          <div class="row-actions">
            <button class="btn btn-sm" onclick={startOidcEdit}>Edit</button>
            <button class="btn btn-sm btn-ghost del-btn" onclick={removeOidc}>Remove</button>
          </div>
        </div>
      {:else}
        <div class="row">
          <span class="folio">No provider configured — humans sign in with email + password only.</span>
          <button class="btn btn-sm" onclick={startOidcEdit}>Configure</button>
        </div>
      {/if}
    </div>
  </section>

  <!-- CI federation -->
  <section class="op-section rise" style="animation-delay: 60ms">
    <div class="section-head">
      <h3>CI federation — GitHub Actions</h3>
      <span class="folio">exchange a workflow OIDC JWT for a short-lived scoped token · no long-lived secret in CI</span>
    </div>
    <div class="sheet card">
      {#if editFed}
        <form class="grid-form" onsubmit={saveFed}>
          <label class="field"><span class="label">Issuer</span><input class="input mono" bind:value={fIssuer} required /></label>
          <label class="field"><span class="label">Audience</span><input class="input mono" bind:value={fAudience} placeholder="https://janus.company.dev" required /></label>
          {#if fError}<p class="error wide">{fError}</p>{/if}
          <div class="form-actions wide">
            <button class="btn btn-ghost" type="button" onclick={() => (editFed = false)}>Cancel</button>
            <button class="btn btn-stamp" type="submit">Save</button>
          </div>
        </form>
      {:else if fed}
        <div class="row">
          <div>
            <span class="t-name">Federation <span class="pill" class:pill-info={fed.enabled} class:pill-neutral={!fed.enabled}>{fed.enabled ? 'active' : 'off'}</span></span>
            <span class="folio mono">{fed.issuer} · aud {fed.audience}</span>
          </div>
          <button class="btn btn-sm" onclick={() => { editFed = true; fIssuer = fed!.issuer; fAudience = fed!.audience }}>Edit</button>
        </div>
      {:else}
        <div class="row">
          <span class="folio">Not configured — CI must use long-lived service tokens.</span>
          <button class="btn btn-sm" onclick={() => (editFed = true)}>Configure</button>
        </div>
      {/if}

      {#if fed}
        <table class="ledger bindings">
          <thead>
            <tr><th>Trust binding</th><th>Repository claim</th><th>Scope</th><th style="width:90px">Access</th><th style="width:80px">TTL</th><th style="width:90px"></th></tr>
          </thead>
          <tbody>
            {#each bindings as b (b.id)}
              <tr>
                <td class="t-name">{b.name}</td>
                <td class="mono small">{b.match_claims.repository}</td>
                <td class="mono small">{b.scope_kind === 'config' ? registry.configLabel(b.scope_id) : b.scope_id}</td>
                <td><span class="pill pill-neutral">{b.access}</span></td>
                <td class="folio">{Math.round(b.ttl_seconds / 60)}m</td>
                <td class="row-actions"><button class="btn btn-ghost btn-sm del-btn" onclick={() => removeBinding(b)}>Delete</button></td>
              </tr>
            {:else}
              <tr><td colspan="6" class="folio">No trust bindings — a workflow cannot federate until one matches its repository.</td></tr>
            {/each}
          </tbody>
        </table>
        {#if addingBinding}
          <form class="grid-form binding-form" onsubmit={addBinding}>
            <label class="field"><span class="label">Name</span><input class="input" bind:value={bName} placeholder="atlas-ci" required /></label>
            <label class="field"><span class="label">Repository</span><input class="input mono" bind:value={bRepo} placeholder="acme/atlas-api" required /></label>
            <label class="field"><span class="label">Scope kind</span>
              <select class="select" bind:value={bScopeKind}><option value="config">config</option><option value="environment">environment</option></select>
            </label>
            <label class="field"><span class="label">Scope</span>
              <select class="select" bind:value={bScopeId} required>
                <option value="" disabled>choose…</option>
                {#each scopeOptions as o}<option value={o.id}>{o.label}</option>{/each}
              </select>
            </label>
            <label class="field"><span class="label">Access</span>
              <select class="select" bind:value={bAccess}><option value="read">read</option><option value="readwrite">read / write</option></select>
            </label>
            <label class="field"><span class="label">TTL seconds (≤3600)</span><input class="input" type="number" min="60" max="3600" bind:value={bTtl} /></label>
            {#if bError}<p class="error wide">{bError}</p>{/if}
            <div class="form-actions wide">
              <button class="btn btn-ghost" type="button" onclick={() => (addingBinding = false)}>Cancel</button>
              <button class="btn btn-stamp" type="submit" disabled={!bScopeId}>Create binding</button>
            </div>
          </form>
        {:else}
          <button class="btn btn-sm add-binding" onclick={() => (addingBinding = true)}>+ Trust binding</button>
        {/if}
      {/if}
    </div>
  </section>

  <!-- outbound sync summary -->
  <section class="op-section rise" style="animation-delay: 120ms">
    <div class="section-head">
      <h3>Outbound sync</h3>
      <a class="folio" href="/operations">Manage in Operations →</a>
    </div>
    <div class="sheet card">
      {#if syncs.length}
        {#each syncs as s (s.id)}
          <div class="row sync-row">
            <div>
              <span class="t-name">{s.provider === 'github' ? 'GitHub Actions secrets' : 'Kubernetes Secret'}</span>
              <span class="folio mono">{registry.configLabel(s.config_id)} → {s.provider === 'github' ? `${s.addr.owner}/${s.addr.repo}` : `${s.addr.namespace}/${s.addr.secret_name}`}</span>
            </div>
            <span class="folio">{s.failure_count > 0 ? '⚠ failing' : `synced ${s.last_synced_at ? relTime(s.last_synced_at) : 'never'}`}</span>
          </div>
        {/each}
      {:else}
        <div class="row"><span class="folio">No sync targets. Create them with <code>janus sync create</code> — they replicate a config's resolved secrets outward.</span></div>
      {/if}
    </div>
  </section>
</div>

<style>
  .page-n { max-width: 1100px; margin: 0 auto; }
  .page-head { display: flex; justify-content: space-between; align-items: flex-end; gap: var(--s4); }
  .page-head h1 { margin-top: var(--s1); }

  .op-section { margin-top: var(--s6); }
  .section-head { display: flex; justify-content: space-between; align-items: baseline; margin-bottom: var(--s3); }
  .card { padding: var(--s4) var(--s5); }
  .row { display: flex; justify-content: space-between; align-items: center; gap: var(--s3); flex-wrap: wrap; }
  .sync-row { padding: var(--s2) 0; border-top: 1px solid var(--rule-faint); }
  .sync-row:first-child { border-top: 0; padding-top: 0; }
  .t-name { display: block; font-weight: 620; }
  .small { font-size: var(--text-xs); color: var(--ink-soft); }
  .row-actions { white-space: nowrap; }
  .del-btn:hover { color: var(--vermilion); }
  .error { color: var(--vermilion); font-size: var(--text-sm); }

  .grid-form {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--s3) var(--s4);
  }
  .field { display: flex; flex-direction: column; gap: var(--s1); }
  .wide { grid-column: 1 / -1; }
  .form-actions { display: flex; justify-content: flex-end; gap: var(--s3); }

  .bindings { margin-top: var(--s4); }
  .binding-form { margin-top: var(--s4); border-top: 1px dashed var(--rule); padding-top: var(--s4); }
  .add-binding { margin-top: var(--s3); }
</style>
