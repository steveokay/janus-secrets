<script lang="ts">
  import { registry } from '../lib/registry.svelte'
  import { api } from '../lib/api'
  import { anySecretExists } from '../lib/ops'

  // First-run onboarding checklist. Shows on the dashboard until the essential
  // steps (project → secrets → token) are all done, then stays hidden. Fully
  // dismissible; the dismissal is remembered per browser. Everything it reads is
  // metadata (project/token/key existence) — never a secret value.

  const KEY = 'janus.onboarding.dismissed'

  let dismissed = $state(localStorage.getItem(KEY) === '1')
  let probed = $state(false)
  let hasSecret = $state(false)
  let tokenCount = $state(0)
  let sawIncomplete = $state(false)

  const hasProject = $derived(registry.projects.length > 0)
  const essentialDone = $derived(hasProject && hasSecret && tokenCount > 0)

  // Re-probe existence whenever the tree changes (e.g. a project/config was just
  // added), tolerating 403s so a scoped user without token:read still onboards.
  let seq = 0
  $effect(() => {
    if (!registry.loaded) return
    // Touch the reactive inputs so this re-runs when they change.
    void registry.projects.length
    void registry.configCount
    const mine = ++seq
    void (async () => {
      const [secret, tokens] = await Promise.all([
        anySecretExists(registry.projects),
        api.listTokens().then(t => t.length).catch(() => 0),
      ])
      if (mine !== seq) return
      hasSecret = secret
      tokenCount = tokens
      probed = true
    })()
  })

  // Once we've confirmed the instance is not yet set up, keep the panel visible
  // through completion (so the last "janus run" step and its command stay in
  // view) until the user dismisses it. An already-onboarded instance is never
  // flagged, so the checklist doesn't clutter an established dashboard.
  $effect(() => {
    if (probed && !essentialDone) sawIncomplete = true
  })

  const show = $derived(!dismissed && probed && (sawIncomplete || !essentialDone))

  const steps = $derived([
    { done: hasProject, label: 'Create a project', hint: 'Group secrets by app or team, with dev / staging / prod environments.', href: '/projects', cta: 'Open Projects' },
    { done: hasSecret, label: 'Add secrets', hint: 'Open a config and add your first key/value — paste an existing .env via Import…', href: firstConfigHref(), cta: 'Open a config' },
    { done: tokenCount > 0, label: 'Mint a service token', hint: 'A scoped, shown-once token lets a machine or CI read this config.', href: '/tokens', cta: 'Open Service tokens' },
  ])
  const doneCount = $derived(steps.filter(s => s.done).length)

  function firstConfigHref(): string {
    const p = registry.projects[0]
    const c = p?.environments.flatMap(e => e.configs)[0]
    return p && c ? `/projects/${p.id}/configs/${c.id}` : '/projects'
  }

  const runCmd = `janus login --addr ${window.location.origin}
janus setup                 # bind this directory to a project/config
janus run -- your-app       # secrets injected as env vars`

  let copied = $state(false)
  async function copyRun() {
    try {
      await navigator.clipboard.writeText(runCmd)
      copied = true
      setTimeout(() => (copied = false), 1800)
    } catch { /* clipboard blocked; the command is visible to copy by hand */ }
  }

  function dismiss() {
    dismissed = true
    localStorage.setItem(KEY, '1')
  }
</script>

{#if show}
  <section class="onboard sheet rise" aria-label="Getting started checklist">
    <div class="ob-head">
      <div>
        <h3>Set up Janus</h3>
        <p class="folio">{doneCount} of 3 done — a few steps to your first injected secret.</p>
      </div>
      <button class="btn btn-ghost btn-sm" onclick={dismiss}>Dismiss</button>
    </div>

    <ol class="ob-steps">
      {#each steps as s, i}
        <li class:done={s.done}>
          <span class="ob-mark" aria-hidden="true">
            {#if s.done}
              <svg width="12" height="12" viewBox="0 0 12 12" fill="none"><path d="M2.5 6.5 L5 9 L9.5 3.5" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"/></svg>
            {:else}{i + 1}{/if}
          </span>
          <div class="ob-body">
            <span class="ob-label">{s.label}</span>
            <span class="folio ob-hint">{s.hint}</span>
          </div>
          {#if s.done}
            <span class="pill pill-info ob-status">Done</span>
          {:else}
            <a class="btn btn-sm" href={s.href}>{s.cta}</a>
          {/if}
        </li>
      {/each}

      <li class:done={essentialDone} class="ob-run-step">
        <span class="ob-mark" aria-hidden="true">
          {#if essentialDone}
            <svg width="12" height="12" viewBox="0 0 12 12" fill="none"><path d="M2.5 6.5 L5 9 L9.5 3.5" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"/></svg>
          {:else}4{/if}
        </span>
        <div class="ob-body ob-run">
          <span class="ob-label">Inject with <code class="mono">janus run</code></span>
          <span class="folio ob-hint">From your project directory, hand secrets to a process as environment variables — no plaintext on disk.</span>
          <div class="ob-cmd">
            <pre class="mono">{runCmd}</pre>
            <button class="btn btn-ghost btn-sm ob-copy" onclick={copyRun}>{copied ? 'Copied' : 'Copy'}</button>
          </div>
          <a class="folio ob-doc" href="/projects" onclick={(e) => { e.preventDefault(); window.open('https://github.com/steveokay/janus-secrets/blob/main/docs/guides/getting-started.md', '_blank', 'noopener') }}>Getting-started guide →</a>
        </div>
      </li>
    </ol>
  </section>
{/if}

<style>
  .onboard {
    padding: var(--s4) var(--s5);
    margin-bottom: var(--s5);
    border-left: 4px solid var(--verdigris);
  }
  .ob-head {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: var(--s3);
    margin-bottom: var(--s3);
  }
  .ob-head h3 { font-family: var(--font-display); font-size: var(--text-lg); }
  .ob-head .folio { margin-top: 0.15rem; }

  .ob-steps { list-style: none; }
  .ob-steps > li {
    display: flex;
    align-items: flex-start;
    gap: var(--s3);
    padding: var(--s3) 0;
    border-top: 1px solid var(--rule-faint);
  }
  .ob-steps > li:first-child { border-top: 0; }

  .ob-mark {
    flex: none;
    width: 22px; height: 22px;
    border-radius: 50%;
    border: 1.5px solid var(--rule-strong);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    font-family: var(--font-mono);
    font-size: 0.7rem;
    font-weight: 600;
    color: var(--ink-soft);
    margin-top: 0.1rem;
  }
  li.done .ob-mark {
    border-color: var(--verdigris);
    background: var(--verdigris-wash);
    color: var(--verdigris);
  }

  .ob-body { display: flex; flex-direction: column; gap: 0.15rem; flex: 1; min-width: 0; }
  .ob-label { font-weight: 560; font-size: var(--text-sm); }
  .ob-hint { color: var(--ink-soft); }
  li.done .ob-label { color: var(--ink-soft); }

  .ob-status { align-self: center; flex: none; }

  .ob-run { gap: var(--s2); }
  .ob-cmd { position: relative; margin-top: var(--s1); }
  .ob-cmd pre {
    background: var(--paper-low);
    border: 1px solid var(--rule);
    border-radius: 3px;
    padding: var(--s3) var(--s4);
    font-size: var(--text-xs);
    line-height: 1.6;
    overflow-x: auto;
    white-space: pre;
    color: var(--ink);
  }
  .ob-copy { position: absolute; top: var(--s2); right: var(--s2); }
  .ob-doc { align-self: flex-start; color: var(--archivist); }
</style>
