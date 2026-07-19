<script lang="ts">
  import { api, errorMessage, type PromoteDiff, type PromoteSelection } from '../lib/api'
  import { registry, type ViewProject, type ViewEnv, type ViewConfig } from '../lib/registry.svelte'

  let {
    project,
    env,
    config,
    targetEnvId,
    onDone,
  }: {
    project: ViewProject
    env: ViewEnv
    config: ViewConfig
    /** Pin the promotion to one env (drag-and-drop) instead of offering pipeline stages. */
    targetEnvId?: string
    onDone: (msg: string) => void
  } = $props()

  interface Target { label: string; kind: 'config' | 'env'; envId: string; configId?: string }

  let pipeline = $state<string[]>([])
  let target = $state<Target | null>(null)
  let diff = $state<PromoteDiff | null>(null)
  let picked = $state<Record<string, boolean>>({})
  let note = $state('')
  let mode = $state<'apply' | 'request'>('apply')
  let busy = $state(false)
  let error = $state('')

  function envTarget(eid: string): Target | null {
    const e = project.environments.find(x => x.id === eid)
    if (!e) return null
    const root = e.configs.find(c => !c.inheritsFrom)
    if (root) return { label: `${e.slug} / ${root.name}`, kind: 'config', envId: e.id, configId: root.id }
    return { label: `${e.slug} / ${config.name} (create)`, kind: 'env', envId: e.id }
  }

  const targets = $derived.by((): Target[] => {
    // pinned target (drag-and-drop) beats pipeline-derived stages
    if (targetEnvId) {
      const t = envTarget(targetEnvId)
      return t ? [t] : []
    }
    // stored pipeline + any environments created after it was saved
    const envIds = project.environments.map(e => e.id)
    const kept = pipeline.filter(id => envIds.includes(id))
    const order = [...kept, ...envIds.filter(id => !kept.includes(id))]
    const idx = order.indexOf(env.id)
    const nextIds = idx >= 0 ? order.slice(idx + 1) : order.filter(id => id !== env.id)
    return nextIds.map(envTarget).filter((t): t is Target => t !== null)
  })

  $effect(() => {
    api.getPipeline(project.id).then(p => (pipeline = p.environment_ids)).catch(() => (pipeline = []))
  })

  $effect(() => {
    if (!target && targets.length) target = targets[0]
  })

  $effect(() => {
    if (!target) return
    void loadPreview(target)
  })

  async function loadPreview(t: Target) {
    error = ''
    diff = null
    try {
      diff = await api.promotePreview(config.id, t.kind === 'config' ? { to: t.configId } : { to_env: t.envId })
      const sel: Record<string, boolean> = {}
      for (const e of diff.entries) sel[e.key] = !e.locked && e.status !== 'same'
      picked = sel
    } catch (err) {
      error = errorMessage(err, 'Could not compute the promotion diff.')
    }
  }

  const selections = $derived.by((): PromoteSelection[] =>
    (diff?.entries ?? [])
      .filter(e => picked[e.key] && e.status !== 'same')
      .map(e => ({ key: e.key, action: e.status === 'remove' ? 'remove' as const : 'set' as const })),
  )

  async function go() {
    if (!target || !diff) return
    busy = true
    error = ''
    const body = {
      from_config: config.id,
      ...(target.kind === 'config'
        ? { to_config: target.configId }
        : { to_env: target.envId, to_name: config.name, create: true }),
      source_version: diff.source_version,
      selections,
    }
    try {
      if (mode === 'apply') {
        const res = await api.promoteApply(body)
        onDone(`Promoted ${res.applied.length} key${res.applied.length === 1 ? '' : 's'} → ${target.label} v${res.target_version}`)
      } else {
        await api.createPromoteRequest({ ...body, note })
        onDone('Promotion request filed — awaiting approval.')
      }
      await registry.hydrate(true)
    } catch (err) {
      error = errorMessage(err, mode === 'apply' ? 'Promotion failed.' : 'Could not file the request.')
    } finally {
      busy = false
    }
  }

  const short = (v: string) => (v.length > 26 ? v.slice(0, 26) + '…' : v)
</script>

<section class="sheet promote rise">
  <div class="head">
    <h3>Promote {env.slug} / {config.name}</h3>
    {#if targets.length > 1}
      <select class="select" onchange={(e) => (target = targets[Number((e.currentTarget as HTMLSelectElement).value)])}>
        {#each targets as t, i}
          <option value={i} selected={t === target}>→ {t.label}</option>
        {/each}
      </select>
    {:else if target}
      <span class="pill pill-info">→ {target.label}</span>
    {/if}
  </div>

  {#if error}<p class="error">{error}</p>{/if}

  {#if !targets.length}
    <p class="folio">No downstream environment in the pipeline to promote into.</p>
  {:else if !diff}
    <p class="folio">Computing diff…</p>
  {:else}
    <table class="ledger">
      <thead>
        <tr><th style="width: 36px"></th><th>Key</th><th style="width: 90px">Change</th><th>Source</th><th>Target</th></tr>
      </thead>
      <tbody>
        {#each diff.entries as e (e.key)}
          <tr class:muted={e.status === 'same'} class:locked-row={e.locked}>
            <td>
              <input type="checkbox" checked={picked[e.key] ?? false} disabled={e.locked || e.status === 'same'}
                onchange={(ev) => (picked[e.key] = (ev.currentTarget as HTMLInputElement).checked)} />
            </td>
            <td class="mono key">
              {e.key}
              {#if e.locked}<span class="lock" title="Locked in target — cannot be promoted over">⚿ locked</span>{/if}
            </td>
            <td>
              {#if e.status === 'add'}<span class="chg add mono">+ add</span>
              {:else if e.status === 'change'}<span class="chg mod mono">~ change</span>
              {:else if e.status === 'remove'}<span class="chg del mono">− remove</span>
              {:else}<span class="folio">same</span>{/if}
            </td>
            <td class="mono val">{short(e.source_value)}</td>
            <td class="mono val">{e.target_value ? short(e.target_value) : '—'}</td>
          </tr>
        {/each}
      </tbody>
    </table>

    <div class="foot">
      <div class="mode-seg" role="group">
        <button class="seg-btn" class:on={mode === 'apply'} onclick={() => (mode = 'apply')}>Apply now</button>
        <button class="seg-btn" class:on={mode === 'request'} onclick={() => (mode = 'request')}>Request approval</button>
      </div>
      {#if mode === 'request'}
        <input class="input note" placeholder="Note for the approver…" bind:value={note} />
      {/if}
      <button class="btn btn-stamp" onclick={go} disabled={busy || !selections.length || (mode === 'request' && !note.trim())}>
        {busy ? 'Working…' : mode === 'apply' ? `Promote ${selections.length}` : 'File request'}
      </button>
    </div>
  {/if}
</section>

<style>
  .promote { padding: var(--s4) var(--s5); margin-top: var(--s4); border-left: 4px solid var(--archivist); }
  .head { display: flex; justify-content: space-between; align-items: center; gap: var(--s3); flex-wrap: wrap; margin-bottom: var(--s3); }
  .head .select { max-width: 280px; }
  .error { color: var(--vermilion); font-size: var(--text-sm); margin-bottom: var(--s2); }

  tr.muted td { opacity: 0.45; }
  tr.locked-row td { background: var(--vermilion-wash); }
  .key { font-weight: 600; font-size: var(--text-sm); }
  .lock { margin-left: var(--s2); font-size: 0.62rem; color: var(--vermilion); text-transform: uppercase; letter-spacing: 0.08em; }
  .val { font-size: var(--text-xs); color: var(--ink-soft); max-width: 220px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .chg { font-size: var(--text-xs); padding: 0.06rem 0.4rem; border-radius: 2px; border: 1px solid; }
  .chg.add { color: var(--verdigris); background: var(--verdigris-wash); }
  .chg.mod { color: var(--archivist); background: var(--archivist-wash); }
  .chg.del { color: var(--vermilion); background: var(--vermilion-wash); }

  .foot { display: flex; align-items: center; gap: var(--s3); margin-top: var(--s4); flex-wrap: wrap; }
  .note { flex: 1; min-width: 200px; }
  .mode-seg { display: flex; border: 1px solid var(--rule-strong); border-radius: var(--radius); overflow: hidden; }
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
</style>
