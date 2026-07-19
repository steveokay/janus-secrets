<script lang="ts">
  import type { Snippet } from 'svelte'
  import { router } from '../lib/router.svelte'
  import { session } from '../lib/session.svelte'
  import { registry } from '../lib/registry.svelte'
  import { theme } from '../lib/theme.svelte'
  import { dialog } from '../lib/dialog.svelte'
  import JanusMark from '../components/JanusMark.svelte'
  import CommandPalette from '../components/CommandPalette.svelte'

  let { children }: { children: Snippet } = $props()

  const sections: Array<{ title: string; items: Array<{ code: string; label: string; href: string }> }> = [
    {
      title: 'Registry',
      items: [
        { code: 'OV', label: 'Overview', href: '/' },
        { code: 'PR', label: 'Projects', href: '/projects' },
      ],
    },
    {
      title: 'Instruments',
      items: [
        { code: 'TR', label: 'Transit', href: '/transit' },
        { code: 'OP', label: 'Operations', href: '/operations' },
        { code: 'IN', label: 'Integrations', href: '/integrations' },
      ],
    },
    {
      title: 'Record',
      items: [
        { code: 'AU', label: 'Audit ledger', href: '/audit' },
        { code: 'AP', label: 'Approvals', href: '/approvals' },
      ],
    },
    {
      title: 'Office',
      items: [
        { code: 'TK', label: 'Service tokens', href: '/tokens' },
        { code: 'MB', label: 'Members', href: '/members' },
        { code: 'ST', label: 'Settings', href: '/settings' },
        { code: 'TS', label: 'Trash', href: '/trash' },
      ],
    },
  ]

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

<CommandPalette />

<div class="shell">
  <aside class="cover">
    <a class="wordmark" href="/">
      <JanusMark size={40} stroke="var(--cover-fg)" />
      <div>
        <span class="wm-name">Janus</span>
        <span class="wm-sub">Secrets Registry</span>
      </div>
    </a>

    <nav>
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
        <span>Unsealed · {session.sealType === 'shamir' ? `Shamir ${session.threshold}-of-${session.totalShares}` : 'AWS KMS'}</span>
      </div>
      <div class="foot-stats">
        <span class="mono">{registry.totalReads24h.toLocaleString()}</span> reads · 24 h
      </div>
      <button class="seal-btn" onclick={sealServer} title="Seal the server">
        Seal server
      </button>
    </div>
  </aside>

  <div class="desk">
    <header class="folio-bar">
      <span class="folio">Janus · self-hosted · single-tenant · <kbd class="key">ctrl</kbd><kbd class="key">K</kbd> to search</span>
      <div class="folio-right">
        {#if registry.verify?.valid}
          <span class="chain-ok" title="Audit hash chain verified">
            <svg width="12" height="12" viewBox="0 0 12 12" fill="none" aria-hidden="true">
              <path d="M2.5 6.5 L5 9 L9.5 3.5" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
            Chain verified
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
  }

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
  }
</style>
