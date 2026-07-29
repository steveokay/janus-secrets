<script lang="ts">
  import type { Snippet } from 'svelte'
  import { router } from '../lib/router.svelte'
  import { session } from '../lib/session.svelte'
  import { sealTypeLabel } from '../lib/api'
  import { registry } from '../lib/registry.svelte'
  import { theme } from '../lib/theme.svelte'
  import { dialog } from '../lib/dialog.svelte'
  import JanusMark from '../components/JanusMark.svelte'
  import CommandPalette from '../components/CommandPalette.svelte'
  import ShortcutsHelp from '../components/ShortcutsHelp.svelte'
  import { trapFocus } from '../lib/a11y'
  import { sectionsFor, holds } from '../lib/nav'

  let { children }: { children: Snippet } = $props()

  /* ── narrow-viewport nav drawer ───────────────────────────────
     Below --bp-shell the cover can't sit beside the desk (236px of a 390px
     phone is most of the screen), so it becomes an off-canvas drawer. Desktop
     is untouched: the drawer state is simply ignored above the breakpoint,
     where the cover is statically positioned by the same CSS. */
  let navOpen = $state(false)

  // Close on navigation — otherwise tapping a tab leaves the drawer covering
  // the page it just opened.
  $effect(() => {
    router.path
    navOpen = false
  })

  function onWindowKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape' && navOpen) {
      e.stopPropagation()
      navOpen = false
    }
  }

  /* The rail renders whatever the principal can actually use. Both this and the
     command palette read the same list in lib/nav.ts — two lists would mean a
     hidden nav item still reachable from the palette, which is no gating at
     all. $derived so it re-filters the moment the session resolves. */
  const sections = $derived(sectionsFor(session.me?.permissions))

  /* Sealing is an instance-scoped operation. Offering the button to someone who
     will be refused is the same discover-by-403 the nav gating removes — and
     this one reads worse, because the refusal arrives as a modal after a
     confirm dialog that says the server is about to stop serving secrets. */
  const canSeal = $derived(holds(session.me?.permissions, 'instance', 'sys:seal'))

  function isActive(href: string): boolean {
    if (href === '/') return router.path === '/'
    return router.path === href || router.path.startsWith(href + '/')
  }

  const initials = $derived(
    (session.me?.name ?? '?')
      .split(/[@\s.]+/)
      .filter(Boolean)
      .slice(0, 2)
      .map(w => w[0]!.toUpperCase())
      .join(''),
  )

  async function sealServer() {
    const ok = await dialog.confirm({
      title: 'Seal the server?',
      body: 'The master key is dropped from memory. All secret operations fail until an operator unseals again.',
      confirmLabel: 'Seal server',
      danger: true,
    })
    if (!ok) return
    try {
      await session.sealServer()
    } catch {
      await dialog.notice({ title: 'Seal failed', body: 'Requires the sys:seal permission.' })
    }
  }
</script>

<svelte:window onkeydown={onWindowKeydown} />

<CommandPalette />
<ShortcutsHelp />

<div class="shell" class:nav-open={navOpen}>
  <!-- Scrim sits under the drawer and above the desk. Only rendered while open
       so it can never swallow clicks on desktop. -->
  {#if navOpen}
    <button
      class="nav-scrim"
      aria-label="Close navigation"
      onclick={() => (navOpen = false)}
    ></button>
  {/if}

  <aside class="cover" class:open={navOpen} use:trapFocus={navOpen}>
    <a class="wordmark" href="/">
      <JanusMark size={40} stroke="var(--cover-fg)" />
      <div>
        <span class="wm-name">Janus</span>
        <span class="wm-sub">Secrets Registry</span>
      </div>
    </a>

    <nav id="shell-nav">
      {#each sections as sec}
        <div class="nav-section">
          <span class="nav-title">{sec.title}</span>
          {#each sec.items as item}
            <a class="tab" class:active={isActive(item.href)} href={item.href}>
              <span class="tab-code">{item.code}</span>
              <span class="tab-label">{item.label}</span>
            </a>
          {/each}
        </div>
      {/each}
    </nav>

    <div class="cover-foot">
      <div class="seal-line">
        <span class="seal-dot" aria-hidden="true"></span>
        <span>Unsealed · {session.sealType === 'shamir' ? `Shamir ${session.threshold}-of-${session.totalShares}` : sealTypeLabel(session.sealType)}</span>
      </div>
      <div class="foot-stats">
        <span class="mono">{registry.totalReads24h.toLocaleString()}</span> reads · 24 h
      </div>
      {#if canSeal}
        <button class="seal-btn" onclick={sealServer} title="Seal the server">
          Seal server
        </button>
      {/if}
    </div>
  </aside>

  <div class="desk">
    <header class="folio-bar">
      <button
        class="nav-toggle"
        aria-label={navOpen ? 'Close navigation' : 'Open navigation'}
        aria-expanded={navOpen}
        aria-controls="shell-nav"
        onclick={() => (navOpen = !navOpen)}
      >
        <svg width="18" height="18" viewBox="0 0 18 18" fill="none" aria-hidden="true">
          {#if navOpen}
            <path d="M4 4 L14 14 M14 4 L4 14" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/>
          {:else}
            <path d="M2.5 4.5h13M2.5 9h13M2.5 13.5h13" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/>
          {/if}
        </svg>
      </button>
      <!-- Keyboard hints are desktop-only guidance; hidden on touch widths
           rather than allowed to wrap into a six-line block. -->
      <span class="folio folio-hint">Janus · self-hosted · single-tenant · <kbd class="key">ctrl</kbd><kbd class="key">K</kbd> to search · <kbd class="key">?</kbd> shortcuts</span>
      <div class="folio-right">
        {#if registry.verify?.valid}
          <span class="chain-ok" title="Audit hash chain verified" aria-label="Audit hash chain verified">
            <svg width="12" height="12" viewBox="0 0 12 12" fill="none" aria-hidden="true">
              <path d="M2.5 6.5 L5 9 L9.5 3.5" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
            <span class="chain-ok-label">Chain verified</span>
          </span>
          <span class="folio-sep" aria-hidden="true">·</span>
        {/if}
        <div class="theme-seg" role="group" aria-label="Theme">
          <button class="theme-btn" class:on={theme.current === 'daylight'} onclick={() => theme.set('daylight')}>Day</button>
          <button class="theme-btn" class:on={theme.current === 'nightwatch'} onclick={() => theme.set('nightwatch')}>Night</button>
        </div>
        <span class="folio-sep" aria-hidden="true">·</span>
        <button class="user-chip" onclick={() => session.logout()} title="Sign out">
          <span class="user-initials">{initials}</span>
          <span class="user-name">{session.me?.name}</span>
        </button>
      </div>
    </header>

    <main class="page">
      {@render children()}
    </main>
  </div>
</div>

<style>
  .shell {
    display: grid;
    grid-template-columns: 236px 1fr;
    height: 100vh;
    /* dvh tracks the collapsing mobile URL bar; vh above is the fallback for
       engines without it. Without this the footer sits under the browser
       chrome and the page grows a phantom scroll. */
    height: 100dvh;
  }

  /* Drawer affordances: inert (display:none) above the breakpoint so they can
     never intercept a desktop click. */
  .nav-toggle { display: none; }
  .nav-scrim { display: none; }

  /* ── ledger cover (sidebar) ─────────────────── */
  .cover {
    background: var(--cover-bg);
    background-image:
      repeating-linear-gradient(0deg, rgba(255,255,255,0.02) 0 1px, transparent 1px 4px);
    color: var(--cover-fg);
    display: flex;
    flex-direction: column;
    padding: var(--s5) 0 var(--s4) var(--s4);
    overflow-y: auto;
  }

  .wordmark {
    display: flex;
    align-items: center;
    gap: var(--s3);
    color: var(--cover-fg);
    margin-right: var(--s4);
    padding-bottom: var(--s5);
    border-bottom: 1px solid var(--cover-line);
  }
  .wordmark:hover { text-decoration: none; }
  .wm-name {
    display: block;
    font-family: var(--font-display);
    font-size: 1.45rem;
    font-weight: 600;
    letter-spacing: 0.01em;
    line-height: 1;
  }
  .wm-sub {
    display: block;
    font-size: 0.62rem;
    text-transform: uppercase;
    letter-spacing: 0.22em;
    color: var(--cover-muted);
    margin-top: 0.3rem;
  }

  nav { flex: 1; padding-top: var(--s4); }

  .nav-section { margin-bottom: var(--s5); }
  .nav-title {
    display: block;
    font-size: 0.62rem;
    font-weight: 650;
    text-transform: uppercase;
    letter-spacing: 0.24em;
    color: var(--cover-faint);
    margin: 0 0 var(--s2);
  }

  /* index tabs — active tab tears out onto the paper */
  .tab {
    display: flex;
    align-items: center;
    gap: var(--s3);
    color: var(--cover-muted);
    font-size: var(--text-sm);
    font-weight: 500;
    padding: 0.42rem var(--s3) 0.42rem var(--s2);
    margin: 1px 0;
    border-radius: 3px 0 0 3px;
    position: relative;
    transition: background var(--t-fast), color var(--t-fast);
  }
  .tab:hover { background: var(--cover-hover); color: var(--cover-fg); text-decoration: none; }
  .tab-code {
    font-family: var(--font-mono);
    font-size: 0.62rem;
    letter-spacing: 0.08em;
    color: var(--cover-faint);
    border: 1px solid var(--cover-line);
    border-radius: 2px;
    padding: 0.06rem 0.28rem;
    min-width: 1.9rem;
    text-align: center;
    transition: all var(--t-fast);
  }
  .tab.active {
    background: var(--paper);
    color: var(--ink);
    font-weight: 620;
    box-shadow: 0 1px 0 rgba(0,0,0,0.25);
  }
  .tab.active .tab-code {
    color: var(--vermilion);
    border-color: var(--vermilion);
    font-weight: 600;
  }

  /* ── cover footer ───────────────────────────── */
  .cover-foot {
    margin-right: var(--s4);
    padding-top: var(--s4);
    border-top: 1px solid var(--cover-line);
    font-size: var(--text-xs);
    color: var(--cover-muted);
    display: flex;
    flex-direction: column;
    gap: var(--s2);
  }
  .seal-line { display: flex; align-items: center; gap: var(--s2); }
  .seal-dot {
    width: 7px; height: 7px; border-radius: 50%;
    background: #6fbf92;
    box-shadow: 0 0 6px rgba(111,191,146,0.8);
  }
  .foot-stats .mono { color: var(--cover-fg); }
  .seal-btn {
    align-self: flex-start;
    font-family: var(--font-ui);
    font-size: 0.62rem;
    font-weight: 650;
    text-transform: uppercase;
    letter-spacing: 0.16em;
    color: var(--cover-muted);
    background: transparent;
    border: 1px solid var(--cover-line);
    border-radius: 2px;
    padding: 0.28rem 0.6rem;
    cursor: pointer;
    transition: all var(--t-fast);
  }
  .seal-btn:hover { border-color: var(--vermilion); color: var(--vermilion); }

  /* ── desk (main area) ───────────────────────── */
  .desk {
    display: flex;
    flex-direction: column;
    overflow: hidden;
    /* THE fix for clipped content. As a grid item the desk defaults to
       `min-width: auto` — "never narrower than my content" — so a wide ledger
       widened the track and `overflow: hidden` above then silently CLIPPED it.
       Nothing scrolled and nothing overflowed the document; the UI was just
       cut off. Allowing the track to shrink is what lets the existing
       `.table-wrap { overflow-x: auto }` inside actually engage. */
    min-width: 0;
  }

  .folio-bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.55rem var(--s6);
    border-bottom: 1px solid var(--rule);
    background: var(--bar-bg);
    backdrop-filter: blur(4px);
  }
  .folio-right { display: flex; align-items: center; gap: var(--s3); }
  .folio-sep { color: var(--ink-ghost); }

  .chain-ok {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    font-size: var(--text-xs);
    font-weight: 650;
    text-transform: uppercase;
    letter-spacing: 0.1em;
    color: var(--verdigris);
  }

  .theme-seg {
    display: inline-flex;
    border: 1px solid var(--rule-strong);
    border-radius: 2px;
    overflow: hidden;
  }
  .theme-btn {
    font-family: var(--font-ui);
    font-size: 0.6rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.12em;
    padding: 0.22rem 0.55rem;
    background: transparent;
    color: var(--ink-faint);
    border: 0;
    cursor: pointer;
  }
  .theme-btn + .theme-btn { border-left: 1px solid var(--rule); }
  .theme-btn.on { background: var(--ink); color: var(--paper-high); }

  .user-chip {
    display: inline-flex;
    align-items: center;
    gap: var(--s2);
    background: transparent;
    border: 0;
    cursor: pointer;
    font-family: var(--font-ui);
    font-size: var(--text-xs);
    color: var(--ink-soft);
    padding: 0.2rem 0.3rem;
    border-radius: 2px;
  }
  .user-chip:hover { background: var(--paper-low); }
  .user-initials {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 24px; height: 24px;
    border-radius: 50%;
    border: 1.5px solid var(--ink);
    font-weight: 700;
    font-size: 0.62rem;
    letter-spacing: 0.03em;
  }

  .page {
    flex: 1;
    overflow-y: auto;
    padding: var(--s6) var(--s6) var(--s8);
    /* The desk is a grid track, so its children may demand their intrinsic
       width and blow the track out. This lets the column actually shrink, which
       is what makes the in-page scroll containers (.scroll-x) take effect
       instead of the whole desk widening. */
    min-width: 0;
  }

  /* ─────────────────────────────────────────────────────────────
     Narrow viewports — the cover becomes an off-canvas drawer.

     1024px keeps the sidebar for tablet LANDSCAPE (a real working width) and
     drops it for portrait/phone, where 236px of static chrome is most of the
     screen. Everything above this line is untouched by the rules below.
     ───────────────────────────────────────────────────────────── */
  @media (max-width: 1024px) {
    .shell {
      /* One column: the desk gets the whole viewport and the cover is lifted
         out of flow into the drawer below. */
      grid-template-columns: 1fr;
    }

    .nav-toggle {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      /* 40px: comfortably over the 24px minimum touch target, and it is the
         first control on the page so it must be easy to hit. */
      width: 40px;
      height: 40px;
      margin-right: var(--s2);
      flex: none;
      background: transparent;
      border: 1px solid var(--rule-strong);
      border-radius: var(--radius);
      color: var(--ink);
      cursor: pointer;
    }
    .nav-toggle:hover { background: var(--paper-low); }

    .cover {
      position: fixed;
      inset: 0 auto 0 0;
      width: min(280px, 82vw);
      z-index: 61;
      padding-left: var(--s4);
      transform: translateX(-100%);
      transition: transform var(--t-med) var(--ease-out);
      box-shadow: none;
      /* Keep it out of the tab order (and off screen readers) while closed —
         a visually hidden drawer that is still focusable is a keyboard trap of
         its own. */
      visibility: hidden;
    }
    .cover.open {
      transform: none;
      visibility: visible;
      box-shadow: 4px 0 24px rgba(0, 0, 0, 0.35);
    }

    .nav-scrim {
      display: block;
      position: fixed;
      inset: 0;
      z-index: 60;
      border: 0;
      padding: 0;
      background: rgba(0, 0, 0, 0.45);
      cursor: pointer;
    }

    /* Give the content back the width the sidebar was taking. */
    .page { padding: var(--s5) var(--s4) var(--s7); }
    .folio-bar { padding: 0.5rem var(--s4); }
  }

  /* The keyboard hints are desktop guidance and the single longest string in
     the bar; drop them before anything useful has to wrap. */
  @media (max-width: 1180px) {
    .folio-hint { display: none; }
  }

  @media (max-width: 560px) {
    /* Below this the name pushes the bar over; the initials disc still
       identifies the signed-in user and stays a full tap target. */
    .user-name { display: none; }
    /* Tick alone; the accessible name on .chain-ok still carries the meaning
       for assistive tech, and the tooltip for a curious pointer. */
    .chain-ok-label { display: none; }
    .page { padding: var(--s4) var(--s3) var(--s7); }
  }

  /* The drawer slides; honour a reduced-motion preference by snapping it. The
     global rule in base.css already neutralises durations, but transform
     transitions on a fixed panel are the most motion-sensitive thing here, so
     be explicit. */
  @media (prefers-reduced-motion: reduce) {
    .cover { transition: none; }
  }
</style>
