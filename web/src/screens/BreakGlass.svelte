<script lang="ts">
  import { api, errorMessage, type BreakGlassGrant, type BreakGlassScopeLevel, type Role } from '../lib/api'
  import { registry } from '../lib/registry.svelte'
  import { dialog } from '../lib/dialog.svelte'

  const ROLES: Role[] = ['developer', 'admin', 'owner']

  let grants = $state<BreakGlassGrant[]>([])
  let loading = $state(true)
  let error = $state('')
  let note = $state('')

  // activation form
  let scope = $state<BreakGlassScopeLevel>('project')
  let pid = $state('')
  let eid = $state('')
  let role = $state<Role>('admin')
  let ttl = $state('30m')
  let reason = $state('')
  let formError = $state('')
  let activating = $state(false)

  // A ticking clock so the countdowns update once a second.
  let nowMs = $state(Date.now())
  $effect(() => {
    const t = setInterval(() => (nowMs = Date.now()), 1000)
    return () => clearInterval(t)
  })

  const envOptions = $derived(registry.findProject(pid)?.environments ?? [])

  $effect(() => {
    if (!pid && registry.projects.length) pid = registry.projects[0].id
  })
  $effect(() => {
    if (scope === 'environment' && !envOptions.some((e) => e.id === eid)) eid = envOptions[0]?.id ?? ''
  })

  $effect(() => { void load() })

  async function load() {
    loading = true
    error = ''
    try {
      grants = await api.listBreakGlass()
    } catch (err) {
      error = errorMessage(err, 'Could not list break-glass grants.')
    } finally {
      loading = false
    }
  }

  function flash(msg: string) {
    note = msg
    setTimeout(() => (note = ''), 3500)
  }

  function scopeLabel(g: BreakGlassGrant): string {
    if (g.scope_level === 'instance') return 'Instance'
    if (g.scope_level === 'project') {
      const p = g.project_id ? registry.findProject(g.project_id) : null
      return `Project · ${p?.name ?? g.project_id}`
    }
    // environment
    return `Environment · ${g.environment_id ?? ''}`
  }

  function remaining(g: BreakGlassGrant): string {
    const ms = new Date(g.expires_at).getTime() - nowMs
    if (ms <= 0) return 'expired'
    const total = Math.floor(ms / 1000)
    const h = Math.floor(total / 3600)
    const m = Math.floor((total % 3600) / 60)
    const s = total % 60
    if (h > 0) return `${h}h ${m}m`
    if (m > 0) return `${m}m ${s}s`
    return `${s}s`
  }

  async function activate(e: SubmitEvent) {
    e.preventDefault()
    formError = ''
    if (reason.trim() === '') { formError = 'A reason is required — it is stamped into the audit chain.'; return }
    if (scope === 'project' && !pid) { formError = 'Choose a project.'; return }
    if (scope === 'environment' && !eid) { formError = 'Choose an environment.'; return }
    activating = true
    try {
      await api.activateBreakGlass({
        scope_level: scope,
        ...(scope === 'project' ? { project_id: pid } : {}),
        ...(scope === 'environment' ? { environment_id: eid } : {}),
        role,
        reason: reason.trim(),
        ...(ttl.trim() ? { ttl: ttl.trim() } : {}),
      })
      reason = ''
      flash('Break-glass activated. Everyone subscribed has been alerted.')
      await load()
    } catch (err) {
      formError = errorMessage(err, 'Could not activate break-glass.')
    } finally {
      activating = false
    }
  }

  async function revoke(g: BreakGlassGrant) {
    const ok = await dialog.confirm({
      title: 'End break-glass early?',
      body: `This ends the ${g.elevated_role} elevation on ${scopeLabel(g)} immediately.`,
      confirmLabel: 'End now',
      danger: true,
    })
    if (!ok) return
    try {
      await api.revokeBreakGlass(g.id)
      flash('Grant ended.')
      await load()
    } catch (err) {
      error = errorMessage(err, 'Could not revoke the grant.')
    }
  }
</script>

<div class="bg-screen">
  <header class="bg-head">
    <span class="stamp">Break-glass</span>
    <h1>Emergency access</h1>
    <p class="sub">
      Time-boxed role elevation on a scope you already hold a role on. Every activation is
      <strong>loud</strong>: stamped into the audit chain and forwarded to your notification channels.
      Grants auto-expire — this is a paved path, not shared root credentials.
    </p>
  </header>

  {#if note}<div class="banner ok" role="status">{note}</div>{/if}
  {#if error}<div class="banner err" role="alert">{error}</div>{/if}

  <section class="plate loud">
    <h2>Activate</h2>
    <form onsubmit={activate} class="form">
      <div class="row">
        <label>
          <span>Scope</span>
          <select bind:value={scope}>
            <option value="instance">Instance</option>
            <option value="project">Project</option>
            <option value="environment">Environment</option>
          </select>
        </label>
        {#if scope === 'project' || scope === 'environment'}
          <label>
            <span>Project</span>
            <select bind:value={pid}>
              {#each registry.projects as p}<option value={p.id}>{p.name}</option>{/each}
            </select>
          </label>
        {/if}
        {#if scope === 'environment'}
          <label>
            <span>Environment</span>
            <select bind:value={eid}>
              {#each envOptions as env}<option value={env.id}>{env.name}</option>{/each}
            </select>
          </label>
        {/if}
      </div>

      <div class="row">
        <label>
          <span>Elevate to</span>
          <select bind:value={role}>
            {#each ROLES as r}<option value={r}>{r}</option>{/each}
          </select>
        </label>
        <label>
          <span>Duration (TTL)</span>
          <input bind:value={ttl} placeholder="30m" spellcheck="false" />
        </label>
      </div>

      <label class="full">
        <span>Reason (required)</span>
        <textarea bind:value={reason} rows="2" maxlength="1000"
          placeholder="Why is emergency access needed? This is recorded, non-secret."></textarea>
      </label>

      {#if formError}<p class="form-err" role="alert">{formError}</p>{/if}

      <div class="actions">
        <button type="submit" class="btn btn-stamp" disabled={activating}>
          {activating ? 'Activating…' : 'Break the glass'}
        </button>
      </div>
    </form>
  </section>

  <section class="plate">
    <h2>Active grants</h2>
    {#if loading}
      <p class="muted">Loading…</p>
    {:else if grants.length === 0}
      <p class="muted">No active break-glass grants.</p>
    {:else}
      <table class="ledger">
        <thead>
          <tr><th>Scope</th><th>Role</th><th>Reason</th><th>Expires in</th><th></th></tr>
        </thead>
        <tbody>
          {#each grants as g (g.id)}
            <tr>
              <td>{scopeLabel(g)}</td>
              <td><span class="pill pill-prod">{g.elevated_role}</span></td>
              <td class="reason">{g.reason}</td>
              <td class="mono">{remaining(g)}</td>
              <td class="right">
                <button class="btn btn-sm" onclick={() => revoke(g)}>End</button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </section>
</div>

<style>
  .bg-screen { display: flex; flex-direction: column; gap: var(--s4); max-width: 880px; }
  .bg-head h1 { margin: var(--s2) 0 var(--s2); }
  .sub { color: var(--ink-soft); max-width: 62ch; }
  .banner { padding: var(--s2) var(--s3); border-radius: var(--radius); border: 1px solid var(--rule); }
  .banner.ok { color: var(--verdigris); background: var(--verdigris-wash); }
  .banner.err { color: var(--vermilion); background: var(--vermilion-wash); }
  .plate { padding: var(--s4); border: 1px solid var(--rule); border-radius: var(--radius); background: var(--paper); }
  .plate.loud { border-color: var(--vermilion-deep); box-shadow: inset 0 0 0 1px var(--vermilion-wash); }
  .plate h2 { margin: 0 0 var(--s3); }
  .form { display: flex; flex-direction: column; gap: var(--s3); }
  .row { display: flex; gap: var(--s3); flex-wrap: wrap; }
  label { display: flex; flex-direction: column; gap: var(--s1); flex: 1 1 180px; }
  label.full { flex-basis: 100%; }
  label span { font-size: 0.82rem; color: var(--ink-soft); }
  select, input, textarea {
    padding: var(--s2); border: 1px solid var(--rule); border-radius: var(--radius);
    background: var(--paper-low); color: var(--ink); font: inherit;
  }
  textarea { resize: vertical; }
  .actions { display: flex; justify-content: flex-end; }
  .form-err { color: var(--vermilion); }
  .muted { color: var(--ink-soft); }
  .reason { color: var(--ink-soft); max-width: 30ch; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .mono { font-family: var(--font-mono); }
  .right { text-align: right; }
  .btn-sm { padding: 2px var(--s2); font-size: 0.82rem; }
</style>
