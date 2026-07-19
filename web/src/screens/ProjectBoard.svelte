<script lang="ts">
  import { registry } from '../lib/registry.svelte'
  import { api, errorMessage } from '../lib/api'
  import { relTime, stampDate } from '../lib/util'
  import { router } from '../lib/router.svelte'
  import { dialog } from '../lib/dialog.svelte'
  import PromotePanel from '../components/PromotePanel.svelte'
  import type { ViewEnv, ViewConfig } from '../lib/registry.svelte'
  import NotFound from './NotFound.svelte'

  let { projectId }: { projectId: string } = $props()
  const project = $derived(registry.findProject(projectId))

  /* drag-to-promote */
  let dragging = $state<{ configId: string; envId: string } | null>(null)
  let dropEnvId = $state<string | null>(null)
  let staged = $state<{ config: ViewConfig; env: ViewEnv; targetEnv: ViewEnv } | null>(null)
  let note = $state('')

  function dragStart(e: DragEvent, cfg: ViewConfig, env: ViewEnv) {
    dragging = { configId: cfg.id, envId: env.id }
    e.dataTransfer?.setData('text/janus-config', cfg.id)
    if (e.dataTransfer) e.dataTransfer.effectAllowed = 'copy'
  }

  function dragEnd() {
    dragging = null
    dropEnvId = null
  }

  function dragOver(e: DragEvent, env: ViewEnv) {
    if (!dragging || dragging.envId === env.id) return
    e.preventDefault()
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy'
    dropEnvId = env.id
  }

  function drop(e: DragEvent, targetEnv: ViewEnv) {
    e.preventDefault()
    if (!project || !dragging || dragging.envId === targetEnv.id) return
    const srcEnv = project.environments.find(x => x.id === dragging!.envId)
    const cfg = srcEnv?.configs.find(c => c.id === dragging!.configId)
    // Every drop stages the review panel — the promoted keys must be visible
    // and selectable before anything is applied, whatever the target env.
    if (srcEnv && cfg) staged = { config: cfg, env: srcEnv, targetEnv }
    dragging = null
    dropEnvId = null
  }

  function promoted(msg: string) {
    staged = null
    note = msg
    setTimeout(() => (note = ''), 3500)
  }

  let addingEnv = $state(false)
  let envSlug = $state('')
  let addingCfgFor = $state<string | null>(null)
  let cfgName = $state('')
  let error = $state('')
  let pipeline = $state<string[]>([])
  let showPipeline = $state(false)
  let renaming = $state(false)
  let newName = $state('')

  $effect(() => {
    if (project) {
      const envIds = project.environments.map(e => e.id)
      api.getPipeline(project.id)
        .then(p => {
          // The stored pipeline is an explicit list — merge in any environments
          // created after it was saved (appended at the end) and drop deleted ones.
          const kept = p.environment_ids.filter(id => envIds.includes(id))
          const missing = envIds.filter(id => !kept.includes(id))
          pipeline = [...kept, ...missing]
        })
        .catch(() => (pipeline = envIds))
    }
  })

  const orderedEnvs = $derived.by(() => {
    if (!project) return []
    const byId = new Map(project.environments.map(e => [e.id, e]))
    const inPipe = pipeline.map(id => byId.get(id)).filter(Boolean) as typeof project.environments
    const rest = project.environments.filter(e => !pipeline.includes(e.id))
    return [...inPipe, ...rest]
  })

  function movePipe(i: number, dir: -1 | 1) {
    const j = i + dir
    if (j < 0 || j >= pipeline.length) return
    const next = pipeline.slice()
    ;[next[i], next[j]] = [next[j], next[i]]
    pipeline = next
  }

  async function savePipeline() {
    if (!project) return
    try {
      const res = await api.setPipeline(project.id, pipeline)
      pipeline = res.environment_ids
      showPipeline = false
    } catch (err) {
      error = errorMessage(err, 'Could not save the pipeline.')
    }
  }

  async function renameProject(e: SubmitEvent) {
    e.preventDefault()
    if (!project || !newName.trim()) return
    try {
      await api.renameProject(project.id, newName.trim())
      renaming = false
      await registry.hydrate(true)
    } catch (err) {
      error = errorMessage(err, 'Rename failed.')
    }
  }

  async function deleteProject() {
    if (!project) return
    const ok = await dialog.confirm({
      title: `Move ${project.name} to the trash?`,
      body: 'All its environments and configs go with it. Restorable from Trash until destroyed.',
      confirmLabel: 'Move to trash',
      danger: true,
    })
    if (!ok) return
    try {
      await api.deleteProject(project.id)
      await registry.hydrate(true)
      router.go('/projects')
    } catch (err) {
      error = errorMessage(err, 'Delete failed.')
    }
  }

  async function renameEnv(env: ViewEnv) {
    if (!project) return
    const name = await dialog.prompt({
      title: `Rename environment ${env.slug}`,
      body: 'Changes the display name; the slug is the immutable identifier used by the CLI and references.',
      label: 'Display name',
      initial: env.name,
      confirmLabel: 'Rename',
    })
    if (!name?.trim() || name.trim() === env.name) return
    try {
      await api.renameEnvironment(project.id, env.id, name.trim())
      await registry.hydrate(true)
    } catch (err) {
      error = errorMessage(err, 'Rename failed.')
    }
  }

  async function cloneEnv(envId: string, slug: string) {
    const name = await dialog.prompt({
      title: `Clone environment ${slug}`,
      body: 'Copies the environment with its configs and secrets under a new slug.',
      label: 'New slug',
      placeholder: `${slug}-copy`,
      initial: `${slug}-copy`,
      confirmLabel: 'Clone',
    })
    if (!project || !name?.trim()) return
    try {
      await api.cloneEnvironment(project.id, envId, name.trim(), name.trim())
      await registry.hydrate(true)
    } catch (err) {
      error = errorMessage(err, 'Clone failed.')
    }
  }

  async function deleteEnv(envId: string, slug: string) {
    if (!project) return
    const ok = await dialog.confirm({
      title: `Move ${slug} to the trash?`,
      body: 'Its configs go with it. Restorable from Trash until destroyed.',
      confirmLabel: 'Move to trash',
      danger: true,
    })
    if (!ok) return
    try {
      await api.deleteEnvironment(project.id, envId)
      await registry.hydrate(true)
    } catch (err) {
      error = errorMessage(err, 'Delete failed.')
    }
  }

  async function addEnv(e: SubmitEvent) {
    e.preventDefault()
    if (!project) return
    error = ''
    try {
      await api.createEnvironment(project.id, envSlug.trim(), envSlug.trim())
      addingEnv = false
      envSlug = ''
      await registry.hydrate(true)
    } catch (err) {
      error = errorMessage(err, 'Could not create the environment.')
    }
  }

  async function addConfig(e: SubmitEvent, envId: string, root?: string) {
    e.preventDefault()
    if (!project) return
    error = ''
    try {
      await api.createConfig(project.id, envId, cfgName.trim(), root)
      addingCfgFor = null
      cfgName = ''
      await registry.hydrate(true)
    } catch (err) {
      error = errorMessage(err, 'Could not create the config.')
    }
  }
</script>

{#if !project}
  {#if registry.loading}
    <p class="folio">Opening the dossier…</p>
  {:else}
    <NotFound />
  {/if}
{:else}
  <div class="board">
    <header class="page-head rise">
      <div>
        <p class="folio"><a href="/projects">Registry</a> / dossier {project.slug.toUpperCase()} · est. {stampDate(project.createdAt)}</p>
        {#if renaming}
          <form class="rename" onsubmit={renameProject}>
            <input class="input" bind:value={newName} />
            <button class="btn btn-sm btn-stamp" type="submit">Save</button>
            <button class="btn btn-sm btn-ghost" type="button" onclick={() => (renaming = false)}>Cancel</button>
          </form>
        {:else}
          <h1>
            {project.name}
            <button class="btn btn-ghost btn-sm rename-btn" onclick={() => { renaming = true; newName = project.name }}>Rename</button>
          </h1>
        {/if}
      </div>
      <div class="head-actions">
        <button class="btn" onclick={() => (showPipeline = !showPipeline)}>Pipeline</button>
        <a class="btn" href={`/audit?q=${encodeURIComponent(project.name)}`}>Audit</a>
        <a class="btn" href="/approvals">Approvals</a>
        <button class="btn btn-primary" onclick={() => (addingEnv = !addingEnv)}>+ Environment</button>
        <button class="btn btn-ghost del-btn" onclick={deleteProject}>Delete</button>
      </div>
    </header>
    <hr class="ledger-rule" />

    {#if error}<p class="error rise">{error}</p>{/if}
    {#if note}<p class="promote-note rise"><span class="pill pill-info">{note}</span></p>{/if}

    {#if staged}
      <div class="staged-wrap">
        <div class="staged-head">
          <span class="folio">Dropped {staged.env.slug}/{staged.config.name} on {staged.targetEnv.slug} — review, then promote</span>
          <button class="btn btn-ghost btn-sm" onclick={() => (staged = null)}>Dismiss</button>
        </div>
        <PromotePanel
          {project}
          env={staged.env}
          config={staged.config}
          targetEnvId={staged.targetEnv.id}
          onDone={promoted}
        />
      </div>
    {/if}

    {#if showPipeline}
      <div class="sheet pipeline rise">
        <span class="label">Promotion pipeline</span>
        <div class="pipe-list">
          {#each pipeline as eid, i (eid)}
            {@const e = project.environments.find(x => x.id === eid)}
            <div class="pipe-item">
              <span class="pill pill-{e?.kind ?? 'dev'}">{e?.slug ?? eid.slice(0, 8)}</span>
              <span class="pipe-ctrl">
                <button class="btn btn-ghost btn-sm" onclick={() => movePipe(i, -1)} disabled={i === 0}>↑</button>
                <button class="btn btn-ghost btn-sm" onclick={() => movePipe(i, 1)} disabled={i === pipeline.length - 1}>↓</button>
              </span>
              {#if i < pipeline.length - 1}<span class="pipe-arrow" aria-hidden="true">→</span>{/if}
            </div>
          {/each}
        </div>
        <span class="folio">promotions flow left → right; the editor's Promote targets the next stage</span>
        <button class="btn btn-stamp btn-sm" onclick={savePipeline}>Save pipeline</button>
      </div>
    {/if}

    {#if addingEnv}
      <form class="sheet inline-form rise" onsubmit={addEnv}>
        <label class="label" for="env-slug">Environment slug</label>
        <input id="env-slug" class="input mono" bind:value={envSlug} placeholder="dev / staging / prod" required />
        <button class="btn btn-stamp" type="submit" disabled={!envSlug.trim()}>Create</button>
      </form>
    {/if}

    <div class="env-columns" style={`--cols: ${Math.max(project.environments.length, 1)}`}>
      {#each orderedEnvs as env, i (env.id)}
        <section
          class="env-col rise"
          class:drop-ok={dragging !== null && dragging.envId !== env.id}
          class:drop-hover={dropEnvId === env.id}
          style={`animation-delay: ${i * 70}ms`}
          ondragover={(e) => dragOver(e, env)}
          ondragleave={() => { if (dropEnvId === env.id) dropEnvId = null }}
          ondrop={(e) => drop(e, env)}
        >
          <header class="env-head env-{env.kind}">
            <span class="env-title">
              <span class="pill pill-{env.kind}">{env.slug}</span>
              {#if env.name && env.name !== env.slug}<span class="env-name folio">“{env.name}”</span>{/if}
            </span>
            <span class="env-tools">
              <span class="folio">{env.configs.reduce((a, c) => a + c.reads24h, 0).toLocaleString()} reads/24h</span>
              <button class="btn btn-ghost btn-sm" title="Rename display name" onclick={() => renameEnv(env)}>Rename</button>
              <button class="btn btn-ghost btn-sm" title="Clone environment" onclick={() => cloneEnv(env.id, env.slug)}>Clone</button>
              <button class="btn btn-ghost btn-sm del-btn" title="Move to trash" onclick={() => deleteEnv(env.id, env.slug)}>✕</button>
            </span>
          </header>

          {#each env.configs as cfg (cfg.id)}
            <a
              class="cfg-card sheet"
              class:branch={!!cfg.inheritsFrom}
              class:lifting={dragging?.configId === cfg.id}
              href={`/projects/${project.id}/configs/${cfg.id}`}
              draggable="true"
              ondragstart={(e) => dragStart(e, cfg, env)}
              ondragend={dragEnd}
              title="Open — or drag onto another environment to promote"
            >
              {#if cfg.inheritsFrom}
                <span class="branch-line" aria-hidden="true"></span>
              {/if}
              <div class="cfg-title">
                <span class="grip" aria-hidden="true">⠿</span>
                <span class="cfg-name mono">{cfg.name}</span>
                <span class="cfg-reads mono">{cfg.reads24h.toLocaleString()}·24h</span>
              </div>
              <div class="cfg-meta folio">
                est. {stampDate(cfg.createdAt)}
                {#if cfg.inheritsFrom}
                  <span class="inherit">⤷ inherits {env.configs.find(c => c.id === cfg.inheritsFrom)?.name ?? 'base'}</span>
                {/if}
              </div>
            </a>
          {/each}

          {#if addingCfgFor === env.id}
            <form class="cfg-form" onsubmit={(e) => addConfig(e, env.id)}>
              <input class="input mono" bind:value={cfgName} placeholder="config name" required />
              <button class="btn btn-sm btn-stamp" type="submit" disabled={!cfgName.trim()}>Add</button>
            </form>
          {:else}
            <button class="add-cfg folio" onclick={() => { addingCfgFor = env.id; cfgName = '' }}>+ config</button>
          {/if}
        </section>
      {:else}
        <p class="folio">No environments yet — create dev, staging, and prod to begin.</p>
      {/each}
    </div>
  </div>
{/if}

<style>
  .board { max-width: 1200px; margin: 0 auto; }
  .page-head { display: flex; justify-content: space-between; align-items: flex-end; gap: var(--s4); }
  .page-head h1 { margin: var(--s1) 0; }
  .head-actions { display: flex; gap: var(--s3); }
  .error { color: var(--vermilion); font-size: var(--text-sm); margin-top: var(--s3); }

  .inline-form {
    display: flex;
    align-items: center;
    gap: var(--s3);
    padding: var(--s3) var(--s4);
    margin-top: var(--s4);
    border-left: 4px solid var(--vermilion);
  }
  .inline-form .input { max-width: 220px; }

  .rename { display: flex; gap: var(--s2); align-items: center; margin-top: var(--s2); }
  .rename .input { max-width: 260px; }
  .rename-btn { font-size: 0.6rem; vertical-align: middle; }
  .del-btn:hover { color: var(--vermilion); }

  .pipeline {
    display: flex;
    align-items: center;
    gap: var(--s4);
    flex-wrap: wrap;
    padding: var(--s3) var(--s4);
    margin-top: var(--s4);
    border-left: 4px solid var(--archivist);
  }
  .pipe-list { display: flex; align-items: center; gap: var(--s2); flex-wrap: wrap; }
  .pipe-item { display: flex; align-items: center; gap: var(--s1); }
  .pipe-ctrl { display: inline-flex; }
  .pipe-arrow { color: var(--ink-faint); margin: 0 var(--s1); }

  .env-tools { display: flex; align-items: center; gap: var(--s1); }
  .env-title { display: flex; align-items: baseline; gap: var(--s2); min-width: 0; }
  .env-name { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }

  /* ── drag-to-promote ────────────────────────── */
  .env-col { border-radius: var(--radius-plate); transition: background var(--t-fast), outline-color var(--t-fast); outline: 2px dashed transparent; outline-offset: 4px; }
  .env-col.drop-ok { outline-color: var(--rule); }
  .env-col.drop-hover { outline-color: var(--archivist); background: var(--archivist-wash); }
  .cfg-card { cursor: grab; }
  .cfg-card.lifting { opacity: 0.45; transform: rotate(-1deg); }
  .grip { color: var(--ink-ghost); font-size: 0.7rem; margin-right: 0.15rem; cursor: grab; }

  .promote-note { margin-top: var(--s3); }
  .staged-head {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: var(--s3);
    margin-top: var(--s4);
  }

  .env-columns {
    display: grid;
    grid-template-columns: repeat(var(--cols, 3), minmax(0, 1fr));
    gap: var(--s5);
    margin-top: var(--s5);
    align-items: start;
  }

  .env-head {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding-bottom: var(--s2);
    margin-bottom: var(--s3);
    border-bottom: 2px solid;
  }
  .env-head.env-dev { border-color: var(--env-dev); }
  .env-head.env-staging { border-color: var(--env-staging); }
  .env-head.env-prod { border-color: var(--env-prod); }

  .cfg-card {
    display: block;
    position: relative;
    color: var(--ink);
    padding: var(--s3) var(--s4);
    margin-bottom: var(--s3);
    transition: box-shadow var(--t-med) var(--ease-out), transform var(--t-med) var(--ease-out);
  }
  .cfg-card:hover { text-decoration: none; box-shadow: var(--shadow-hover); transform: translateY(-2px); }
  .cfg-card.branch { margin-left: var(--s5); }
  .branch-line {
    position: absolute;
    left: calc(-1 * var(--s4));
    top: -12px;
    width: 12px;
    height: 34px;
    border-left: 1.5px solid var(--rule-strong);
    border-bottom: 1.5px solid var(--rule-strong);
    border-bottom-left-radius: 6px;
  }

  .cfg-title { display: flex; justify-content: space-between; align-items: baseline; gap: var(--s2); }
  .cfg-name { font-weight: 600; font-size: var(--text-base); }
  .cfg-reads { font-size: var(--text-xs); color: var(--ink-faint); }
  .cfg-meta { margin-top: var(--s1); }
  .inherit { display: block; color: var(--archivist); }

  .cfg-form { display: flex; gap: var(--s2); }
  .add-cfg {
    width: 100%;
    background: transparent;
    border: 1.5px dashed var(--rule);
    border-radius: var(--radius);
    padding: var(--s2);
    cursor: pointer;
    transition: all var(--t-fast);
    color: var(--ink-faint);
  }
  .add-cfg:hover { border-color: var(--archivist); color: var(--archivist); }

  @media (max-width: 980px) {
    .env-columns { grid-template-columns: 1fr; }
  }
</style>
