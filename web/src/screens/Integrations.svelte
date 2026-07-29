<script lang="ts">
  import {
    api, errorMessage,
    type OIDCProviderView, type FederationConfigView, type FederationBindingView, type SyncTargetApi,
  } from '../lib/api'
  import { registry } from '../lib/registry.svelte'
  import { listAllSyncs } from '../lib/ops'
  import { dialog } from '../lib/dialog.svelte'
  import { relTime } from '../lib/util'
  import { federationProviders, providerFor, bindingClaimSummary } from '../lib/federation'

  let oidc = $state<OIDCProviderView | null>(null)
  // Every trusted federation issuer: a CI provider and a Kubernetes cluster can
  // be trusted at the same time, and each binding is pinned to one of them.
  let issuers = $state<FederationConfigView[]>([])
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

  /* trusted-issuer form */
  let editFed = $state(false)
  let fProvider = $state('github')
  let fIssuer = $state('https://token.actions.githubusercontent.com')
  let fAudience = $state('')
  // Optional PEM CA bundle for this issuer's discovery + JWKS TLS. Public
  // material, so unlike the sync providers' credentials it is loaded back from
  // the server for editing rather than held write-only.
  let fCaCert = $state('')
  let fError = $state('')

  const formProvider = $derived(providerFor(fIssuer, fProvider))

  function pickProvider(id: string) {
    fProvider = id
    const p = federationProviders.find(x => x.id === id)
    // custom / CircleCI / Kubernetes: the admin supplies the URL.
    fIssuer = p?.issuer ?? ''
  }

  /* binding form */
  let addingBinding = $state(false)
  let bName = $state('')
  let bIssuerId = $state('')
  let bClaims = $state<Record<string, string>>({})
  let bScopeKind = $state<'config' | 'environment'>('config')
  let bScopeId = $state('')
  let bAccess = $state<'read' | 'readwrite'>('read')
  let bTtl = $state(900)
  let bError = $state('')

  // The issuer a new binding is pinned to, and the claim fields it demands.
  const bIssuer = $derived(issuers.find(i => i.id === bIssuerId) ?? issuers[0])
  const bProvider = $derived(providerFor(bIssuer?.issuer ?? '', bIssuer?.preset))

  $effect(() => {
    void load()
  })

  async function load() {
    oidc = await api.getOIDCConfig().catch(() => null)
    issuers = await api.listFederationIssuers().catch(() => [])
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
      await api.putFederationIssuer({
        issuer: fIssuer.trim(), audience: fAudience.trim(), preset: fProvider,
        // Sent on every save, empty included: an empty bundle is how an operator
        // goes back to the system roots, so it cannot mean "leave unchanged".
        ca_cert: fCaCert.trim(), enabled: true,
      })
      editFed = false
      flash('Trusted issuer saved.')
      await load()
    } catch (err) {
      fError = errorMessage(err, 'Could not save the trusted issuer.')
    }
  }

  async function removeIssuer(iss: FederationConfigView) {
    const ok = await dialog.confirm({
      title: `Stop trusting ${iss.issuer}?`,
      body: 'Its trust bindings stay but can never match again until the issuer is trusted anew.',
      confirmLabel: 'Stop trusting',
      danger: true,
    })
    if (!ok || !iss.id) return
    try {
      await api.deleteFederationIssuer(iss.id)
      flash('Issuer removed.')
      await load()
    } catch (err) {
      flash(errorMessage(err, 'Remove failed.'))
    }
  }

  function startIssuerAdd() {
    editFed = true
    fAudience = ''
    fCaCert = ''
    fError = ''
    pickProvider(issuers.length ? 'kubernetes' : 'github')
  }

  function startIssuerEdit(iss: FederationConfigView) {
    editFed = true
    fProvider = providerFor(iss.issuer, iss.preset).id
    fIssuer = iss.issuer
    fAudience = iss.audience
    fCaCert = iss.ca_cert ?? ''
    fError = ''
  }

  function startBindingAdd() {
    addingBinding = true
    bIssuerId = issuers[0]?.id ?? ''
    bClaims = {}
    bError = ''
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
        issuer: bIssuer?.issuer ?? '',
        match_claims: Object.fromEntries(
          bProvider.claims.map(c => [c.key, (bClaims[c.key] ?? '').trim()]),
        ),
        scope_kind: bScopeKind, scope_id: bScopeId,
        access: bAccess, ttl_seconds: bTtl, enabled: true,
      })
      addingBinding = false
      bName = ''; bClaims = {}; bScopeId = ''
      flash('Trust binding created.')
      await load()
    } catch (err) {
      bError = errorMessage(err, 'Could not create the binding.')
    }
  }

  async function removeBinding(b: FederationBindingView) {
    const ok = await dialog.confirm({
      title: `Delete trust binding ${b.name}?`,
      body: `Workflows matching ${bindingClaimSummary(b.match_claims)} can no longer federate.`,
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
      <h3>Machine identity federation</h3>
      <span class="folio">exchange a workload OIDC JWT for a short-lived scoped token · GitHub Actions · GitLab · Buildkite · CircleCI · Kubernetes service accounts · several issuers can be trusted at once</span>
    </div>
    <div class="sheet card">
      {#each issuers as iss (iss.id ?? iss.issuer)}
        <div class="row">
          <div>
            <span class="t-name">{providerFor(iss.issuer, iss.preset).label} <span class="pill" class:pill-info={iss.enabled} class:pill-neutral={!iss.enabled}>{iss.enabled ? 'active' : 'off'}</span></span>
            <span class="folio mono">{iss.issuer} · aud {iss.audience}{iss.ca_cert ? ' · custom CA' : ''}</span>
          </div>
          <div class="row-actions">
            <button class="btn btn-sm" onclick={() => startIssuerEdit(iss)}>Edit</button>
            <button class="btn btn-ghost btn-sm del-btn" onclick={() => removeIssuer(iss)}>Remove</button>
          </div>
        </div>
      {/each}

      {#if editFed}
        <form class="grid-form" onsubmit={saveFed}>
          <label class="field"><span class="label">Provider</span>
            <select class="select" value={fProvider} onchange={(e) => pickProvider((e.currentTarget as HTMLSelectElement).value)}>
              {#each federationProviders as p}<option value={p.id}>{p.label}</option>{/each}
            </select>
          </label>
          <label class="field"><span class="label">Issuer</span><input class="input mono" bind:value={fIssuer} placeholder="https://oidc.eks.eu-west-1.amazonaws.com/id/EXAMPLE" required /></label>
          <label class="field wide"><span class="label">Audience</span><input class="input mono" bind:value={fAudience} placeholder="https://janus.company.dev" required /></label>
          <label class="field wide"><span class="label">CA certificate <span class="folio">(optional, PEM)</span></span>
            <textarea class="input mono" rows="3" bind:value={fCaCert} placeholder="-----BEGIN CERTIFICATE-----"></textarea></label>
          <p class="folio wide">Leave the CA empty to verify the issuer against the system roots. A bundle <strong>replaces</strong> them for this issuer — nothing else is trusted for it.{#if formProvider.caHint}&nbsp;{formProvider.caHint}{/if}</p>
          <p class="folio wide">Bind the <span class="mono">{formProvider.claims.map(c => c.key).join(' + ')}</span> claim{formProvider.claims.length > 1 ? 's' : ''} for this provider to identify trusted workloads.{#if formProvider.hint}&nbsp;{formProvider.hint}{/if}</p>
          {#if fError}<p class="error wide">{fError}</p>{/if}
          <div class="form-actions wide">
            <button class="btn btn-ghost" type="button" onclick={() => (editFed = false)}>Cancel</button>
            <button class="btn btn-stamp" type="submit">Save</button>
          </div>
        </form>
      {:else}
        <div class="row">
          <span class="folio">{issuers.length ? 'Trust another issuer — a CI provider and a Kubernetes cluster can both federate.' : 'Not configured — CI and workloads must use long-lived service tokens.'}</span>
          <button class="btn btn-sm" onclick={startIssuerAdd}>{issuers.length ? '+ Trusted issuer' : 'Configure'}</button>
        </div>
      {/if}

      {#if issuers.length}
        <table class="ledger bindings" aria-label="Federation trust bindings">
          <thead>
            <tr><th scope="col">Trust binding</th><th scope="col">Issuer</th><th scope="col">Identity claims</th><th scope="col">Scope</th><th scope="col" style="width:90px">Access</th><th scope="col" style="width:80px">TTL</th><th scope="col" style="width:90px"></th></tr>
          </thead>
          <tbody>
            {#each bindings as b (b.id)}
              <tr>
                <td class="t-name">{b.name}</td>
                <td class="mono small">{providerFor(b.issuer ?? '', issuers.find(i => i.issuer === b.issuer)?.preset).label}</td>
                <td class="mono small">{bindingClaimSummary(b.match_claims)}</td>
                <td class="mono small">{b.scope_kind === 'config' ? registry.configLabel(b.scope_id) : b.scope_id}</td>
                <td><span class="pill pill-neutral">{b.access}</span></td>
                <td class="folio">{Math.round(b.ttl_seconds / 60)}m</td>
                <td class="row-actions"><button class="btn btn-ghost btn-sm del-btn" onclick={() => removeBinding(b)}>Delete</button></td>
              </tr>
            {:else}
              <tr><td colspan="7" class="folio">No trust bindings — a workload cannot federate until one matches its identity claims.</td></tr>
            {/each}
          </tbody>
        </table>
        {#if addingBinding}
          <form class="grid-form binding-form" onsubmit={addBinding}>
            <label class="field"><span class="label">Name</span><input class="input" bind:value={bName} placeholder="atlas-ci" required /></label>
            <label class="field"><span class="label">Issuer</span>
              <select class="select" bind:value={bIssuerId}>
                {#each issuers as iss}<option value={iss.id}>{providerFor(iss.issuer, iss.preset).label} · {iss.issuer}</option>{/each}
              </select>
            </label>
            {#each bProvider.claims as c (c.key)}
              <label class="field"><span class="label">{c.label} <span class="folio mono">({c.key})</span></span><input class="input mono" bind:value={bClaims[c.key]} placeholder={c.example} required /></label>
            {/each}
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
          <button class="btn btn-sm add-binding" onclick={startBindingAdd}>+ Trust binding</button>
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
