<script lang="ts">
  import { session } from '../lib/session.svelte'
  import { api, downloadBackup, errorMessage, type VersionInfo, type MasterKeyStatus } from '../lib/api'
  import { dialog } from '../lib/dialog.svelte'
  import { relTime } from '../lib/util'

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

  $effect(() => {
    api.version().then(v => (version = v)).catch(() => (version = null))
    void loadMk()
  })

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
    { k: 'Seal', v: session.sealType === 'shamir' ? `shamir · ${session.threshold}-of-${session.totalShares}` : 'awskms auto-unseal' },
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
    <table class="ledger">
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
</style>
