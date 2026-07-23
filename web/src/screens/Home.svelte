<script lang="ts">
  import { session } from '../lib/session.svelte'
  import { registry } from '../lib/registry.svelte'
  import { api, type RotationPolicy, type SyncTargetApi, type ApiLease, type BreakGlassGrant } from '../lib/api'
  import { listAllRotations, listAllSyncs, listAllDynamicRoles, listAllLeases, countAllStaleKeys, countAllUnusedKeys } from '../lib/ops'
  import { relTime } from '../lib/util'
  import Sparkline from '../components/Sparkline.svelte'
  import Guilloche from '../components/Guilloche.svelte'
  import OnboardingChecklist from '../components/OnboardingChecklist.svelte'

  interface TrayItem { kind: string; text: string; href: string; when: string }
  let tray = $state<TrayItem[]>([])

  $effect(() => {
    if (registry.loaded) void loadTray()
  })

  async function loadTray() {
    const [rots, syncs] = await Promise.all([
      listAllRotations(registry.projects).catch(() => [] as RotationPolicy[]),
      listAllSyncs(registry.projects).catch(() => [] as SyncTargetApi[]),
    ])
    const roles = await listAllDynamicRoles(registry.projects).catch(() => [])
    const leases = await listAllLeases(roles).catch(() => [] as ApiLease[])
    const [staleKeys, unusedKeys, breakGlass] = await Promise.all([
      countAllStaleKeys(registry.projects).catch(() => 0),
      countAllUnusedKeys(registry.projects).catch(() => 0),
      api.listBreakGlass().catch(() => [] as BreakGlassGrant[]),
    ])
    const items: TrayItem[] = []
    // Active break-glass is the loudest signal — surface it first.
    if (breakGlass.length > 0)
      items.push({ kind: 'breakglass', text: `${breakGlass.length} active break-glass grant${breakGlass.length === 1 ? '' : 's'}`, href: '/break-glass', when: new Date().toISOString() })
    if (staleKeys > 0)
      items.push({ kind: 'stale', text: `${staleKeys} secret${staleKeys === 1 ? '' : 's'} past max-age`, href: '/projects', when: new Date().toISOString() })
    if (unusedKeys > 0)
      items.push({ kind: 'unused', text: `${unusedKeys} secret${unusedKeys === 1 ? '' : 's'} not read in 90d`, href: '/projects', when: new Date().toISOString() })
    for (const r of rots.filter(r => r.failure_count > 0 || r.status === 'failed'))
      items.push({ kind: 'failed', text: `Rotation ${r.secret_key} failing on ${registry.configLabel(r.config_id)}`, href: '/operations', when: r.last_rotated_at ?? r.created_at })
    for (const s of syncs.filter(s => s.failure_count > 0 || s.status === 'error'))
      items.push({ kind: 'drift', text: `Sync to ${s.provider} for ${registry.configLabel(s.config_id)} is failing`, href: '/operations', when: s.last_synced_at ?? s.next_sync_at })
    for (const l of leases.filter(l => l.status === 'active' && new Date(l.expires_at).getTime() - Date.now() < 3600_000))
      items.push({ kind: 'expiring', text: `Lease ${l.id.slice(0, 12)}… (${l.db_username}) expires ${relTime(l.expires_at)}`, href: '/operations', when: l.created_at })
    for (const e of registry.recentEvents.filter(e => e.result === 'denied').slice(0, 3))
      items.push({ kind: 'denied', text: `Denied ${e.action} on ${e.resource} from ${e.ip}`, href: '/audit', when: e.occurred_at })
    tray = items.slice(0, 6)
  }

  const spark = $derived(
    registry.histogram.length >= 2
      ? registry.histogram.map(b => b.success + b.denied + b.error)
      : [],
  )

  const today = new Date().toLocaleDateString('en-GB', { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric' })
  const hour = new Date().getHours()
  const greeting = hour < 12 ? 'Good morning' : hour < 18 ? 'Good afternoon' : 'Good evening'
  const firstName = $derived((session.me?.name ?? 'registrar').split(/[@\s]/)[0])
</script>

<div class="home">
  <header class="masthead rise">
    <div>
      <p class="folio">{today} · The daily registry</p>
      <h1>{greeting}, <em>{firstName}.</em></h1>
      <p class="standfirst">
        {registry.totalReads24h.toLocaleString()} secret read{registry.totalReads24h === 1 ? '' : 's'} in the last 24 hours.
        {#if registry.verify?.valid}
          The chain holds — <a href="/audit">{registry.verify.count.toLocaleString()} event{registry.verify.count === 1 ? '' : 's'} on record</a>, all verified.
        {:else if registry.verify}
          <strong style="color: var(--vermilion)">Audit chain verification FAILED — investigate immediately.</strong>
        {/if}
      </p>
    </div>
    <div class="masthead-stamp">
      {#if registry.verify?.valid}
        <span class="stamp ok">Chain verified</span>
      {/if}
      <div class="masthead-rosette" aria-hidden="true"><Guilloche size={200} rings={12} opacity={0.16} /></div>
    </div>
  </header>

  <hr class="ledger-rule" />

  <OnboardingChecklist />

  <div class="stat-strip rise" style="animation-delay: 60ms">
    <div class="stat">
      <span class="label">Reads · 24 h</span>
      <div class="stat-line">
        <span class="stat-num">{registry.totalReads24h.toLocaleString()}</span>
        {#if spark.length >= 2}<Sparkline data={spark} width={110} height={30} />{/if}
      </div>
    </div>
    <div class="stat">
      <span class="label">Projects</span>
      <span class="stat-num">{registry.projects.length}</span>
    </div>
    <div class="stat">
      <span class="label">Configs</span>
      <span class="stat-num">{registry.configCount}</span>
    </div>
    <div class="stat">
      <span class="label">Events on record</span>
      <span class="stat-num">{registry.verify ? registry.verify.count.toLocaleString() : '—'}</span>
    </div>
    <div class="stat" class:alert={registry.denied24h > 0}>
      <span class="label">Denied · 24 h</span>
      <span class="stat-num">{registry.denied24h}</span>
    </div>
  </div>

  <div class="columns">
    <section class="main-col">
      <div class="intray sheet rise" style="animation-delay: 120ms">
        <div class="section-head">
          <h3>In tray</h3>
          <span class="folio">{tray.length ? `${tray.length} matter${tray.length === 1 ? '' : 's'} requiring attention` : 'nothing requires attention'}</span>
        </div>
        <ul>
          {#each tray as a}
            <li>
              <span class="tray-mark {a.kind}" aria-hidden="true"></span>
              <a href={a.href}>{a.text}</a>
              <span class="folio">{relTime(a.when)}</span>
            </li>
          {:else}
            <li class="clear-day">
              <span class="tray-mark ok" aria-hidden="true"></span>
              All quiet — rotations healthy, syncs current, no denials on record today.
            </li>
          {/each}
        </ul>
      </div>

      <div class="section-head" style="margin-top: var(--s6)">
        <h3>Registry</h3>
        <a class="folio" href="/projects">All projects →</a>
      </div>
      {#if registry.loading && !registry.projects.length}
        <p class="folio">Opening the registry…</p>
      {/if}
      <div class="proj-grid">
        {#each registry.projects as p, i}
          <a class="proj-card sheet rise" style={`animation-delay: ${160 + i * 50}ms`} href={`/projects/${p.id}`}>
            <div class="proj-head">
              <span class="proj-slug mono">{p.slug.toUpperCase()}</span>
              <span class="proj-name">{p.name}</span>
            </div>
            <div class="proj-envs">
              {#each p.environments as env}
                <span class="pill pill-{env.kind}">{env.slug}</span>
              {/each}
            </div>
            <div class="proj-foot folio">
              {p.environments.reduce((a, e) => a + e.configs.length, 0)} configs ·
              {p.environments.flatMap(e => e.configs).reduce((a, c) => a + c.reads24h, 0).toLocaleString()} reads/24h ·
              active {relTime(p.lastActivityAt ?? p.createdAt)}
            </div>
          </a>
        {:else}
          {#if !registry.loading}
            <div class="sheet empty-card">
              <p>No projects on record yet.</p>
              <a class="btn btn-primary" href="/projects">Open the first dossier</a>
            </div>
          {/if}
        {/each}
      </div>
    </section>

    <aside class="activity perforated rise" style="animation-delay: 200ms">
      <div class="section-head">
        <h3>The record</h3>
        <a class="folio" href="/audit">Full ledger →</a>
      </div>
      <ol>
        {#each registry.recentEvents.slice(0, 9) as ev}
          <li class:denied={ev.result === 'denied'}>
            <div class="ev-line1">
              <span class="ev-action mono">{ev.action}</span>
              <span class="folio">{relTime(ev.occurred_at)}</span>
            </div>
            <div class="ev-line2">
              <span class="ev-actor">{ev.actor_name}</span>
              <span class="ev-res">{ev.resource}</span>
            </div>
            {#if ev.result === 'denied'}<span class="stamp flat denied-stamp">Denied</span>{/if}
          </li>
        {:else}
          <li><span class="folio">The ledger is empty.</span></li>
        {/each}
      </ol>
    </aside>
  </div>
</div>

<style>
  .home { max-width: 1200px; margin: 0 auto; }

  .masthead {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: var(--s5);
  }
  .masthead h1 { margin: var(--s2) 0 var(--s3); font-size: var(--text-3xl); }
  .masthead h1 em { font-style: italic; font-weight: 480; }
  .standfirst { color: var(--ink-soft); max-width: 52ch; }

  .masthead-stamp { position: relative; padding: var(--s5) var(--s4) 0 0; }
  .masthead-rosette {
    position: absolute;
    top: -34px; right: -40px;
    color: var(--ink);
    pointer-events: none;
    z-index: -1;
  }

  /* ── stat strip ─────────────────────────────── */
  .stat-strip {
    display: grid;
    grid-template-columns: 1.6fr repeat(4, 1fr);
    margin: var(--s5) 0 var(--s6);
  }
  .stat {
    padding: var(--s3) var(--s4);
    border-right: 1px solid var(--rule);
    display: flex;
    flex-direction: column;
    gap: var(--s1);
  }
  .stat:first-child { padding-left: 0; }
  .stat:last-child { border-right: 0; }
  .stat-line { display: flex; align-items: flex-end; gap: var(--s3); }
  .stat-num {
    font-family: var(--font-display);
    font-size: var(--text-xl);
    font-weight: 560;
    line-height: 1;
    font-variant-numeric: tabular-nums;
  }
  .stat.alert .stat-num { color: var(--vermilion); }

  /* ── columns ────────────────────────────────── */
  .columns {
    display: grid;
    grid-template-columns: 1fr 340px;
    gap: var(--s6);
    align-items: start;
  }

  .section-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    margin-bottom: var(--s3);
  }

  /* ── in tray ────────────────────────────────── */
  .intray { padding: var(--s4) var(--s5); }
  .intray .section-head { margin-bottom: var(--s2); }
  .intray ul { list-style: none; }
  .intray li {
    display: flex;
    align-items: center;
    gap: var(--s3);
    padding: var(--s2) 0;
    border-top: 1px solid var(--rule-faint);
    font-size: var(--text-sm);
  }
  .intray li a { color: var(--ink); flex: 1; }
  .intray li a:hover { color: var(--archivist); }
  .clear-day { color: var(--ink-soft); }
  .tray-mark { width: 8px; height: 8px; border-radius: 50%; flex: none; }
  .tray-mark.failed { background: var(--vermilion); }
  .tray-mark.drift { background: var(--ochre); }
  .tray-mark.expiring { background: var(--ochre); }
  .tray-mark.stale { background: var(--ochre); }
  .tray-mark.unused { background: var(--ochre); }
  .tray-mark.denied { background: var(--vermilion); }
  .tray-mark.breakglass { background: var(--vermilion); box-shadow: 0 0 0 3px var(--vermilion-wash); }
  .tray-mark.ok { background: var(--verdigris); }

  /* ── project cards ──────────────────────────── */
  .proj-grid {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: var(--s4);
  }
  .proj-card {
    display: block;
    padding: var(--s4) var(--s5);
    color: var(--ink);
    transition: box-shadow var(--t-med) var(--ease-out), transform var(--t-med) var(--ease-out);
  }
  .proj-card:hover {
    text-decoration: none;
    box-shadow: var(--shadow-hover);
    transform: translateY(-2px);
  }
  .proj-head { display: flex; align-items: baseline; gap: var(--s3); }
  .proj-slug {
    font-size: var(--text-xs);
    color: var(--vermilion);
    border: 1px solid currentColor;
    border-radius: 2px;
    padding: 0.05rem 0.3rem;
    letter-spacing: 0.1em;
  }
  .proj-name { font-family: var(--font-display); font-size: var(--text-md); font-weight: 600; }
  .proj-envs { display: flex; gap: var(--s2); margin: var(--s3) 0; flex-wrap: wrap; }
  .proj-foot { border-top: 1px dashed var(--rule); padding-top: var(--s2); }

  .empty-card {
    grid-column: 1 / -1;
    padding: var(--s6);
    text-align: center;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--s3);
    color: var(--ink-soft);
  }

  /* ── activity ledger ────────────────────────── */
  .activity { padding-left: var(--s5); }
  .activity ol { list-style: none; }
  .activity li {
    padding: var(--s2) 0;
    border-top: 1px solid var(--rule-faint);
    position: relative;
  }
  .ev-line1 { display: flex; justify-content: space-between; align-items: baseline; }
  .ev-action { font-size: var(--text-xs); font-weight: 600; color: var(--archivist); }
  .activity li.denied .ev-action { color: var(--vermilion); }
  .ev-line2 {
    display: flex;
    gap: var(--s2);
    font-size: var(--text-xs);
    color: var(--ink-faint);
    overflow: hidden;
  }
  .ev-actor { font-weight: 620; color: var(--ink-soft); flex: none; }
  .ev-res { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .denied-stamp {
    position: absolute;
    right: 0; top: 50%;
    translate: 0 -50%;
    transform: rotate(-8deg);
    font-size: 0.55rem;
    padding: 0.1rem 0.35rem;
  }

  @media (max-width: 980px) {
    .columns { grid-template-columns: 1fr; }
    .stat-strip { grid-template-columns: repeat(2, 1fr); row-gap: var(--s4); }
    .proj-grid { grid-template-columns: 1fr; }
  }
</style>
