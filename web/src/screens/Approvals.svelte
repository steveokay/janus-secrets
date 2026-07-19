<script lang="ts">
  import { api, errorMessage, type PromotionRequest, type PromotionRequestDetail } from '../lib/api'
  import { registry } from '../lib/registry.svelte'
  import { relTime } from '../lib/util'

  let projectId = $state<string>('')
  let statusFilter = $state<'pending' | 'applied' | 'rejected' | 'cancelled' | ''>('pending')
  let requests = $state<PromotionRequest[]>([])
  let detail = $state<PromotionRequestDetail | null>(null)
  let rejectNote = $state('')
  let loading = $state(false)
  let error = $state('')
  let note = $state('')
  let userEmails = $state<Map<string, string>>(new Map())

  $effect(() => {
    api.listUsers().then(us => (userEmails = new Map(us.map(u => [u.id, u.email])))).catch(() => {})
  })

  const who = (id: string) => userEmails.get(id) ?? (id.includes('-') ? `${id.slice(0, 8)}…` : id)

  $effect(() => {
    if (!projectId && registry.projects.length) projectId = registry.projects[0].id
  })

  $effect(() => {
    if (projectId) void load(projectId, statusFilter)
  })

  async function load(pid: string, status: string) {
    loading = true
    error = ''
    detail = null
    try {
      requests = await api.listPromoteRequests(pid, status || undefined)
    } catch (err) {
      error = errorMessage(err, 'Could not list promotion requests.')
      requests = []
    } finally {
      loading = false
    }
  }

  async function open(r: PromotionRequest) {
    try {
      detail = await api.getPromoteRequest(r.id)
      rejectNote = ''
    } catch (err) {
      error = errorMessage(err, 'Could not open the request.')
    }
  }

  function flash(msg: string) {
    note = msg
    setTimeout(() => (note = ''), 3200)
  }

  async function approve(r: PromotionRequest) {
    try {
      const res = await api.approvePromoteRequest(r.id)
      flash(`Approved — target now v${res.target_version} (${res.applied.length} applied).`)
      await load(projectId, statusFilter)
      await registry.hydrate(true)
    } catch (err) {
      flash(errorMessage(err, 'Approval failed.'))
    }
  }

  async function reject(r: PromotionRequest) {
    try {
      await api.rejectPromoteRequest(r.id, rejectNote.trim() || 'rejected')
      flash('Request rejected.')
      await load(projectId, statusFilter)
    } catch (err) {
      flash(errorMessage(err, 'Reject failed.'))
    }
  }

  async function cancel(r: PromotionRequest) {
    try {
      await api.cancelPromoteRequest(r.id)
      flash('Request cancelled.')
      await load(projectId, statusFilter)
    } catch (err) {
      flash(errorMessage(err, 'Cancel failed.'))
    }
  }

  function envSlug(id: string): string {
    for (const p of registry.projects)
      for (const e of p.environments)
        if (e.id === id) return e.slug
    return id.slice(0, 8)
  }
</script>

<div class="page-n">
  <header class="page-head rise">
    <div>
      <p class="folio">Record · promotion requests — four-eyes review before prod changes</p>
      <h1>Approvals</h1>
    </div>
    <div class="head-actions">
      {#if note}<span class="pill pill-info">{note}</span>{/if}
      <select class="select" bind:value={projectId}>
        {#each registry.projects as p}
          <option value={p.id}>{p.name}</option>
        {/each}
      </select>
      <div class="seg" role="group">
        {#each ['pending', 'applied', 'rejected', ''] as f}
          <button class="seg-btn" class:on={statusFilter === f} onclick={() => (statusFilter = f as typeof statusFilter)}>
            {f === '' ? 'all' : f}
          </button>
        {/each}
      </div>
    </div>
  </header>
  <hr class="ledger-rule" />

  {#if error}<p class="error rise">{error}</p>{/if}

  <div class="sheet table-wrap rise" style="animation-delay: 60ms">
    <table class="ledger">
      <thead>
        <tr>
          <th>Request</th>
          <th style="width: 200px">Route</th>
          <th style="width: 90px">Keys</th>
          <th style="width: 110px">Status</th>
          <th style="width: 130px">Filed</th>
          <th style="width: 220px"></th>
        </tr>
      </thead>
      <tbody>
        {#each requests as r (r.id)}
          <tr>
            <td>
              <span class="r-note">{r.note || '(no note)'}</span>
              <span class="folio">by {who(r.requested_by)}{#if r.decided_by} · decided by {who(r.decided_by)}{/if}</span>
            </td>
            <td class="mono small">
              v{r.source_version} → {envSlug(r.target_env_id)}/{r.target_name || (r.target_config_id ? registry.findConfig(r.target_config_id)?.config.name ?? '?' : '?')}{r.create_target ? ' (new)' : ''}
            </td>
            <td class="num">{r.keys.length}</td>
            <td>
              {#if r.status === 'pending'}<span class="state warn">pending</span>
              {:else if r.status === 'applied'}<span class="state ok">applied{r.applied_target_version ? ` · v${r.applied_target_version}` : ''}</span>
              {:else if r.status === 'rejected'}<span class="state bad">rejected</span>
              {:else}<span class="folio">cancelled</span>{/if}
            </td>
            <td class="folio">{relTime(r.created_at)}</td>
            <td class="row-actions">
              <button class="btn btn-ghost btn-sm" onclick={() => open(r)}>Review</button>
              {#if r.status === 'pending'}
                <button class="btn btn-ghost btn-sm ok-btn" onclick={() => approve(r)}>Approve</button>
                <button class="btn btn-ghost btn-sm del-btn" onclick={() => cancel(r)}>Cancel</button>
              {/if}
            </td>
          </tr>
        {:else}
          <tr><td colspan="6" class="empty folio">
            {loading ? 'Reading…' : 'No promotion requests. File one from a config editor via Promote → Request approval.'}
          </td></tr>
        {/each}
      </tbody>
    </table>
  </div>

  {#if detail}
    <section class="sheet review rise">
      <div class="section-head">
        <h3>Review — {detail.note || detail.id.slice(0, 8)}</h3>
        <span class="folio">value-free diff · values are never shown in a request</span>
      </div>
      <div class="chips">
        {#each detail.diff?.entries ?? [] as e (e.key)}
          <span class="chg mono" class:add={e.status === 'add'} class:mod={e.status === 'change'} class:del={e.status === 'remove'}>
            {e.status === 'add' ? '+' : e.status === 'change' ? '~' : e.status === 'remove' ? '−' : '='} {e.key}{e.locked ? ' ⚿' : ''}
          </span>
        {:else}
          {#each detail.keys as k}<span class="chg mod mono">~ {k}</span>{/each}
        {/each}
      </div>
      {#if detail.status === 'pending'}
        <div class="decide">
          <input class="input" placeholder="Rejection note…" bind:value={rejectNote} />
          <button class="btn" onclick={() => reject(detail!)}>Reject</button>
          <button class="btn btn-stamp" onclick={() => approve(detail!)}>Approve &amp; apply</button>
        </div>
      {/if}
    </section>
  {/if}
</div>

<style>
  .page-n { max-width: 1200px; margin: 0 auto; }
  .page-head { display: flex; justify-content: space-between; align-items: flex-end; gap: var(--s4); flex-wrap: wrap; }
  .page-head h1 { margin-top: var(--s1); }
  .head-actions { display: flex; align-items: center; gap: var(--s3); flex-wrap: wrap; }
  .head-actions .select { max-width: 200px; }
  .error { color: var(--vermilion); font-size: var(--text-sm); margin-top: var(--s3); }

  .seg { display: flex; border: 1px solid var(--rule-strong); border-radius: var(--radius); overflow: hidden; }
  .seg-btn {
    font-family: var(--font-ui);
    font-size: var(--text-xs);
    font-weight: 650;
    text-transform: uppercase;
    letter-spacing: 0.1em;
    padding: 0.4rem 0.9rem;
    background: var(--paper-high);
    border: 0;
    border-right: 1px solid var(--rule);
    cursor: pointer;
    color: var(--ink-faint);
  }
  .seg-btn:last-child { border-right: 0; }
  .seg-btn.on { background: var(--ink); color: var(--paper-high); }

  .table-wrap { overflow-x: auto; margin-top: var(--s5); }
  .r-note { display: block; font-weight: 620; }
  .small { font-size: var(--text-xs); color: var(--ink-soft); }
  .state { font-size: var(--text-xs); font-weight: 700; text-transform: uppercase; letter-spacing: 0.08em; }
  .state.ok { color: var(--verdigris); }
  .state.warn { color: var(--ochre); }
  .state.bad { color: var(--vermilion); }
  .row-actions { text-align: right; white-space: nowrap; }
  .ok-btn:hover { color: var(--verdigris); }
  .del-btn:hover { color: var(--vermilion); }
  .empty { text-align: center; padding: var(--s6) !important; }

  .review { margin-top: var(--s5); padding: var(--s4) var(--s5); border-left: 4px solid var(--archivist); }
  .section-head { display: flex; justify-content: space-between; align-items: baseline; margin-bottom: var(--s3); }
  .chips { display: flex; flex-wrap: wrap; gap: 0.35rem; }
  .chg { font-size: var(--text-xs); padding: 0.06rem 0.4rem; border-radius: 2px; border: 1px solid; color: var(--ink-faint); }
  .chg.add { color: var(--verdigris); background: var(--verdigris-wash); }
  .chg.mod { color: var(--archivist); background: var(--archivist-wash); }
  .chg.del { color: var(--vermilion); background: var(--vermilion-wash); }
  .decide { display: flex; gap: var(--s3); margin-top: var(--s4); }
  .decide .input { max-width: 320px; }
</style>
