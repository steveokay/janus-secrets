<script lang="ts">
  import { api, errorMessage, type CompareEntry } from '../lib/api'
  import { registry } from '../lib/registry.svelte'

  // A flat catalog of selectable configs, each carrying its env kind for accent
  // coding and a human label. Built from the registry tree.
  interface ConfigOpt {
    id: string
    label: string
    kind: 'dev' | 'staging' | 'prod'
    envSlug: string
  }

  const options = $derived.by((): ConfigOpt[] => {
    const out: ConfigOpt[] = []
    for (const p of registry.projects)
      for (const e of p.environments)
        for (const c of e.configs)
          out.push({
            id: c.id,
            label: `${p.name} / ${e.slug} / ${c.name}`,
            kind: e.kind,
            envSlug: e.slug,
          })
    out.sort((a, b) => a.label.localeCompare(b.label))
    return out
  })

  let aId = $state('')
  let bId = $state('')
  let entries = $state<CompareEntry[] | null>(null)
  let loading = $state(false)
  let error = $state('')

  const aOpt = $derived(options.find(o => o.id === aId) ?? null)
  const bOpt = $derived(options.find(o => o.id === bId) ?? null)
  const canCompare = $derived(!!aId && !!bId && aId !== bId)

  // Read ?a= / ?b= deep-link params on mount (if present, kick a compare).
  $effect(() => {
    const q = new URLSearchParams(location.search)
    const a = q.get('a')
    const b = q.get('b')
    if (a && !aId) aId = a
    if (b && !bId) bId = b
  })

  function statusOf(e: CompareEntry): { label: string; cls: string } {
    if (e.in_a && e.in_b) return e.differs
      ? { label: 'differs', cls: 'pill-prod' }
      : { label: 'same', cls: 'pill-neutral' }
    if (e.in_a) return { label: 'only A', cls: 'pill-info' }
    return { label: 'only B', cls: 'pill-staging' }
  }

  function pillClass(kind: 'dev' | 'staging' | 'prod'): string {
    return `pill-${kind}`
  }

  async function run() {
    if (!canCompare) return
    loading = true
    error = ''
    entries = null
    try {
      const res = await api.compareConfigs(aId, bId)
      entries = res.entries.slice().sort((x, y) => x.key.localeCompare(y.key))
    } catch (err) {
      error = errorMessage(err, 'Could not compare these configs. You need read access to both.')
    } finally {
      loading = false
    }
  }

  const summary = $derived.by(() => {
    if (!entries) return null
    let same = 0, differs = 0, onlyA = 0, onlyB = 0
    for (const e of entries) {
      if (e.in_a && e.in_b) e.differs ? differs++ : same++
      else if (e.in_a) onlyA++
      else onlyB++
    }
    return { same, differs, onlyA, onlyB, total: entries.length }
  })
</script>

<div class="page-n">
  <header class="page-head rise">
    <div>
      <p class="folio">Registry · key-level comparison — values stay masked</p>
      <h1>Cross-env diff</h1>
    </div>
  </header>
  <hr class="ledger-rule" />

  <div class="sheet picker-card rise">
    <div class="pickers">
      <label class="picker">
        <span class="folio">Config A</span>
        <select class="field-ruled" bind:value={aId}>
          <option value="">Select a config…</option>
          {#each options as o (o.id)}
            <option value={o.id}>{o.label}</option>
          {/each}
        </select>
        {#if aOpt}<span class="pill {pillClass(aOpt.kind)}">{aOpt.envSlug}</span>{/if}
      </label>

      <span class="vs folio" aria-hidden="true">vs</span>

      <label class="picker">
        <span class="folio">Config B</span>
        <select class="field-ruled" bind:value={bId}>
          <option value="">Select a config…</option>
          {#each options as o (o.id)}
            <option value={o.id}>{o.label}</option>
          {/each}
        </select>
        {#if bOpt}<span class="pill {pillClass(bOpt.kind)}">{bOpt.envSlug}</span>{/if}
      </label>

      <button class="btn btn-primary" disabled={!canCompare || loading} onclick={run}>
        {loading ? 'Comparing…' : 'Compare'}
      </button>
    </div>
    {#if aId && bId && aId === bId}
      <p class="folio hint">Pick two different configs to compare.</p>
    {:else}
      <p class="folio hint">Only key names, presence, and per-side origin are compared. Secret values never leave the server.</p>
    {/if}
  </div>

  {#if error}<p class="error rise">{error}</p>{/if}

  {#if summary}
    <div class="summary rise" style="animation-delay: 40ms">
      <span class="pill pill-prod">{summary.differs} differ</span>
      <span class="pill pill-neutral">{summary.same} same</span>
      <span class="pill pill-info">{summary.onlyA} only A</span>
      <span class="pill pill-staging">{summary.onlyB} only B</span>
      <span class="folio">· {summary.total} keys</span>
    </div>
  {/if}

  {#if entries}
    {#if entries.length === 0}
      <div class="sheet empty-card rise">
        <p class="folio">Both configs are empty — no keys to compare.</p>
      </div>
    {:else}
      <div class="sheet table-wrap rise" style="animation-delay: 80ms">
        <table class="ledger">
          <thead>
            <tr>
              <th>Key</th>
              <th style="width: 160px">A · {aOpt?.envSlug ?? 'A'}</th>
              <th style="width: 160px">B · {bOpt?.envSlug ?? 'B'}</th>
              <th style="width: 120px">Status</th>
            </tr>
          </thead>
          <tbody>
            {#each entries as e (e.key)}
              {@const st = statusOf(e)}
              <tr>
                <td class="mono keycell">{e.key}</td>
                <td class="folio">
                  {#if e.in_a}present <span class="origin">· {e.origin_a || 'own'}</span>{:else}<span class="absent">—</span>{/if}
                </td>
                <td class="folio">
                  {#if e.in_b}present <span class="origin">· {e.origin_b || 'own'}</span>{:else}<span class="absent">—</span>{/if}
                </td>
                <td><span class="pill {st.cls}">{st.label}</span></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {/if}
</div>

<style>
  .page-n { max-width: 1000px; margin: 0 auto; }
  .page-head { display: flex; justify-content: space-between; align-items: flex-end; gap: var(--s4); }
  .page-head h1 { margin-top: var(--s1); }
  .table-wrap { overflow-x: auto; }
  .picker-card { padding: var(--s4) var(--s5); margin-top: var(--s5); }
  .pickers {
    display: flex;
    align-items: flex-end;
    gap: var(--s4);
    flex-wrap: wrap;
  }
  .picker {
    display: flex;
    flex-direction: column;
    gap: var(--s2);
    min-width: 240px;
    flex: 1;
  }
  .picker select { width: 100%; }
  .vs {
    padding-bottom: 0.6rem;
    font-style: italic;
  }
  .hint { margin-top: var(--s3); color: var(--ink-faint); }
  .summary {
    display: flex;
    align-items: center;
    gap: var(--s3);
    margin: var(--s5) 0 var(--s4);
    flex-wrap: wrap;
  }
  .keycell { font-size: var(--text-sm); }
  .origin { color: var(--ink-ghost); font-size: var(--text-xs); }
  .absent { color: var(--ink-ghost); }
  .empty-card { padding: var(--s6); text-align: center; }
</style>
