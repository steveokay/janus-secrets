<script lang="ts">
  import { router } from '../lib/router.svelte'
  import { registry } from '../lib/registry.svelte'
  import { theme } from '../lib/theme.svelte'
  import { api, type KeySearchResult } from '../lib/api'
  import { shortcuts } from '../lib/shortcuts.svelte'

  interface Item { id: string; group: string; label: string; sublabel?: string; keywords: string; hint?: boolean; run: () => void }

  let open = $state(false)
  let query = $state('')
  let cursor = $state(0)
  let inputEl = $state<HTMLInputElement | null>(null)

  // Async "Secret keys" group — key NAMES only, never values. Streams in.
  let keyHits = $state<KeySearchResult[]>([])
  let keysTruncated = $state(false)
  let searchSeq = 0
  let debounceTimer: ReturnType<typeof setTimeout> | undefined

  const NAV: Array<[string, string, string]> = [
    ['Go to Overview', '/', 'home dashboard overview'],
    ['Go to Projects', '/projects', 'projects registry dossiers'],
    ['Go to Audit ledger', '/audit', 'audit activity log events record chain'],
    ['Go to Approvals', '/approvals', 'approvals promotion requests review'],
    ['Go to Transit', '/transit', 'transit encrypt sign kms keys'],
    ['Go to Operations', '/operations', 'operations rotation sync dynamic leases'],
    ['Go to Integrations', '/integrations', 'integrations oidc sso federation github kubernetes'],
    ['Go to Service tokens', '/tokens', 'tokens service machine api'],
    ['Go to Members', '/members', 'members users roles rbac team'],
    ['Go to Notifications', '/notifications', 'notifications alerts webhook slack channels'],
    ['Go to Settings', '/settings', 'settings master key backup password'],
    ['Go to Trash', '/trash', 'trash deleted restore bin'],
  ]

  // Debounced global key search. Only ≥2 chars fire a request; stale responses
  // (a slower fetch for an older query) are dropped via searchSeq. Errors show
  // no key results. State clears when the query drops below 2 chars or closes.
  $effect(() => {
    const q = query.trim()
    if (debounceTimer) clearTimeout(debounceTimer)
    if (!open || q.length < 2) {
      searchSeq++            // invalidate any in-flight request
      keyHits = []
      keysTruncated = false
      return
    }
    const seq = ++searchSeq
    debounceTimer = setTimeout(() => {
      api.searchKeys(q)
        .then(r => {
          if (seq !== searchSeq) return   // a newer query superseded this one
          keyHits = r.results
          keysTruncated = r.truncated
        })
        .catch(() => {
          if (seq !== searchSeq) return
          keyHits = []
          keysTruncated = false
        })
    }, 150)
    return () => { if (debounceTimer) clearTimeout(debounceTimer) }
  })

  const items = $derived.by((): Item[] => {
    const out: Item[] = []
    for (const p of registry.projects) {
      out.push({
        id: `p:${p.id}`, group: 'Projects', label: p.name, sublabel: p.slug,
        keywords: `${p.name} ${p.slug}`, run: () => router.go(`/projects/${p.id}`),
      })
      for (const e of p.environments)
        for (const c of e.configs)
          out.push({
            id: `c:${c.id}`, group: 'Configs', label: `${e.slug} / ${c.name}`, sublabel: p.name,
            keywords: `${p.name} ${e.slug} ${c.name} secrets`,
            run: () => router.go(`/projects/${p.id}/configs/${c.id}`),
          })
    }
    for (const [label, to, kw] of NAV)
      out.push({ id: `n:${to}`, group: 'Navigate', label, keywords: kw, run: () => router.go(to) })
    out.push({
      id: 'a:theme', group: 'Actions', label: `Switch to ${theme.current === 'daylight' ? 'Nightwatch' : 'Daylight'}`,
      keywords: 'theme dark light night day toggle', run: () => theme.toggle(),
    })
    out.push({
      id: 'a:export', group: 'Actions', label: 'Export audit ledger (CSV)',
      keywords: 'export audit csv download', run: () => { location.href = api.auditExportUrl('csv') },
    })
    out.push({
      id: 'a:shortcuts', group: 'Actions', label: 'Keyboard shortcuts',
      keywords: 'keyboard shortcuts help keys chords hotkeys', run: () => shortcuts.openHelp(),
    })
    return out
  })

  // Server-filtered "Secret keys" group. These are NOT re-filtered locally (the
  // API already substring-matched the query); they stream in and participate in
  // the same keyboard-navigable match list.
  const keyItems = $derived.by((): Item[] => {
    const out: Item[] = []
    for (const r of keyHits) {
      out.push({
        id: `k:${r.config_id}:${r.key}`, group: 'Secret keys', label: r.key,
        sublabel: `${r.project_name} · ${r.environment_slug} / ${r.config_name}`,
        keywords: '',
        run: () => router.go(`/projects/${r.project_id}/configs/${r.config_id}?key=${encodeURIComponent(r.key)}`),
      })
    }
    if (keysTruncated)
      out.push({
        id: 'k:truncated', group: 'Secret keys', label: 'Refine to see more…',
        keywords: '', hint: true, run: () => {},
      })
    return out
  })

  const matches = $derived.by(() => {
    const q = query.trim().toLowerCase()
    const local = !q
      ? items.slice(0, 12)
      : (() => {
          const terms = q.split(/\s+/)
          return items
            .filter(i => terms.every(t => (i.label + ' ' + i.keywords).toLowerCase().includes(t)))
            .slice(0, 12)
        })()
    return [...local, ...keyItems]
  })

  $effect(() => {
    void matches
    cursor = 0
  })

  function onKeydown(e: KeyboardEvent) {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
      e.preventDefault()
      open = !open
      if (open) { query = ''; setTimeout(() => inputEl?.focus(), 30) }
      return
    }
    if (!open) return
    if (e.key === 'Escape') { open = false }
    else if (e.key === 'ArrowDown') { e.preventDefault(); cursor = Math.min(cursor + 1, matches.length - 1) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); cursor = Math.max(cursor - 1, 0) }
    else if (e.key === 'Enter' && matches[cursor]) { e.preventDefault(); pick(matches[cursor]) }
  }

  function pick(i: Item) {
    if (i.hint) return   // non-interactive "Refine to see more…" row
    open = false
    i.run()
  }
</script>

<svelte:window onkeydown={onKeydown} />

{#if open}
  <div class="veil" role="presentation" onclick={() => (open = false)}>
    <div class="palette plate" role="dialog" aria-label="Command palette" onclick={(e) => e.stopPropagation()}>
      <input
        bind:this={inputEl}
        class="pal-input mono"
        placeholder="Find a project, config, page, or action…"
        bind:value={query}
      />
      <ol class="results">
        {#each matches as m, i (m.id)}
          <li>
            {#if m.hint}
              <div class="result hint folio">
                <span class="grp folio">{m.group}</span>
                <span class="lbl">{m.label}</span>
              </div>
            {:else}
              <button class="result" class:hot={i === cursor} onclick={() => pick(m)} onmouseenter={() => (cursor = i)}>
                <span class="grp folio">{m.group}</span>
                <span class="lbl">{m.label}</span>
                {#if m.sublabel}<span class="sub folio">{m.sublabel}</span>{/if}
              </button>
            {/if}
          </li>
        {:else}
          <li class="none folio">Nothing matches “{query}”.</li>
        {/each}
      </ol>
      <div class="pal-foot folio">
        <span><kbd class="key">↑↓</kbd> navigate</span>
        <span><kbd class="key">↵</kbd> open</span>
        <span><kbd class="key">esc</kbd> close</span>
        <span><kbd class="key">?</kbd> shortcuts</span>
      </div>
    </div>
  </div>
{/if}

<style>
  .veil {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.25);
    z-index: 100;
    display: flex;
    justify-content: center;
    padding-top: 12vh;
  }
  .palette {
    width: min(560px, 92vw);
    height: fit-content;
    overflow: hidden;
    animation: rise-in var(--t-fast) var(--ease-out) both;
  }
  .pal-input {
    width: 100%;
    font-size: var(--text-md);
    padding: var(--s4) var(--s5);
    border: 0;
    border-bottom: 2px solid var(--rule-strong);
    background: var(--paper-high);
    color: var(--ink);
  }
  .pal-input:focus { outline: none; border-bottom-color: var(--archivist); }
  .pal-input::placeholder { color: var(--ink-ghost); font-family: var(--font-ui); font-size: var(--text-sm); }

  .results { list-style: none; max-height: 46vh; overflow-y: auto; }
  .result {
    display: grid;
    grid-template-columns: 80px 1fr auto;
    align-items: baseline;
    gap: var(--s3);
    width: 100%;
    text-align: left;
    padding: var(--s2) var(--s5);
    background: transparent;
    border: 0;
    border-bottom: 1px solid var(--rule-faint);
    cursor: pointer;
    font-family: var(--font-ui);
    color: var(--ink);
    font-size: var(--text-sm);
  }
  .result.hot { background: var(--archivist-wash); box-shadow: inset 3px 0 0 var(--archivist); }
  .result.hint { cursor: default; color: var(--ink-ghost); font-style: italic; }
  .grp { font-size: 0.6rem; }
  .lbl { font-weight: 560; }
  .none { padding: var(--s4) var(--s5); }

  .pal-foot {
    display: flex;
    gap: var(--s4);
    padding: var(--s2) var(--s5);
    background: var(--paper-low);
  }
</style>
