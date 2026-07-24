<script lang="ts">
  import { api, errorMessage, type Trash } from '../lib/api'
  import { registry } from '../lib/registry.svelte'
  import { dialog } from '../lib/dialog.svelte'
  import { relTime } from '../lib/util'

  let trash = $state<Trash | null>(null)
  let loading = $state(true)
  let error = $state('')
  let note = $state('')

  $effect(() => {
    void load()
  })

  async function load() {
    loading = true
    error = ''
    try {
      trash = await api.listTrash()
    } catch (err) {
      error = errorMessage(err, 'Could not read the trash.')
    } finally {
      loading = false
    }
  }

  function flash(msg: string) {
    note = msg
    setTimeout(() => (note = ''), 3000)
  }

  async function act(fn: () => Promise<unknown>, ok: string) {
    try {
      await fn()
      flash(ok)
      await load()
      await registry.hydrate(true)
    } catch (err) {
      flash(errorMessage(err, 'Action failed.'))
    }
  }

  function confirmDestroy(label: string): Promise<boolean> {
    return dialog.confirm({
      title: `Permanently destroy ${label}?`,
      body: 'The encrypted data is hard-deleted. This cannot be undone.',
      confirmLabel: 'Destroy',
      danger: true,
    })
  }

  const empty = $derived(
    !!trash && !trash.projects.length && !trash.environments.length && !trash.configs.length,
  )
</script>

<div class="page-n">
  <header class="page-head rise">
    <div>
      <p class="folio">Office · soft-deleted material — restorable until destroyed</p>
      <h1>Trash</h1>
    </div>
    {#if note}<span class="pill pill-info">{note}</span>{/if}
  </header>
  <hr class="ledger-rule" />

  {#if error}<p class="error rise">{error}</p>{/if}

  {#if loading && !trash}
    <p class="folio" style="margin-top: var(--s5)">Reading…</p>
  {:else if empty}
    <div class="sheet empty-card rise">
      <p class="folio">The trash is empty. Deleted projects, environments, and configs land here.</p>
    </div>
  {:else if trash}
    {#if trash.projects.length}
      <section class="op-section rise">
        <div class="section-head"><h3>Projects</h3></div>
        <div class="sheet table-wrap">
          <table class="ledger" aria-label="Deleted projects">
            <tbody>
              {#each trash.projects as p (p.id)}
                <tr>
                  <td><span class="name">{p.name}</span> <span class="folio mono">{p.slug}</span></td>
                  <td class="folio" style="width: 160px">deleted {relTime(p.deleted_at)}</td>
                  <td class="row-actions" style="width: 200px">
                    <button class="btn btn-ghost btn-sm" onclick={() => act(() => api.restoreProject(p.id), `Restored ${p.name}.`)}>Restore</button>
                    <button class="btn btn-ghost btn-sm del-btn"
                      onclick={async () => (await confirmDestroy(`project ${p.name} (cascades to all its secrets)`)) && act(() => api.destroyProject(p.id), `Destroyed ${p.name}.`)}>Destroy</button>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </section>
    {/if}

    {#if trash.environments.length}
      <section class="op-section rise" style="animation-delay: 60ms">
        <div class="section-head"><h3>Environments</h3></div>
        <div class="sheet table-wrap">
          <table class="ledger" aria-label="Deleted environments">
            <tbody>
              {#each trash.environments as e (e.id)}
                <tr>
                  <td><span class="name">{e.project_name} / {e.name}</span></td>
                  <td class="folio" style="width: 160px">deleted {relTime(e.deleted_at)}</td>
                  <td class="row-actions" style="width: 200px">
                    <button class="btn btn-ghost btn-sm" onclick={() => act(() => api.restoreEnvironment(e.project_id, e.id), `Restored ${e.name}.`)}>Restore</button>
                    <button class="btn btn-ghost btn-sm del-btn"
                      onclick={async () => (await confirmDestroy(`environment ${e.project_name}/${e.name}`)) && act(() => api.destroyEnvironment(e.project_id, e.id), `Destroyed ${e.name}.`)}>Destroy</button>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </section>
    {/if}

    {#if trash.configs.length}
      <section class="op-section rise" style="animation-delay: 120ms">
        <div class="section-head"><h3>Configs</h3></div>
        <div class="sheet table-wrap">
          <table class="ledger" aria-label="Deleted configs">
            <tbody>
              {#each trash.configs as c (c.id)}
                <tr>
                  <td><span class="name mono">{c.project_name} / {c.environment_name} / {c.name}</span></td>
                  <td class="folio" style="width: 160px">deleted {relTime(c.deleted_at)}</td>
                  <td class="row-actions" style="width: 200px">
                    <button class="btn btn-ghost btn-sm" onclick={() => act(() => api.restoreConfig(c.id), `Restored ${c.name}.`)}>Restore</button>
                    <button class="btn btn-ghost btn-sm del-btn"
                      onclick={async () => (await confirmDestroy(`config ${c.name}`)) && act(() => api.destroyConfig(c.id), `Destroyed ${c.name}.`)}>Destroy</button>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </section>
    {/if}
  {/if}

  <p class="foot-note folio">
    Restore undeletes in place. Destroy is a permanent, cascading hard delete of the
    encrypted material — Janus never shows plaintext here either way.
  </p>
</div>

<style>
  .page-n { max-width: 1000px; margin: 0 auto; }
  .page-head { display: flex; justify-content: space-between; align-items: flex-end; gap: var(--s4); }
  .page-head h1 { margin-top: var(--s1); }
  .error { color: var(--vermilion); font-size: var(--text-sm); margin-top: var(--s3); }

  .op-section { margin-top: var(--s5); }
  .section-head { margin-bottom: var(--s3); }
  .table-wrap { overflow-x: auto; }
  .name { font-weight: 620; }
  .row-actions { text-align: right; white-space: nowrap; }
  .del-btn:hover { color: var(--vermilion); }
  .empty-card { margin-top: var(--s5); padding: var(--s6); text-align: center; }
  .foot-note { margin-top: var(--s4); max-width: 70ch; }
</style>
