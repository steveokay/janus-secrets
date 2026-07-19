<script lang="ts">
  import { registry } from '../lib/registry.svelte'
  import { api, errorMessage } from '../lib/api'
  import { relTime, stampDate } from '../lib/util'

  let query = $state('')
  let creating = $state(false)
  let newSlug = $state('')
  let newName = $state('')
  let error = $state('')
  let busy = $state(false)

  const filtered = $derived(
    registry.projects.filter(p => (p.name + p.slug).toLowerCase().includes(query.toLowerCase())),
  )

  async function create(e: SubmitEvent) {
    e.preventDefault()
    error = ''
    busy = true
    try {
      await api.createProject(newSlug.trim(), newName.trim() || newSlug.trim())
      creating = false
      newSlug = ''
      newName = ''
      await registry.hydrate(true)
    } catch (err) {
      error = errorMessage(err, 'Could not create the project.')
    } finally {
      busy = false
    }
  }
</script>

<div class="page-narrow">
  <header class="page-head rise">
    <div>
      <p class="folio">Registry · {registry.projects.length} dossier{registry.projects.length === 1 ? '' : 's'}</p>
      <h1>Projects</h1>
    </div>
    <div class="head-actions">
      <input class="input search" placeholder="Filter the registry…" bind:value={query} />
      <button class="btn btn-primary" onclick={() => (creating = !creating)}>+ New project</button>
    </div>
  </header>
  <hr class="ledger-rule" />

  {#if creating}
    <form class="sheet create rise" onsubmit={create}>
      <div class="field">
        <label class="label" for="np-slug">Slug</label>
        <input id="np-slug" class="input mono" bind:value={newSlug} placeholder="atlas-api" required
          pattern="[a-z0-9][a-z0-9-]*" title="lowercase letters, digits, hyphens" />
      </div>
      <div class="field grow">
        <label class="label" for="np-name">Name</label>
        <input id="np-name" class="input" bind:value={newName} placeholder="Atlas API" />
      </div>
      <button class="btn btn-stamp" type="submit" disabled={busy || !newSlug.trim()}>Open dossier</button>
      {#if error}<p class="error">{error}</p>{/if}
    </form>
  {/if}

  {#if registry.loading && !registry.projects.length}
    <p class="folio" style="margin-top: var(--s5)">Opening the registry…</p>
  {/if}

  <div class="dossiers">
    {#each filtered as p, i (p.id)}
      <a class="dossier sheet rise" style={`animation-delay: ${i * 60}ms`} href={`/projects/${p.id}`}>
        <div class="dossier-tab mono">{p.slug.slice(0, 3).toUpperCase()}</div>
        <div class="dossier-body">
          <div class="dossier-title">
            <h3>{p.name}</h3>
            <span class="folio">est. {stampDate(p.createdAt)}</span>
          </div>
          <div class="env-row">
            {#each p.environments as env}
              <div class="env-cell">
                <span class="pill pill-{env.kind}">{env.slug}</span>
                <span class="folio">
                  {env.configs.length} config{env.configs.length === 1 ? '' : 's'}
                </span>
              </div>
            {:else}
              <span class="folio">no environments yet</span>
            {/each}
          </div>
        </div>
        <div class="dossier-reads">
          <span class="reads-num">{p.environments.flatMap(e => e.configs).reduce((a, c) => a + c.reads24h, 0).toLocaleString()}</span>
          <span class="label">reads · 24 h</span>
        </div>
      </a>
    {:else}
      {#if !registry.loading}
        <p class="empty folio">
          {query ? `No dossier matches “${query}”.` : 'The registry is empty — open the first dossier.'}
        </p>
      {/if}
    {/each}
  </div>
</div>

<style>
  .page-narrow { max-width: 1000px; margin: 0 auto; }
  .page-head {
    display: flex;
    justify-content: space-between;
    align-items: flex-end;
    gap: var(--s4);
  }
  .page-head h1 { margin-top: var(--s1); }
  .head-actions { display: flex; gap: var(--s3); align-items: center; }
  .search { width: 240px; }

  .create {
    display: flex;
    align-items: flex-end;
    gap: var(--s4);
    padding: var(--s4) var(--s5);
    margin-top: var(--s4);
    border-left: 4px solid var(--vermilion);
    flex-wrap: wrap;
  }
  .field { display: flex; flex-direction: column; gap: var(--s2); }
  .field.grow { flex: 1; min-width: 180px; }
  .error { color: var(--vermilion); font-size: var(--text-sm); width: 100%; }

  .dossiers { display: flex; flex-direction: column; gap: var(--s4); margin-top: var(--s5); }

  .dossier {
    display: grid;
    grid-template-columns: 64px 1fr 140px;
    color: var(--ink);
    transition: box-shadow var(--t-med) var(--ease-out), transform var(--t-med) var(--ease-out);
    overflow: hidden;
  }
  .dossier:hover { text-decoration: none; box-shadow: var(--shadow-hover); transform: translateY(-2px); }

  .dossier-tab {
    display: grid;
    place-items: center;
    background: var(--cover-bg);
    color: var(--cover-fg);
    font-size: var(--text-sm);
    letter-spacing: 0.14em;
    writing-mode: vertical-rl;
    text-orientation: mixed;
  }
  .dossier:hover .dossier-tab { background: var(--vermilion); }

  .dossier-body { padding: var(--s4) var(--s5); }
  .dossier-title { display: flex; align-items: baseline; gap: var(--s3); }
  .env-row { display: flex; gap: var(--s5); flex-wrap: wrap; margin-top: var(--s3); }
  .env-cell { display: flex; flex-direction: column; gap: var(--s1); }

  .dossier-reads {
    display: flex;
    flex-direction: column;
    justify-content: center;
    align-items: flex-end;
    padding: var(--s4) var(--s5);
    border-left: 2px dashed var(--rule);
    text-align: right;
  }
  .reads-num {
    font-family: var(--font-display);
    font-size: var(--text-lg);
    font-weight: 600;
    font-variant-numeric: tabular-nums;
  }

  .empty { padding: var(--s6); text-align: center; }
</style>
