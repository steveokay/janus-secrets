<script lang="ts">
  import { session } from '../lib/session.svelte'
  import { api, downloadBackup, errorMessage, sealTypeLabel, type VersionInfo, type MasterKeyStatus, type SessionInfo, type SysStatus } from '../lib/api'
  import { dialog } from '../lib/dialog.svelte'
  import { relTime } from '../lib/util'
  import { renderSVG } from 'uqr'

  let version = $state<VersionInfo | null>(null)
  let mk = $state<MasterKeyStatus | null>(null)
  let note = $state('')

  /* password change */
  let curPw = $state('')
  let newPw = $state('')
  let pwError = $state('')
  let pwOk = $state(false)

  /* rekey ceremony */
  let rekeyNonce = $state('')
  let rekeyShare = $state('')
  let rekeyProgress = $state<{ submitted: number; required: number } | null>(null)
  let newShares = $state<string[] | null>(null)
  let rekeyError = $state('')

  /* active sessions */
  let sessions = $state<SessionInfo[] | null>(null)
  let sessError = $state('')

  /* instance health */
  let health = $state<SysStatus | null>(null)
  let healthError = $state('')
  let healthLoading = $state(false)

  /* two-factor (TOTP) */
  type TotpStatus = { enabled: boolean; recovery_remaining: number }
  let totp = $state<TotpStatus | null>(null)
  let totpError = $state('')
  let totpBusy = $state(false)
  /* enrollment (shown once, component state only) */
  let enroll = $state<{ secret: string; otpauth_url: string } | null>(null)
  let confirmCode = $state('')
  /* QR of the otpauth URI, rendered locally (the secret never leaves the
     browser); colours are theme-invariant tokens so it always scans. */
  let enrollQr = $derived(
    enroll
      ? renderSVG(enroll.otpauth_url, { blackColor: 'var(--qr-ink)', whiteColor: 'var(--qr-paper)', border: 2 })
      : '',
  )
  /* recovery codes (shown once, component state only) */
  let recoveryCodes = $state<string[] | null>(null)
  /* regenerate flow */
  let regenOpen = $state(false)
  let regenCode = $state('')

  $effect(() => {
    api.version().then(v => (version = v)).catch(() => (version = null))
    void loadMk()
    void loadSessions()
    void loadTotp()
    void loadHealth()
  })

  async function loadHealth() {
    healthError = ''
    healthLoading = true
    try {
      health = await api.sysStatus()
    } catch (err) {
      healthError = errorMessage(err, 'Could not load health.')
      health = null
    } finally {
      healthLoading = false
    }
  }

  /* Humanize a duration in seconds → e.g. "2d 3h", "5m", "just now". */
  function humanizeSeconds(s: number | null | undefined): string {
    if (s === null || s === undefined) return '—'
    if (s < 1) return 'just now'
    const d = Math.floor(s / 86400)
    const h = Math.floor((s % 86400) / 3600)
    const m = Math.floor((s % 3600) / 60)
    const sec = Math.floor(s % 60)
    if (d > 0) return h > 0 ? `${d}d ${h}h` : `${d}d`
    if (h > 0) return m > 0 ? `${h}h ${m}m` : `${h}h`
    if (m > 0) return sec > 0 ? `${m}m ${sec}s` : `${m}m`
    return `${sec}s`
  }

  /* A scheduler is stale when it has ticked but the last tick is older than
     ~3× its interval — only meaningful when enabled and it has ticked. */
  function schedulerStale(sc: { enabled: boolean; last_tick_age_seconds: number | null; interval_seconds: number }): boolean {
    return sc.enabled && sc.last_tick_age_seconds !== null && sc.interval_seconds > 0 &&
      sc.last_tick_age_seconds > sc.interval_seconds * 3
  }

  const schedulerRows = $derived(
    health
      ? ([
          ['rotation', health.schedulers.rotation],
          ['sync', health.schedulers.sync],
          ['dynamic', health.schedulers.dynamic],
        ] as const)
      : [],
  )

  async function loadTotp() {
    totpError = ''
    try {
      totp = await api.totpStatus()
    } catch (err) {
      totpError = errorMessage(err, 'Could not load two-factor status.')
      totp = null
    }
  }

  async function startEnroll() {
    totpError = ''
    recoveryCodes = null
    confirmCode = ''
    totpBusy = true
    try {
      enroll = await api.totpEnroll()
    } catch (err) {
      totpError = errorMessage(err, 'Could not begin enrollment.')
    } finally {
      totpBusy = false
    }
  }

  function cancelEnroll() {
    enroll = null
    confirmCode = ''
    totpError = ''
  }

  async function confirmEnroll(e: SubmitEvent) {
    e.preventDefault()
    totpError = ''
    totpBusy = true
    try {
      const res = await api.totpConfirm(confirmCode.trim())
      enroll = null
      confirmCode = ''
      recoveryCodes = res.recovery_codes
      await loadTotp()
    } catch (err) {
      totpError = errorMessage(err, 'That code was not accepted.')
    } finally {
      totpBusy = false
    }
  }

  async function regenSubmit(e: SubmitEvent) {
    e.preventDefault()
    totpError = ''
    totpBusy = true
    try {
      const res = await api.totpRegenerateRecovery(regenCode.trim())
      regenOpen = false
      regenCode = ''
      recoveryCodes = res.recovery_codes
      await loadTotp()
    } catch (err) {
      totpError = errorMessage(err, 'That code was not accepted.')
    } finally {
      totpBusy = false
    }
  }

  async function disableTotp() {
    totpError = ''
    const code = await dialog.prompt({
      title: 'Disable two-factor?',
      body: 'Enter a current authenticator code (or a recovery code) to confirm. Your account will no longer require a second factor.',
      label: 'Current code',
      placeholder: '123456 or a recovery code',
      confirmLabel: 'Disable two-factor',
      danger: true,
    })
    if (code === null) return
    const trimmed = code.trim()
    if (!trimmed) {
      totpError = 'A current code is required to disable two-factor.'
      return
    }
    totpBusy = true
    try {
      await api.totpDisable(trimmed)
      flash('Two-factor disabled.')
      recoveryCodes = null
      await loadTotp()
    } catch (err) {
      totpError = errorMessage(err, 'Could not disable two-factor.')
    } finally {
      totpBusy = false
    }
  }

  function copyRecovery() {
    if (!recoveryCodes) return
    navigator.clipboard.writeText(recoveryCodes.join('\n'))
    flash('Recovery codes copied.')
  }

  async function loadSessions() {
    sessError = ''
    try {
      sessions = await api.listSessions()
    } catch (err) {
      sessError = errorMessage(err, 'Could not load sessions.')
      sessions = []
    }
  }

  /* A short human label for a session's device from its user-agent. Best-effort,
     display-only — the raw string stays available in the title attribute. */
  function deviceLabel(ua: string): string {
    if (!ua) return 'Unknown device'
    if (/janus-cli|Go-http-client/i.test(ua)) return 'CLI / API client'
    let os = ''
    if (/Windows/i.test(ua)) os = 'Windows'
    else if (/Mac OS X|Macintosh/i.test(ua)) os = 'macOS'
    else if (/Android/i.test(ua)) os = 'Android'
    else if (/iPhone|iPad|iOS/i.test(ua)) os = 'iOS'
    else if (/Linux/i.test(ua)) os = 'Linux'
    let br = ''
    if (/Edg\//i.test(ua)) br = 'Edge'
    else if (/Chrome\//i.test(ua)) br = 'Chrome'
    else if (/Firefox\//i.test(ua)) br = 'Firefox'
    else if (/Safari\//i.test(ua)) br = 'Safari'
    return [br, os].filter(Boolean).join(' · ') || 'Browser'
  }

  async function revokeSession(s: SessionInfo) {
    const ok = await dialog.confirm({
      title: 'Revoke this session?',
      body: 'That device will be signed out immediately.',
      confirmLabel: 'Revoke',
      danger: true,
    })
    if (!ok) return
    try {
      await api.revokeSession(s.id)
      flash('Session revoked.')
      await loadSessions()
    } catch (err) {
      flash(errorMessage(err, 'Revoke failed.'))
    }
  }

  async function revokeOthers() {
    const others = (sessions ?? []).filter(s => !s.current).length
    const ok = await dialog.confirm({
      title: 'Sign out everywhere else?',
      body: `Every session except this one will be revoked${others ? ` (${others})` : ''}.`,
      confirmLabel: 'Revoke all others',
      danger: true,
    })
    if (!ok) return
    try {
      const { revoked } = await api.revokeOtherSessions()
      flash(revoked === 1 ? '1 other session revoked.' : `${revoked} other sessions revoked.`)
      await loadSessions()
    } catch (err) {
      flash(errorMessage(err, 'Revoke failed.'))
    }
  }

  async function loadMk() {
    mk = await api.masterKeyStatus().catch(() => null)
  }

  function flash(msg: string) {
    note = msg
    setTimeout(() => (note = ''), 3200)
  }

  async function changePassword(e: SubmitEvent) {
    e.preventDefault()
    pwError = ''
    pwOk = false
    try {
      await api.changePassword(curPw, newPw)
      pwOk = true
      curPw = ''
      newPw = ''
    } catch (err) {
      pwError = errorMessage(err, 'Password change failed.')
    }
  }

  async function backup() {
    try {
      await downloadBackup()
      flash('Backup downloaded — sealed material only.')
    } catch (err) {
      flash(errorMessage(err, 'Backup failed (requires sys:backup).'))
    }
  }

  async function rotateMk() {
    const ok = await dialog.confirm({
      title: 'Rotate the master key?',
      body: 'All project KEKs are re-wrapped online; secrets stay available throughout.',
      confirmLabel: 'Rotate',
    })
    if (!ok) return
    try {
      const res = await api.rotateMasterKey()
      flash(`Master key rotated — now v${res.master_key_version}.`)
      await loadMk()
    } catch (err) {
      flash(errorMessage(err, 'Rotation failed (owner only).'))
    }
  }

  async function rekeyStart() {
    rekeyError = ''
    newShares = null
    try {
      const res = await api.rekeyInit()
      rekeyNonce = res.nonce
      rekeyProgress = { submitted: res.submitted, required: res.required }
    } catch (err) {
      rekeyError = errorMessage(err, 'Could not start the rekey.')
    }
  }

  async function rekeySubmit(e: SubmitEvent) {
    e.preventDefault()
    rekeyError = ''
    try {
      const res = await api.rekeySubmit(rekeyNonce, rekeyShare.trim())
      rekeyShare = ''
      if (res.complete) {
        newShares = res.new_shares ?? []
        rekeyNonce = ''
        rekeyProgress = null
        await loadMk()
      } else {
        rekeyProgress = { submitted: res.submitted ?? 0, required: res.required ?? 0 }
      }
    } catch (err) {
      rekeyError = errorMessage(err, 'Share rejected.')
    }
  }

  async function rekeyAbort() {
    try {
      await api.rekeyCancel()
      rekeyNonce = ''
      rekeyProgress = null
      rekeyError = ''
      await loadMk()
    } catch { /* already gone */ }
  }

  const rows = $derived([
    { k: 'Seal', v: session.sealType === 'shamir' ? `shamir · ${session.threshold}-of-${session.totalShares}` : `${sealTypeLabel(session.sealType)} auto-unseal` },
    { k: 'Server version', v: version ? `janus ${version.version}${version.commit ? ` · ${version.commit.slice(0, 8)}` : ''}` : '—' },
    { k: 'Signed in as', v: `${session.me?.name ?? '—'} (${session.me?.kind ?? ''})` },
    { k: 'Master key', v: mk ? `v${mk.master_key_version} · rotated ${mk.rotated_at ? relTime(mk.rotated_at) : 'never'}` : '—' },
    { k: 'Audit retention', v: 'unlimited (append-only)' },
    { k: 'Configuration', v: 'env-only — JANUS_* variables on the server process' },
  ])
</script>

<div class="page-n">
  <header class="page-head rise">
    <div>
      <p class="folio">Office · instance, keys &amp; account</p>
      <h1>Settings</h1>
    </div>
    {#if note}<span class="pill pill-info">{note}</span>{/if}
  </header>
  <hr class="ledger-rule" />

  <div class="sheet panel rise" style="animation-delay: 40ms">
    <table class="ledger" aria-label="Instance settings">
      <tbody>
        {#each rows as r}
          <tr>
            <td class="mono k">{r.k}</td>
            <td class="mono v">{r.v}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>

  <section class="op-section rise" style="animation-delay: 70ms">
    <div class="section-head">
      <h3>Health</h3>
      <div class="head-right">
        <span class="folio">instance, database, audit &amp; scheduler status · read-only</span>
        <button class="btn btn-ghost btn-sm" onclick={loadHealth} disabled={healthLoading}>
          {healthLoading ? 'Refreshing…' : 'Refresh'}
        </button>
      </div>
    </div>
    <div class="sheet card">
      {#if healthError}
        <p class="error">{healthError}</p>
      {:else if health === null}
        <p class="folio">Loading…</p>
      {:else}
        <div class="health-grid" class:dim={healthLoading}>
          <!-- Instance -->
          <div class="hgroup">
            <h4 class="folio">Instance</h4>
            <dl class="kvs">
              <div class="kv-row">
                <dt>Version</dt>
                <dd class="mono">janus {health.version}{#if health.commit} · {health.commit.slice(0, 8)}{/if}</dd>
              </div>
              <div class="kv-row">
                <dt>Uptime</dt>
                <dd>{humanizeSeconds(health.uptime_seconds)}</dd>
              </div>
              <div class="kv-row">
                <dt>Seal</dt>
                <dd>
                  {#if health.sealed}
                    <span class="stamp warn flat">sealed</span>
                  {:else}
                    <span class="stamp ok flat">unsealed</span>
                  {/if}
                  <span class="folio">· {health.seal_type}</span>
                </dd>
              </div>
            </dl>
          </div>

          <!-- Database -->
          <div class="hgroup">
            <h4 class="folio">Database</h4>
            <dl class="kvs">
              <div class="kv-row">
                <dt>Reachable</dt>
                <dd>
                  {#if health.db.reachable}
                    <span class="stamp ok flat">yes</span>
                  {:else}
                    <span class="stamp warn flat">unreachable</span>
                  {/if}
                </dd>
              </div>
              {#if health.db.reachable}
                <div class="kv-row">
                  <dt>Latency</dt>
                  <dd class="mono">{health.db.latency_ms} ms</dd>
                </div>
                <div class="kv-row">
                  <dt>Pool</dt>
                  <dd class="mono">{health.db.pool.acquired}/{health.db.pool.idle}/{health.db.pool.total} <span class="folio">(max {health.db.pool.max})</span></dd>
                </div>
              {:else}
                <div class="kv-row full">
                  <dd class="warn-line">Database is not reachable — pool and latency figures may be stale.</dd>
                </div>
              {/if}
            </dl>
          </div>

          <!-- Audit -->
          <div class="hgroup">
            <h4 class="folio">Audit</h4>
            <dl class="kvs">
              <div class="kv-row">
                <dt>Head seq</dt>
                <dd class="mono">{health.audit.head_seq}</dd>
              </div>
              <div class="kv-row">
                <dt>Events</dt>
                <dd class="mono">{health.audit.event_count}</dd>
              </div>
            </dl>
          </div>

          <!-- Failures / leases -->
          <div class="hgroup">
            <h4 class="folio">Failures &amp; leases</h4>
            <dl class="kvs">
              <div class="kv-row">
                <dt>Rotation failed</dt>
                <dd class="mono" class:bad={health.runs.rotation_failed > 0}>{health.runs.rotation_failed}</dd>
              </div>
              <div class="kv-row">
                <dt>Sync failed</dt>
                <dd class="mono" class:bad={health.runs.sync_failed > 0}>{health.runs.sync_failed}</dd>
              </div>
              <div class="kv-row">
                <dt>Active leases</dt>
                <dd class="mono">{health.leases.active}</dd>
              </div>
            </dl>
          </div>

          <!-- Schedulers -->
          <div class="hgroup schedulers">
            <h4 class="folio">Schedulers</h4>
            <table class="ledger sched" aria-label="Background scheduler engines">
              <thead>
                <tr><th scope="col">Engine</th><th scope="col">State</th><th scope="col">Last tick</th><th scope="col">Interval</th></tr>
              </thead>
              <tbody>
                {#each schedulerRows as [name, sc] (name)}
                  <tr class:stale={schedulerStale(sc)}>
                    <td class="eng">{name}</td>
                    <td>
                      {#if sc.enabled}
                        <span class="pill pill-info">enabled</span>
                      {:else}
                        <span class="pill">disabled</span>
                      {/if}
                    </td>
                    <td class="mono">
                      {sc.last_tick_age_seconds === null ? 'never' : `${humanizeSeconds(sc.last_tick_age_seconds)} ago`}
                      {#if schedulerStale(sc)}<span class="stamp warn flat">stale</span>{/if}
                    </td>
                    <td class="mono">{sc.interval_seconds > 0 ? humanizeSeconds(sc.interval_seconds) : '—'}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        </div>
      {/if}
    </div>
  </section>

  <section class="op-section rise" style="animation-delay: 90ms">
    <div class="section-head">
      <h3>Master key</h3>
      <span class="folio">rotate re-wraps project KEKs online · rekey re-splits the Shamir shares</span>
    </div>
    <div class="sheet card">
      <div class="row">
        <button class="btn" onclick={rotateMk}>Rotate master key</button>
        {#if session.sealType === 'shamir' && !rekeyNonce && !newShares}
          <button class="btn" onclick={rekeyStart}>Rekey shares…</button>
        {/if}
        <button class="btn" onclick={backup}>Download backup</button>
      </div>

      {#if rekeyError}<p class="error">{rekeyError}</p>{/if}

      {#if rekeyNonce}
        <form class="rekey" onsubmit={rekeySubmit}>
          <span class="label">Rekey in progress — present current shares ({rekeyProgress?.submitted ?? 0}/{rekeyProgress?.required ?? '?'})</span>
          <div class="rekey-line">
            <input class="field-ruled" type="password" bind:value={rekeyShare} placeholder="current key share" />
            <button class="btn btn-primary btn-sm" type="submit" disabled={!rekeyShare.trim()}>Present</button>
            <button class="btn btn-ghost btn-sm" type="button" onclick={rekeyAbort}>Abort</button>
          </div>
        </form>
      {/if}

      {#if newShares}
        <div class="new-shares">
          <span class="stamp ok flat">Rekeyed — new shares, shown exactly once</span>
          <ol>
            {#each newShares as sh, i}
              <li><span class="folio">Share {i + 1}</span><code class="mono">{sh}</code></li>
            {/each}
          </ol>
          <button class="btn btn-sm" onclick={() => (newShares = null)}>I have stored them — dismiss</button>
        </div>
      {/if}
    </div>
  </section>

  <section class="op-section rise" style="animation-delay: 140ms">
    <div class="section-head"><h3>Account</h3></div>
    <div class="sheet card">
      <form class="pw-form" onsubmit={changePassword}>
        <label class="field"><span class="label">Current passphrase</span>
          <input class="input mono" type="password" bind:value={curPw} autocomplete="current-password" required /></label>
        <label class="field"><span class="label">New passphrase</span>
          <input class="input mono" type="password" bind:value={newPw} autocomplete="new-password" required minlength="12" /></label>
        <button class="btn btn-stamp" type="submit" disabled={!curPw || newPw.length < 12}>Change passphrase</button>
        {#if pwError}<p class="error">{pwError}</p>{/if}
        {#if pwOk}<p class="ok-note">Passphrase changed.</p>{/if}
      </form>
    </div>
  </section>

  <section class="op-section rise" style="animation-delay: 190ms">
    <div class="section-head">
      <h3>Active sessions</h3>
      <span class="folio">your signed-in devices · revoke anything you don't recognize</span>
    </div>
    <div class="sheet card">
      {#if sessError}
        <p class="error">{sessError}</p>
      {:else if sessions === null}
        <p class="folio">Loading…</p>
      {:else if sessions.length === 0}
        <p class="folio">No active sessions.</p>
      {:else}
        <table class="ledger sessions" aria-label="Active sessions">
          <thead>
            <tr>
              <th scope="col">Device</th>
              <th scope="col">IP</th>
              <th scope="col">Last seen</th>
              <th scope="col"></th>
            </tr>
          </thead>
          <tbody>
            {#each sessions as s (s.id)}
              <tr>
                <td>
                  <span class="dev" title={s.user_agent || ''}>{deviceLabel(s.user_agent)}</span>
                  {#if s.current}<span class="pill pill-info">this device</span>{/if}
                </td>
                <td class="mono ip">{s.ip || '—'}</td>
                <td class="mono when" title={s.last_seen_at}>{relTime(s.last_seen_at)}</td>
                <td class="act">
                  {#if !s.current}
                    <button class="btn btn-ghost btn-sm" onclick={() => revokeSession(s)}>Revoke</button>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
        {#if sessions.some(s => !s.current)}
          <div class="row" style="margin-top: var(--s4)">
            <button class="btn" onclick={revokeOthers}>Sign out all other sessions</button>
          </div>
        {/if}
      {/if}
    </div>
  </section>

  <section class="op-section rise" style="animation-delay: 240ms">
    <div class="section-head">
      <h3>Two-factor authentication</h3>
      <span class="folio">a time-based code from your authenticator app, in addition to your passphrase</span>
    </div>
    <div class="sheet card">
      {#if totp === null}
        <p class="folio">Loading…</p>
      {:else if recoveryCodes}
        <!-- Recovery codes are shown exactly once, on this device only. -->
        <div class="new-shares">
          <span class="stamp ok flat">Recovery codes — shown exactly once</span>
          <p class="folio">Store these somewhere safe now. Each code can be used once if you lose your authenticator. They will not be shown again.</p>
          <ol class="codes">
            {#each recoveryCodes as rc, i}
              <li><span class="folio">{i + 1}</span><code class="mono">{rc}</code></li>
            {/each}
          </ol>
          <div class="row">
            <button class="btn btn-sm" onclick={copyRecovery}>Copy all</button>
            <button class="btn btn-sm" onclick={() => (recoveryCodes = null)}>I have stored them — dismiss</button>
          </div>
        </div>
      {:else if enroll}
        <!-- Secret + otpauth URI shown once, in component state only. -->
        <div class="enroll">
          <p class="folio">Scan this with your authenticator app — or paste the setup link / type the secret manually. Then enter the 6-digit code it shows to finish.</p>
          <!-- QR rendered locally from the otpauth URI; the secret never leaves the browser. -->
          <div class="qr" aria-label="TOTP enrolment QR code" role="img">{@html enrollQr}</div>
          <div class="kv">
            <span class="label">Secret</span>
            <div class="mono-line">
              <code class="mono">{enroll.secret}</code>
              <button class="btn btn-ghost btn-sm" type="button"
                onclick={() => { navigator.clipboard.writeText(enroll!.secret); flash('Secret copied.') }}>Copy</button>
            </div>
          </div>
          <div class="kv">
            <span class="label">Setup link</span>
            <div class="mono-line">
              <code class="mono uri">{enroll.otpauth_url}</code>
              <button class="btn btn-ghost btn-sm" type="button"
                onclick={() => { navigator.clipboard.writeText(enroll!.otpauth_url); flash('Setup link copied.') }}>Copy</button>
            </div>
          </div>
          <form class="code-form" onsubmit={confirmEnroll}>
            <label class="field"><span class="label" id="totp-confirm-lbl">Verification code</span>
              <input class="field-ruled mono" bind:value={confirmCode} aria-labelledby="totp-confirm-lbl"
                placeholder="123456" autocomplete="one-time-code" inputmode="numeric"
                autocapitalize="off" spellcheck="false" /></label>
            <button class="btn btn-primary btn-sm" type="submit" disabled={totpBusy || !confirmCode.trim()}>
              {totpBusy ? 'Confirming…' : 'Confirm & enable'}
            </button>
            <button class="btn btn-ghost btn-sm" type="button" onclick={cancelEnroll} disabled={totpBusy}>Cancel</button>
          </form>
        </div>
      {:else if totp.enabled}
        <div class="tfa-enabled">
          <div class="status-line">
            <span class="stamp ok flat">Enabled</span>
            <span class="folio {totp.recovery_remaining <= 2 ? 'low' : ''}">
              {totp.recovery_remaining} recovery code{totp.recovery_remaining === 1 ? '' : 's'} remaining
              {#if totp.recovery_remaining <= 2}— regenerate soon{/if}
            </span>
          </div>
          {#if regenOpen}
            <form class="code-form" onsubmit={regenSubmit}>
              <label class="field"><span class="label" id="totp-regen-lbl">Current code to regenerate</span>
                <input class="field-ruled mono" bind:value={regenCode} aria-labelledby="totp-regen-lbl"
                  placeholder="123456 or a recovery code" autocomplete="one-time-code" inputmode="numeric"
                  autocapitalize="off" spellcheck="false" /></label>
              <button class="btn btn-primary btn-sm" type="submit" disabled={totpBusy || !regenCode.trim()}>
                {totpBusy ? 'Working…' : 'Regenerate'}
              </button>
              <button class="btn btn-ghost btn-sm" type="button"
                onclick={() => { regenOpen = false; regenCode = ''; totpError = '' }} disabled={totpBusy}>Cancel</button>
            </form>
          {:else}
            <div class="row">
              <button class="btn" onclick={() => { regenOpen = true; regenCode = ''; totpError = '' }} disabled={totpBusy}>Regenerate recovery codes</button>
              <button class="btn btn-stamp" onclick={disableTotp} disabled={totpBusy}>Disable two-factor</button>
            </div>
          {/if}
        </div>
      {:else}
        <div class="tfa-disabled">
          <p class="folio">Two-factor is not enabled. Protect your account with a code from an authenticator app.</p>
          <button class="btn btn-primary" onclick={startEnroll} disabled={totpBusy}>
            {totpBusy ? 'Preparing…' : 'Enable two-factor'}
          </button>
        </div>
      {/if}

      {#if totpError}<p class="error">{totpError}</p>{/if}
    </div>
  </section>
</div>

<style>
  .page-n { max-width: 900px; margin: 0 auto; }
  .page-head { display: flex; justify-content: space-between; align-items: flex-end; gap: var(--s4); }
  .page-head h1 { margin-top: var(--s1); }
  .panel { margin-top: var(--s5); }
  .k { color: var(--ink-faint); font-size: var(--text-xs); width: 260px; }
  .v { font-size: var(--text-sm); }

  .op-section { margin-top: var(--s6); }
  .section-head { display: flex; justify-content: space-between; align-items: baseline; margin-bottom: var(--s3); flex-wrap: wrap; }
  .head-right { display: flex; align-items: baseline; gap: var(--s3); flex-wrap: wrap; }
  .card { padding: var(--s4) var(--s5); }
  .row { display: flex; gap: var(--s3); flex-wrap: wrap; }
  .error { color: var(--vermilion); font-size: var(--text-sm); margin-top: var(--s3); }
  .ok-note { color: var(--verdigris); font-size: var(--text-sm); }

  .rekey { margin-top: var(--s4); display: flex; flex-direction: column; gap: var(--s2); }
  .rekey-line { display: flex; gap: var(--s3); align-items: center; }
  .rekey-line .field-ruled { max-width: 380px; }

  .new-shares { margin-top: var(--s4); display: flex; flex-direction: column; gap: var(--s3); align-items: flex-start; }
  .new-shares ol { list-style: none; width: 100%; border-top: 1px solid var(--rule); }
  .new-shares li {
    display: grid;
    grid-template-columns: 90px 1fr;
    gap: var(--s3);
    padding: var(--s2) 0;
    border-bottom: 1px solid var(--rule-faint);
  }
  .new-shares code { font-size: var(--text-xs); word-break: break-all; }

  .pw-form { display: flex; align-items: flex-end; gap: var(--s4); flex-wrap: wrap; }
  .field { display: flex; flex-direction: column; gap: var(--s1); min-width: 220px; }

  .sessions { width: 100%; }
  .sessions thead th {
    text-align: left;
    font-size: var(--text-xs);
    color: var(--ink-faint);
    font-weight: 600;
    padding-bottom: var(--s2);
    border-bottom: 1px solid var(--rule);
  }
  .sessions tbody td { padding: var(--s2) 0; border-bottom: 1px solid var(--rule-faint); vertical-align: middle; }
  .sessions .dev { font-size: var(--text-sm); margin-right: var(--s2); }
  .sessions .ip, .sessions .when { font-size: var(--text-xs); color: var(--ink-faint); }
  .sessions .act { text-align: right; width: 1%; white-space: nowrap; }

  /* two-factor */
  .tfa-enabled, .tfa-disabled, .enroll { display: flex; flex-direction: column; gap: var(--s3); align-items: flex-start; }
  .status-line { display: flex; align-items: baseline; gap: var(--s3); flex-wrap: wrap; }
  .status-line .low { color: var(--vermilion); }
  .qr {
    align-self: flex-start;
    width: 168px;
    height: 168px;
    border: 1px solid var(--rule);
    border-radius: var(--radius-sm, 4px);
    background: var(--qr-paper);
    padding: var(--s2);
  }
  .qr :global(svg) { display: block; width: 100%; height: 100%; }
  .kv { display: flex; flex-direction: column; gap: var(--s1); width: 100%; }
  .mono-line { display: flex; align-items: center; gap: var(--s3); flex-wrap: wrap; }
  .mono-line code { font-size: var(--text-xs); word-break: break-all; }
  .mono-line .uri { max-width: 100%; }
  .code-form { display: flex; align-items: flex-end; gap: var(--s3); flex-wrap: wrap; margin-top: var(--s2); }
  .code-form .field-ruled { max-width: 220px; }
  .codes { list-style: none; width: 100%; border-top: 1px solid var(--rule); }
  .codes li {
    display: grid;
    grid-template-columns: 40px 1fr;
    gap: var(--s3);
    align-items: center;
    padding: var(--s2) 0;
    border-bottom: 1px solid var(--rule-faint);
  }
  .codes code { font-size: var(--text-xs); word-break: break-all; }

  /* health panel */
  .health-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
    gap: var(--s5) var(--s6);
    transition: opacity var(--t-fast);
  }
  .health-grid.dim { opacity: 0.55; }
  .hgroup h4 { margin: 0 0 var(--s2); letter-spacing: var(--track-caps); }
  .hgroup.schedulers { grid-column: 1 / -1; }
  .kvs { display: flex; flex-direction: column; gap: 0; }
  .kv-row {
    display: grid;
    grid-template-columns: 130px 1fr;
    gap: var(--s3);
    align-items: baseline;
    padding: var(--s1) 0;
    border-bottom: 1px solid var(--rule-faint);
  }
  .kv-row.full { grid-template-columns: 1fr; }
  .kv-row dt { color: var(--ink-faint); font-size: var(--text-xs); }
  .kv-row dd { font-size: var(--text-sm); margin: 0; }
  .kv-row dd .folio { font-size: var(--text-xs); }
  .kv-row dd.bad { color: var(--vermilion); font-weight: 650; }
  .warn-line { color: var(--vermilion); font-size: var(--text-sm); }
  /* warning-accented stamp (default .stamp colour is already vermilion) */
  .stamp.warn { color: var(--vermilion); }

  .sched { width: 100%; margin-top: var(--s1); }
  .sched thead th {
    text-align: left;
    font-size: var(--text-xs);
    color: var(--ink-faint);
    font-weight: 600;
    padding: 0 var(--s3) var(--s2) 0;
    border-bottom: 1px solid var(--rule);
  }
  .sched tbody td { padding: var(--s2) var(--s3) var(--s2) 0; border-bottom: 1px solid var(--rule-faint); vertical-align: middle; }
  .sched .eng { text-transform: capitalize; font-size: var(--text-sm); }
  .sched .stamp { margin-left: var(--s2); }
  .sched tr.stale td { color: var(--vermilion); }
</style>
