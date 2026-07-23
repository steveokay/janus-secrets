<script lang="ts">
  import {
    api, errorMessage,
    type NotificationChannel, type NotificationDelivery, type NotificationEventKind,
    type NotificationChannelType, type SmtpTlsMode,
  } from '../lib/api'
  import { dialog } from '../lib/dialog.svelte'
  import { relTime } from '../lib/util'

  const ALL_EVENTS: Array<{ kind: NotificationEventKind; label: string }> = [
    { kind: 'rotation.failed', label: 'Rotation failed' },
    { kind: 'sync.failed', label: 'Sync failed' },
    { kind: 'promotion.pending', label: 'Promotion awaiting approval' },
    { kind: 'access.denied', label: 'Access denied' },
    { kind: 'breakglass.activated', label: 'Break-glass activated' },
  ]

  let channels = $state<NotificationChannel[]>([])
  let loading = $state(true)
  let error = $state('')
  let note = $state('')

  /* create/edit form */
  let editing = $state<NotificationChannel | null>(null)
  let showForm = $state(false)
  let fName = $state('')
  let fType = $state<NotificationChannelType>('webhook')
  let fUrl = $state('')
  let fHmac = $state('')
  /* smtp fields */
  let fSmtpHost = $state('')
  let fSmtpPort = $state('587')
  let fSmtpFrom = $state('')
  let fSmtpTo = $state('')          // comma/space-separated, split into string[] on submit
  let fSmtpUser = $state('')
  let fSmtpPass = $state('')        // write-only, send-only, never populated from server
  let fSmtpTls = $state<SmtpTlsMode>('starttls')
  let fSmtpSkipVerify = $state(false)
  let fEvents = $state<Set<NotificationEventKind>>(new Set())
  let fError = $state('')
  let saving = $state(false)

  /* split a comma/space/newline-separated recipient string into trimmed addresses */
  function parseRecipients(s: string): string[] {
    return s.split(/[\s,;]+/).map((a) => a.trim()).filter((a) => a !== '')
  }

  /* deliveries drawer */
  let drawerFor = $state<NotificationChannel | null>(null)
  let deliveries = $state<NotificationDelivery[]>([])
  let drawerLoading = $state(false)

  $effect(() => { void load() })

  async function load() {
    loading = true
    error = ''
    try {
      channels = await api.listChannels()
    } catch (err) {
      error = errorMessage(err, 'Could not list channels (needs notification:manage).')
    } finally {
      loading = false
    }
  }

  function flash(msg: string) {
    note = msg
    setTimeout(() => (note = ''), 3000)
  }

  function resetSmtp() {
    fSmtpHost = ''; fSmtpPort = '587'; fSmtpFrom = ''; fSmtpTo = ''
    fSmtpUser = ''; fSmtpPass = ''; fSmtpTls = 'starttls'; fSmtpSkipVerify = false
  }

  function openCreate() {
    editing = null
    fName = ''; fType = 'webhook'; fUrl = ''; fHmac = ''; fEvents = new Set(); fError = ''
    resetSmtp()
    showForm = true
  }

  function openEdit(c: NotificationChannel) {
    editing = c
    fName = c.name; fType = c.type; fUrl = ''; fHmac = ''
    fEvents = new Set(c.events); fError = ''
    /* prefill non-secret SMTP settings; password stays blank (write-only) */
    resetSmtp()
    fSmtpHost = c.smtp_host ?? ''
    fSmtpPort = c.smtp_port ? String(c.smtp_port) : '587'
    fSmtpFrom = c.smtp_from ?? ''
    fSmtpTo = (c.smtp_to ?? []).join(', ')
    fSmtpUser = c.smtp_username ?? ''
    fSmtpTls = c.smtp_tls_mode ?? 'starttls'
    fSmtpSkipVerify = c.smtp_insecure_skip_verify ?? false
    showForm = true
  }

  function toggleEvent(k: NotificationEventKind) {
    const next = new Set(fEvents)
    if (next.has(k)) next.delete(k); else next.add(k)
    fEvents = next
  }

  /* validate SMTP inputs; returns an error string or '' when valid */
  function validateSmtp(): string {
    if (fSmtpHost.trim() === '') return 'SMTP host is required.'
    const port = Number(fSmtpPort)
    if (!Number.isInteger(port) || port < 1 || port > 65535) return 'SMTP port must be a whole number between 1 and 65535.'
    if (fSmtpFrom.trim() === '') return 'A From address is required.'
    if (parseRecipients(fSmtpTo).length === 0) return 'At least one recipient (To) is required.'
    return ''
  }

  /* build the smtp_* payload shared by create and update */
  function smtpPayload() {
    return {
      smtp_host: fSmtpHost.trim(),
      smtp_port: Number(fSmtpPort),
      smtp_from: fSmtpFrom.trim(),
      smtp_to: parseRecipients(fSmtpTo),
      ...(fSmtpUser.trim() ? { smtp_username: fSmtpUser.trim() } : {}),
      ...(fSmtpPass ? { smtp_password: fSmtpPass } : {}),
      smtp_tls_mode: fSmtpTls,
      smtp_insecure_skip_verify: fSmtpSkipVerify,
    }
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault()
    fError = ''
    const smtp = fType === 'smtp'
    if (smtp) {
      const v = validateSmtp()
      if (v) { fError = v; return }
    }
    saving = true
    try {
      if (editing) {
        await api.updateChannel(editing.id, {
          events: [...fEvents],
          ...(smtp
            ? smtpPayload()
            : fUrl.trim() ? { url: fUrl.trim(), hmac_key: fHmac } : {}),
        })
        flash('Channel updated.')
      } else {
        await api.createChannel({
          name: fName.trim(), type: fType,
          url: smtp ? '' : fUrl.trim(),
          ...(fType === 'webhook' && fHmac ? { hmac_key: fHmac } : {}),
          ...(smtp ? smtpPayload() : {}),
          events: [...fEvents],
        })
        flash('Channel created.')
      }
      showForm = false
      await load()
    } catch (err) {
      fError = errorMessage(err, 'Save failed.')
    } finally {
      saving = false
    }
  }

  async function toggleEnabled(c: NotificationChannel) {
    try {
      await api.updateChannel(c.id, { enabled: !c.enabled })
      await load()
    } catch (err) {
      flash(errorMessage(err, 'Update failed.'))
    }
  }

  async function test(c: NotificationChannel) {
    try {
      await api.testChannel(c.id)
      flash(`Test delivered to ${c.name}.`)
    } catch (err) {
      flash(errorMessage(err, 'Test delivery failed.'))
    }
  }

  async function remove(c: NotificationChannel) {
    const ok = await dialog.confirm({
      title: `Delete ${c.name}?`,
      body: 'This channel and its queued deliveries are removed. This cannot be undone.',
      confirmLabel: 'Delete',
      danger: true,
    })
    if (!ok) return
    try {
      await api.deleteChannel(c.id)
      await load()
    } catch (err) {
      flash(errorMessage(err, 'Delete failed.'))
    }
  }

  async function openDrawer(c: NotificationChannel) {
    drawerFor = c
    drawerLoading = true
    try {
      deliveries = await api.listDeliveries(c.id)
    } catch (err) {
      flash(errorMessage(err, 'Could not load deliveries.'))
      deliveries = []
    } finally {
      drawerLoading = false
    }
  }

  const smtpReady = $derived(
    fSmtpHost.trim() !== '' &&
    fSmtpFrom.trim() !== '' &&
    parseRecipients(fSmtpTo).length > 0 &&
    Number.isInteger(Number(fSmtpPort)) && Number(fSmtpPort) >= 1 && Number(fSmtpPort) <= 65535,
  )

  const destReady = $derived(
    fType === 'smtp' ? smtpReady : (editing ? true : fUrl.trim() !== ''),
  )

  const canSubmit = $derived(
    (editing ? true : fName.trim() !== '') && destReady && fEvents.size > 0,
  )
</script>

<div class="page-n">
  <header class="page-head rise">
    <div>
      <p class="folio">Office · outbound alerting · failures find humans, never a secret value</p>
      <h1>Notifications</h1>
    </div>
    <div class="head-actions">
      {#if note}<span class="pill pill-info">{note}</span>{/if}
      <button class="btn btn-primary" onclick={openCreate}>+ New channel</button>
    </div>
  </header>
  <hr class="ledger-rule" />

  {#if error}<p class="error rise">{error}</p>{/if}

  <div class="sheet table-wrap rise" style="animation-delay: 60ms">
    <table class="ledger">
      <thead>
        <tr>
          <th>Channel</th>
          <th style="width: 90px">Type</th>
          <th>Events</th>
          <th style="width: 90px">Status</th>
          <th style="width: 220px"></th>
        </tr>
      </thead>
      <tbody>
        {#each channels as c (c.id)}
          <tr>
            <td><span class="c-name">{c.name}</span><span class="folio">by {c.created_by}</span></td>
            <td><span class="pill pill-neutral">{c.type}</span></td>
            <td class="events">
              {#each c.events as e}<span class="pill pill-info ev">{e}</span>{/each}
            </td>
            <td>
              <button class="pill {c.enabled ? 'pill-dev' : 'pill-neutral'} toggle" onclick={() => toggleEnabled(c)} title="Toggle enabled">
                {c.enabled ? 'enabled' : 'disabled'}
              </button>
            </td>
            <td class="row-actions">
              <button class="btn btn-ghost btn-sm" onclick={() => test(c)}>Test</button>
              <button class="btn btn-ghost btn-sm" onclick={() => openDrawer(c)}>History</button>
              <button class="btn btn-ghost btn-sm" onclick={() => openEdit(c)}>Edit</button>
              <button class="btn btn-ghost btn-sm danger" onclick={() => remove(c)}>Delete</button>
            </td>
          </tr>
        {:else}
          <tr><td colspan="5" class="empty folio">{loading ? 'Reading…' : 'No channels yet — create one to route rotation/sync failures, denials, or pending approvals to a webhook or Slack.'}</td></tr>
        {/each}
      </tbody>
    </table>
  </div>

  <p class="foot-note folio">
    The destination URL and webhook HMAC key are write-only — Janus stores them
    envelope-encrypted and never returns them. Notifications are rendered from the
    audit log, which has no value field, so they can never carry a secret value.
  </p>
</div>

{#if showForm}
  <div class="scrim" onclick={() => (showForm = false)} role="presentation"></div>
  <aside class="drawer sheet" aria-label="channel form">
    <h2>{editing ? `Edit ${editing.name}` : 'New channel'}</h2>
    <form onsubmit={submit}>
      {#if !editing}
        <div class="field">
          <label class="label" for="n-name">Name</label>
          <input id="n-name" class="input" bind:value={fName} placeholder="ops-alerts" required />
        </div>
        <div class="field">
          <label class="label" for="n-type">Type</label>
          <select id="n-type" class="select" bind:value={fType}>
            <option value="webhook">webhook (signed JSON POST)</option>
            <option value="slack">slack (incoming webhook)</option>
            <option value="smtp">smtp (email)</option>
          </select>
        </div>
      {/if}

      {#if fType === 'smtp'}
        <div class="field-row">
          <div class="field grow">
            <label class="label" for="n-smtp-host">SMTP host</label>
            <input id="n-smtp-host" class="input mono" bind:value={fSmtpHost} placeholder="smtp.example.com" autocomplete="off" />
          </div>
          <div class="field port">
            <label class="label" for="n-smtp-port">Port</label>
            <input id="n-smtp-port" class="input mono" type="number" min="1" max="65535" bind:value={fSmtpPort} placeholder="587" />
          </div>
        </div>
        <div class="field">
          <label class="label" for="n-smtp-from">From address</label>
          <input id="n-smtp-from" class="input mono" bind:value={fSmtpFrom} placeholder="janus@example.com" autocomplete="off" />
        </div>
        <div class="field">
          <label class="label" for="n-smtp-to">To <span class="folio">(one or more, comma or space separated)</span></label>
          <input id="n-smtp-to" class="input mono" bind:value={fSmtpTo} placeholder="oncall@example.com, ops@example.com" autocomplete="off" />
        </div>
        <div class="field">
          <label class="label" for="n-smtp-user">Username <span class="folio">(optional; auth requires TLS)</span></label>
          <input id="n-smtp-user" class="input mono" bind:value={fSmtpUser} placeholder="apikey" autocomplete="off" />
        </div>
        <div class="field">
          <label class="label" for="n-smtp-pass">Password <span class="folio">(write-only{#if editing}; blank = keep current{/if})</span></label>
          <input id="n-smtp-pass" class="input mono" type="password" bind:value={fSmtpPass} placeholder="write-only" autocomplete="new-password" />
        </div>
        <div class="field">
          <label class="label" for="n-smtp-tls">TLS mode</label>
          <select id="n-smtp-tls" class="select" bind:value={fSmtpTls}>
            <option value="starttls">STARTTLS (upgrade, e.g. port 587)</option>
            <option value="implicit">Implicit TLS (e.g. port 465)</option>
            <option value="none">None (plaintext, no auth)</option>
          </select>
        </div>
        <div class="field">
          <label class="check">
            <input type="checkbox" bind:checked={fSmtpSkipVerify} />
            <span>Allow insecure TLS (skip cert verification)</span>
          </label>
          <p class="warn-note folio">For self-signed / internal relays only — disabling verification exposes delivery to interception. Leave off unless you trust the network.</p>
        </div>
      {:else}
        <div class="field">
          <label class="label" for="n-url">Destination URL {#if editing}<span class="folio">(blank = keep current)</span>{/if}</label>
          <input id="n-url" class="input mono" bind:value={fUrl} placeholder="https://hooks.example.com/…" required={!editing} />
        </div>
        {#if fType === 'webhook'}
          <div class="field">
            <label class="label" for="n-hmac">HMAC signing key <span class="folio">(optional; X-Janus-Signature)</span></label>
            <input id="n-hmac" class="input mono" type="password" bind:value={fHmac} placeholder="write-only" autocomplete="off" />
          </div>
        {/if}
      {/if}
      <div class="field">
        <span class="label">Subscribe to</span>
        <div class="events-pick">
          {#each ALL_EVENTS as ev}
            <label class="check">
              <input type="checkbox" checked={fEvents.has(ev.kind)} onchange={() => toggleEvent(ev.kind)} />
              <span>{ev.label} <code class="mono ek">{ev.kind}</code></span>
            </label>
          {/each}
        </div>
      </div>
      {#if fError}<p class="error">{fError}</p>{/if}
      <div class="form-actions">
        <button class="btn btn-stamp" type="submit" disabled={!canSubmit || saving}>{editing ? 'Save' : 'Create'}</button>
        <button class="btn btn-ghost" type="button" onclick={() => (showForm = false)}>Cancel</button>
      </div>
    </form>
  </aside>
{/if}

{#if drawerFor}
  <div class="scrim" onclick={() => (drawerFor = null)} role="presentation"></div>
  <aside class="drawer sheet" aria-label="delivery history">
    <h2>{drawerFor.name} — deliveries</h2>
    {#if drawerLoading}
      <p class="folio">Reading…</p>
    {:else if deliveries.length === 0}
      <p class="folio">No deliveries yet.</p>
    {:else}
      <table class="ledger">
        <thead><tr><th>Event</th><th>Status</th><th>When</th></tr></thead>
        <tbody>
          {#each deliveries as d (d.id)}
            <tr>
              <td class="mono ek">{d.event_kind}</td>
              <td>
                {#if d.status === 'delivered'}<span class="pill pill-dev">delivered</span>
                {:else if d.status === 'failed'}<span class="pill pill-prod">failed</span>
                {:else}<span class="pill pill-staging">pending ({d.attempts})</span>{/if}
                {#if d.last_error}<span class="folio err-line">{d.last_error}</span>{/if}
              </td>
              <td class="folio">{relTime(d.delivered_at ?? d.created_at)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
    <div class="form-actions">
      <button class="btn btn-ghost" onclick={() => (drawerFor = null)}>Close</button>
    </div>
  </aside>
{/if}

<style>
  .page-n { max-width: 1200px; margin: 0 auto; }
  .page-head { display: flex; justify-content: space-between; align-items: flex-end; gap: var(--s4); }
  .page-head h1 { margin-top: var(--s1); }
  .head-actions { display: flex; align-items: center; gap: var(--s3); }
  .error { color: var(--vermilion); font-size: var(--text-sm); }

  .table-wrap { overflow-x: auto; margin-top: var(--s5); }
  .c-name { display: block; font-weight: 620; }
  .events { display: flex; flex-wrap: wrap; gap: var(--s1); }
  .ev { font-size: var(--text-2xs, var(--text-xs)); }
  .ek { font-size: var(--text-xs); color: var(--ink-faint); }
  .toggle { cursor: pointer; border: none; }
  .row-actions { text-align: right; white-space: nowrap; }
  .danger:hover { color: var(--vermilion); }
  .empty { text-align: center; padding: var(--s6) !important; }
  .foot-note { margin-top: var(--s3); max-width: 78ch; }

  .scrim { position: fixed; inset: 0; background: rgba(0,0,0,0.35); z-index: 40; }
  .drawer {
    position: fixed; top: 0; right: 0; bottom: 0; width: min(460px, 92vw);
    z-index: 41; padding: var(--s5); overflow-y: auto;
    display: flex; flex-direction: column; gap: var(--s4);
    border-left: 4px solid var(--verdigris);
  }
  .drawer h2 { margin: 0; }
  .drawer form { display: flex; flex-direction: column; gap: var(--s4); }
  .field { display: flex; flex-direction: column; gap: var(--s2); }
  .field-row { display: flex; gap: var(--s3); }
  .field-row .grow { flex: 1; }
  .field-row .port { width: 96px; flex: none; }
  .warn-note { color: var(--ochre); max-width: 46ch; }
  .events-pick { display: flex; flex-direction: column; gap: var(--s2); }
  .check { display: flex; align-items: center; gap: var(--s2); font-size: var(--text-sm); cursor: pointer; }
  .form-actions { display: flex; gap: var(--s3); margin-top: var(--s2); }
  .err-line { display: block; color: var(--vermilion); }
</style>
