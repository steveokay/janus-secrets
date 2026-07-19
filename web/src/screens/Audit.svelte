<script lang="ts">
  import { api, errorMessage, type ApiAuditEvent, type VerifyResult } from '../lib/api'
  import { relTime, clockTime, shortDate } from '../lib/util'

  let events = $state<ApiAuditEvent[]>([])
  let nextCursor = $state<number | null>(null)
  let verify = $state<VerifyResult | null>(null)
  let resultFilter = $state<'all' | 'success' | 'denied'>('all')
  let query = $state(new URLSearchParams(window.location.search).get('q') ?? '')
  let verifying = $state(false)
  let loading = $state(true)
  let error = $state('')

  $effect(() => {
    // reload whenever the server-side result filter changes
    void loadEvents(resultFilter)
  })

  $effect(() => {
    void runVerify()
  })

  async function loadEvents(rf: string, cursor?: number) {
    loading = true
    error = ''
    try {
      const params: Record<string, string | number> = { limit: 50 }
      if (rf !== 'all') params.result = rf
      if (cursor !== undefined) params.cursor = cursor
      const res = await api.listAuditEvents(params)
      events = cursor !== undefined ? [...events, ...res.events] : res.events
      nextCursor = res.next_cursor
    } catch (err) {
      error = errorMessage(err, 'Could not read the ledger.')
    } finally {
      loading = false
    }
  }

  async function runVerify() {
    verifying = true
    try {
      verify = await api.verifyAudit()
    } catch {
      verify = null
    } finally {
      verifying = false
    }
  }

  const filtered = $derived(
    events.filter(e =>
      (e.actor_name + e.action + e.resource).toLowerCase().includes(query.toLowerCase()),
    ),
  )
</script>

<div class="audit">
  <header class="page-head rise">
    <div>
      <p class="folio">The record · append-only · hash-chained · fail-closed</p>
      <h1>Audit ledger</h1>
    </div>
    <div class="head-right">
      {#if verifying}
        <span class="verifying mono">walking the chain…</span>
      {:else if verify?.valid}
        <span class="stamp ok">Chain verified · {verify.count.toLocaleString()} events</span>
      {:else if verify}
        <span class="stamp">Chain broken at №{verify.head_seq}</span>
      {/if}
      <button class="btn btn-sm" onclick={runVerify} disabled={verifying}>Re-verify</button>
      <a class="btn btn-sm" href={api.auditExportUrl('jsonl')} download>Export JSONL</a>
      <a class="btn btn-sm" href={api.auditExportUrl('csv')} download>Export CSV</a>
    </div>
  </header>
  <hr class="ledger-rule" />

  <div class="toolbar rise" style="animation-delay: 50ms">
    <input class="input search" placeholder="Filter by actor, action, resource…" bind:value={query} />
    <div class="seg" role="group" aria-label="Filter by result">
      {#each ['all', 'success', 'denied'] as f}
        <button class="seg-btn" class:on={resultFilter === f} onclick={() => (resultFilter = f as typeof resultFilter)}>
          {f === 'success' ? 'ok' : f}
        </button>
      {/each}
    </div>
  </div>

  {#if error}<p class="error rise">{error}</p>{/if}

  <div class="sheet table-wrap rise" style="animation-delay: 90ms">
    <table class="ledger">
      <thead>
        <tr>
          <th style="width: 80px">Entry</th>
          <th style="width: 110px">When</th>
          <th style="width: 150px">Actor</th>
          <th style="width: 170px">Action</th>
          <th>Resource</th>
          <th style="width: 110px">Origin IP</th>
          <th style="width: 170px">Chain</th>
        </tr>
      </thead>
      <tbody>
        {#each filtered as ev (ev.seq)}
          <tr class:denied={ev.result === 'denied'}>
            <td class="num seq">№ {ev.seq.toLocaleString()}</td>
            <td class="num when">
              <span class="d">{shortDate(ev.occurred_at)}</span> {clockTime(ev.occurred_at)}
              <span class="folio rel">{relTime(ev.occurred_at)}</span>
            </td>
            <td>
              <span class="actor">{ev.actor_name}</span>
              <span class="folio kind">{ev.actor_kind}</span>
            </td>
            <td>
              <span class="action mono" class:bad={ev.result !== 'success'}>{ev.action}</span>
              {#if ev.result === 'denied'}
                <span class="stamp flat mini-stamp">Denied</span>
              {:else if ev.result === 'error'}
                <span class="stamp flat mini-stamp">Error</span>
              {/if}
            </td>
            <td class="res mono">{ev.resource}{#if ev.detail}<span class="detail"> · {ev.detail}</span>{/if}</td>
            <td class="num ip">{ev.ip || '—'}</td>
            <td class="chain-cell">
              <span class="chain mono" title={`prev ${ev.prev_hash} → ${ev.hash}`}>
                <svg class="link-glyph" width="10" height="10" viewBox="0 0 10 10" fill="none" aria-hidden="true">
                  <circle cx="3.4" cy="5" r="2.4" stroke="currentColor" stroke-width="1.1"/>
                  <circle cx="6.6" cy="5" r="2.4" stroke="currentColor" stroke-width="1.1"/>
                </svg>{ev.hash.slice(0, 12)}
              </span>
            </td>
          </tr>
        {:else}
          <tr><td colspan="7" class="empty folio">{loading ? 'Reading the ledger…' : 'No events match.'}</td></tr>
        {/each}
      </tbody>
    </table>
    {#if nextCursor !== null}
      <div class="more">
        <button class="btn btn-sm" onclick={() => loadEvents(resultFilter, nextCursor!)} disabled={loading}>
          {loading ? 'Reading…' : 'Older entries'}
        </button>
      </div>
    {/if}
  </div>

  <p class="foot-note folio">
    Each entry stores the SHA-256 of the previous entry. An event has no value field by
    construction — a secret can never enter this ledger. Audit write failure fails the request.
  </p>
</div>

<style>
  .audit { max-width: 1280px; margin: 0 auto; }
  .page-head { display: flex; justify-content: space-between; align-items: flex-end; gap: var(--s4); }
  .page-head h1 { margin-top: var(--s1); }
  .head-right { display: flex; align-items: center; gap: var(--s3); flex-wrap: wrap; }
  .verifying { font-size: var(--text-xs); color: var(--archivist); animation: blink-caret 1s step-end infinite; }
  .error { color: var(--vermilion); font-size: var(--text-sm); margin-top: var(--s3); }

  .toolbar { display: flex; justify-content: space-between; gap: var(--s3); margin: var(--s5) 0 var(--s3); }
  .search { max-width: 340px; }

  .seg { display: flex; border: 1px solid var(--rule-strong); border-radius: var(--radius); overflow: hidden; }
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

  .table-wrap { overflow-x: auto; }

  .seq { color: var(--ink-faint); font-size: var(--text-xs); white-space: nowrap; }
  .when { white-space: nowrap; font-size: var(--text-xs); }
  .when .d { color: var(--ink-faint); }
  .when .rel { display: block; }
  .actor { display: block; font-weight: 620; font-size: var(--text-sm); word-break: break-all; }
  .kind { font-size: 0.62rem; }
  .action { font-size: var(--text-xs); font-weight: 600; color: var(--archivist); }
  .action.bad { color: var(--vermilion); }
  .mini-stamp { font-size: 0.5rem; padding: 0.06rem 0.3rem; margin-left: 0.4rem; transform: rotate(-6deg); display: inline-block; }
  .res { font-size: var(--text-xs); color: var(--ink-soft); max-width: 320px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .detail { color: var(--ink-faint); }
  .ip { font-size: var(--text-xs); color: var(--ink-faint); }

  .chain-cell { position: relative; }
  .chain {
    font-size: 0.65rem;
    color: var(--verdigris);
    letter-spacing: 0.04em;
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
  }
  .link-glyph { opacity: 0.75; flex: none; }
  /* the stitch — a thread connecting each entry's hash to the next */
  tbody tr:not(:last-child) .chain-cell::after {
    content: '';
    position: absolute;
    left: 1.35rem;
    bottom: -1px;
    height: 12px;
    border-left: 1px dashed var(--verdigris);
    opacity: 0.5;
    transform: translateY(50%);
  }

  tr.denied td { background: var(--vermilion-wash); }

  .more { padding: var(--s3); text-align: center; }
  .empty { text-align: center; padding: var(--s6) !important; }
  .foot-note { margin-top: var(--s3); max-width: 70ch; }
</style>
