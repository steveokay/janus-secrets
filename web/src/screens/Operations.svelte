<script lang="ts">
  import {
    api, errorMessage,
    type RotationPolicy, type RotationCreateInput, type SyncTargetApi, type SyncCreateInput, type DynamicRole, type ApiLease,
    type RunView, type IssuedCreds,
  } from '../lib/api'
  import { registry } from '../lib/registry.svelte'
  import { listAllRotations, listAllSyncs, listAllDynamicRoles, listAllLeases } from '../lib/ops'
  import { dialog } from '../lib/dialog.svelte'
  import { relTime } from '../lib/util'

  let rotations = $state<RotationPolicy[]>([])
  let syncs = $state<SyncTargetApi[]>([])
  let roles = $state<DynamicRole[]>([])
  let leases = $state<ApiLease[]>([])
  let loading = $state(true)
  let error = $state('')
  let note = $state('')

  /* run history expansion */
  let runsFor = $state<string | null>(null)
  let runs = $state<RunView[]>([])

  /* issued credentials — the one shown-once plaintext */
  let issued = $state<IssuedCreds | null>(null)

  /* create forms */
  let showNewRotation = $state(false)
  let rKey = $state('')
  let rConfigId = $state('')
  let rType = $state<'postgres' | 'webhook' | 'mysql' | 'redis'>('postgres')
  let rIntervalDays = $state(30)
  let rAdminDsn = $state('')
  let rRole = $state('')
  let rUrl = $state('')
  let rHmac = $state('')
  // mysql
  let rMyAddr = $state('')
  let rMyAdminUser = $state('')
  let rMyAdminPassword = $state('')
  let rMyTls = $state('')
  let rMyUser = $state('')
  let rMyHost = $state('')
  // redis
  let rRdAddr = $state('')
  let rRdAdminUser = $state('')
  let rRdAdminPassword = $state('')
  let rRdTls = $state(false)
  let rRdSkipVerify = $state(false)
  let rRdUser = $state('')
  let rRdRules = $state('')
  let rError = $state('')

  let showNewSync = $state(false)
  let sConfigId = $state('')
  let sProvider = $state<'github' | 'k8s' | 'gitlab' | 'aws_ssm' | 'cloudflare' | 'aws_secrets' | 'vercel' | 'netlify'>('github')
  let sIntervalMin = $state(15)
  let sOwner = $state('')
  let sRepo = $state('')
  let sEnvName = $state('')
  let sPat = $state('')
  let sApiUrl = $state('')
  let sNamespace = $state('')
  let sSecretName = $state('')
  let sToken = $state('')
  let sCaCert = $state('')
  // gitlab
  let sGitlabUrl = $state('')
  let sGlProject = $state('')
  let sGlEnvScope = $state('')
  let sGlToken = $state('')
  // aws_ssm
  let sAwsRegion = $state('')
  let sAwsPathPrefix = $state('')
  let sAwsAccessKeyId = $state('')
  let sAwsSecretAccessKey = $state('')
  let sAwsSessionToken = $state('')
  // cloudflare
  let sCfAccountId = $state('')
  let sCfScriptName = $state('')
  let sCfApiToken = $state('')
  // aws_secrets
  let sSmRegion = $state('')
  let sSmPathPrefix = $state('')
  let sSmAccessKeyId = $state('')
  let sSmSecretAccessKey = $state('')
  let sSmSessionToken = $state('')
  // vercel
  let sVcProject = $state('')
  let sVcTeamId = $state('')
  let sVcTarget = $state<'production' | 'preview' | 'development'>('production')
  let sVcApiToken = $state('')
  // netlify
  let sNfAccountId = $state('')
  let sNfSiteId = $state('')
  let sNfApiToken = $state('')
  let sError = $state('')

  let showNewRole = $state(false)
  let dConfigId = $state('')
  let dName = $state('')
  let dTtlMin = $state(60)
  let dMaxTtlMin = $state(720)
  let dAdminDsn = $state('')
  let dCreate = $state("CREATE ROLE \"{{name}}\" WITH LOGIN PASSWORD '{{password}}' VALID UNTIL '{{expiration}}';\nGRANT SELECT ON ALL TABLES IN SCHEMA public TO \"{{name}}\";")
  let dRevoke = $state('DROP ROLE IF EXISTS "{{name}}";')
  let dError = $state('')

  $effect(() => {
    // the ops lists are keyed by project/config — wait for the registry tree
    if (registry.loaded) void load()
  })

  async function load() {
    loading = true
    error = ''
    try {
      ;[rotations, syncs, roles] = await Promise.all([
        listAllRotations(registry.projects),
        listAllSyncs(registry.projects),
        listAllDynamicRoles(registry.projects),
      ])
      leases = await listAllLeases(roles)
    } catch (err) {
      error = errorMessage(err, 'Could not load operations.')
    } finally {
      loading = false
    }
  }

  function flash(msg: string) {
    note = msg
    setTimeout(() => (note = ''), 3200)
  }

  const configOptions = $derived(
    registry.projects.flatMap(p => p.environments.flatMap(e => e.configs.map(c => ({ id: c.id, label: `${p.name} / ${e.slug} / ${c.name}` })))),
  )

  /* ── rotation ─────────────────────────────── */

  function rotationConfig(): RotationCreateInput['config'] {
    switch (rType) {
      case 'postgres':
        return { admin_dsn: rAdminDsn, role: rRole.trim() }
      case 'webhook':
        return { url: rUrl.trim(), hmac_key: rHmac }
      case 'mysql':
        return {
          admin_dsn: rAdminDsn || undefined, mysql_addr: rMyAddr.trim() || undefined,
          mysql_admin_user: rMyAdminUser.trim() || undefined, mysql_admin_password: rMyAdminPassword || undefined,
          mysql_tls: rMyTls || undefined, mysql_user: rMyUser.trim(), mysql_host: rMyHost.trim() || undefined,
        }
      case 'redis':
        return {
          redis_addr: rRdAddr.trim(), redis_admin_user: rRdAdminUser.trim() || undefined,
          redis_admin_password: rRdAdminPassword || undefined, redis_tls: rRdTls,
          redis_skip_verify: rRdSkipVerify, redis_user: rRdUser.trim(), redis_rules: rRdRules.trim() || undefined,
        }
    }
  }

  async function createRotation(e: SubmitEvent) {
    e.preventDefault()
    rError = ''
    try {
      await api.createRotationPolicy({
        config_id: rConfigId,
        secret_key: rKey.trim().toUpperCase(),
        type: rType,
        interval_seconds: rIntervalDays * 86400,
        config: rotationConfig(),
      })
      showNewRotation = false
      rKey = ''; rAdminDsn = ''; rRole = ''; rUrl = ''; rHmac = ''
      rMyAddr = ''; rMyAdminUser = ''; rMyAdminPassword = ''; rMyTls = ''; rMyUser = ''; rMyHost = ''
      rRdAddr = ''; rRdAdminUser = ''; rRdAdminPassword = ''; rRdTls = false; rRdSkipVerify = false; rRdUser = ''; rRdRules = ''
      flash('Rotation policy created.')
      await load()
    } catch (err) {
      rError = errorMessage(err, 'Could not create the policy.')
    }
  }

  async function rotateNow(r: RotationPolicy) {
    try {
      await api.rotateNow(r.id)
      flash(`Rotated ${r.secret_key}.`)
      await load()
    } catch (err) {
      flash(errorMessage(err, 'Rotation failed.'))
    }
  }

  async function toggleRotation(r: RotationPolicy) {
    try {
      await api.setRotationStatus(r.id, r.status === 'paused' ? 'active' : 'paused')
      await load()
    } catch (err) {
      flash(errorMessage(err, 'Status change failed.'))
    }
  }

  async function removeRotation(r: RotationPolicy) {
    const ok = await dialog.confirm({
      title: `Delete the rotation policy for ${r.secret_key}?`,
      body: 'The secret keeps its current value; it just stops rotating.',
      confirmLabel: 'Delete policy',
      danger: true,
    })
    if (!ok) return
    try {
      await api.deleteRotationPolicy(r.id)
      await load()
    } catch (err) {
      flash(errorMessage(err, 'Delete failed.'))
    }
  }

  async function toggleRuns(kind: 'rot' | 'sync', id: string) {
    const key = `${kind}:${id}`
    if (runsFor === key) {
      runsFor = null
      return
    }
    runsFor = key
    runs = []
    try {
      runs = kind === 'rot' ? await api.rotationRuns(id) : await api.syncRuns(id)
    } catch (err) {
      flash(errorMessage(err, 'Could not load runs.'))
      runsFor = null
    }
  }

  /* ── sync ─────────────────────────────────── */

  async function createSync(e: SubmitEvent) {
    e.preventDefault()
    sError = ''
    try {
      let addr: SyncCreateInput['addr']
      let creds: SyncCreateInput['creds']
      if (sProvider === 'github') {
        addr = { owner: sOwner.trim(), repo: sRepo.trim(), ...(sEnvName.trim() ? { environment: sEnvName.trim() } : {}) }
        creds = { pat: sPat }
      } else if (sProvider === 'k8s') {
        addr = { namespace: sNamespace.trim(), secret_name: sSecretName.trim() }
        creds = { api_url: sApiUrl.trim(), token: sToken, ca_cert: sCaCert }
      } else if (sProvider === 'gitlab') {
        addr = {
          project: sGlProject.trim(),
          ...(sGitlabUrl.trim() ? { gitlab_url: sGitlabUrl.trim() } : {}),
          ...(sGlEnvScope.trim() ? { environment_scope: sGlEnvScope.trim() } : {}),
        }
        creds = { token: sGlToken }
      } else if (sProvider === 'aws_ssm') {
        addr = { region: sAwsRegion.trim(), path_prefix: sAwsPathPrefix.trim() }
        creds = {
          access_key_id: sAwsAccessKeyId.trim(),
          secret_access_key: sAwsSecretAccessKey,
          ...(sAwsSessionToken.trim() ? { session_token: sAwsSessionToken.trim() } : {}),
        }
      } else if (sProvider === 'cloudflare') {
        addr = { account_id: sCfAccountId.trim(), script_name: sCfScriptName.trim() }
        creds = { api_token: sCfApiToken }
      } else if (sProvider === 'vercel') {
        addr = {
          vercel_project: sVcProject.trim(),
          ...(sVcTeamId.trim() ? { vercel_team_id: sVcTeamId.trim() } : {}),
          vercel_targets: [sVcTarget],
        }
        creds = { api_token: sVcApiToken }
      } else if (sProvider === 'netlify') {
        addr = {
          netlify_account_id: sNfAccountId.trim(),
          ...(sNfSiteId.trim() ? { netlify_site_id: sNfSiteId.trim() } : {}),
        }
        creds = { api_token: sNfApiToken }
      } else {
        addr = { region: sSmRegion.trim(), path_prefix: sSmPathPrefix.trim() }
        creds = {
          access_key_id: sSmAccessKeyId.trim(),
          secret_access_key: sSmSecretAccessKey,
          ...(sSmSessionToken.trim() ? { session_token: sSmSessionToken.trim() } : {}),
        }
      }
      await api.createSyncTarget({
        config_id: sConfigId,
        provider: sProvider,
        interval_seconds: sIntervalMin * 60,
        addr,
        creds,
      })
      showNewSync = false
      sOwner = ''; sRepo = ''; sEnvName = ''; sPat = ''; sApiUrl = ''; sNamespace = ''; sSecretName = ''; sToken = ''; sCaCert = ''
      sGitlabUrl = ''; sGlProject = ''; sGlEnvScope = ''; sGlToken = ''
      sAwsRegion = ''; sAwsPathPrefix = ''; sAwsAccessKeyId = ''; sAwsSecretAccessKey = ''; sAwsSessionToken = ''
      sCfAccountId = ''; sCfScriptName = ''; sCfApiToken = ''
      sSmRegion = ''; sSmPathPrefix = ''; sSmAccessKeyId = ''; sSmSecretAccessKey = ''; sSmSessionToken = ''
      sVcProject = ''; sVcTeamId = ''; sVcTarget = 'production'; sVcApiToken = ''
      sNfAccountId = ''; sNfSiteId = ''; sNfApiToken = ''
      flash('Sync target created.')
      await load()
    } catch (err) {
      sError = errorMessage(err, 'Could not create the target.')
    }
  }

  async function syncNow(s: SyncTargetApi) {
    try {
      await api.syncNow(s.id)
      flash('Sync pushed.')
      await load()
    } catch (err) {
      flash(errorMessage(err, 'Sync failed.'))
    }
  }

  async function toggleSync(s: SyncTargetApi) {
    try {
      await api.setSyncStatus(s.id, s.status === 'paused' ? 'active' : 'paused')
      await load()
    } catch (err) {
      flash(errorMessage(err, 'Status change failed.'))
    }
  }

  async function removeSync(s: SyncTargetApi) {
    const ok = await dialog.confirm({
      title: 'Delete this sync target?',
      body: 'Already-pushed secrets remain at the destination; they just stop updating.',
      confirmLabel: 'Delete target',
      danger: true,
    })
    if (!ok) return
    try {
      await api.deleteSyncTarget(s.id)
      await load()
    } catch (err) {
      flash(errorMessage(err, 'Delete failed.'))
    }
  }

  /* ── dynamic ──────────────────────────────── */

  async function createRole(e: SubmitEvent) {
    e.preventDefault()
    dError = ''
    try {
      await api.createDynamicRole({
        config_id: dConfigId,
        name: dName.trim(),
        default_ttl_seconds: dTtlMin * 60,
        max_ttl_seconds: dMaxTtlMin * 60,
        config: { admin_dsn: dAdminDsn, creation_statements: dCreate, revocation_statements: dRevoke },
      })
      showNewRole = false
      dName = ''; dAdminDsn = ''
      flash('Dynamic role created.')
      await load()
    } catch (err) {
      dError = errorMessage(err, 'Could not create the role.')
    }
  }

  async function issue(role: DynamicRole) {
    try {
      issued = await api.issueCreds(role.id)
      await load()
    } catch (err) {
      flash(errorMessage(err, 'Issue failed.'))
    }
  }

  async function removeRole(role: DynamicRole) {
    const ok = await dialog.confirm({
      title: `Delete dynamic role ${role.name}?`,
      body: 'Active leases are revoked and their database credentials dropped.',
      confirmLabel: 'Delete role',
      danger: true,
    })
    if (!ok) return
    try {
      await api.deleteDynamicRole(role.id)
      await load()
    } catch (err) {
      flash(errorMessage(err, 'Delete failed.'))
    }
  }

  async function renew(l: ApiLease) {
    try {
      await api.renewLease(l.id)
      flash('Lease renewed.')
      await load()
    } catch (err) {
      flash(errorMessage(err, 'Renew failed.'))
    }
  }

  async function revoke(l: ApiLease) {
    try {
      await api.revokeLease(l.id)
      flash('Lease revoked.')
      await load()
    } catch (err) {
      flash(errorMessage(err, 'Revoke failed.'))
    }
  }

  function destLabel(s: SyncTargetApi): string {
    if (s.provider === 'github') return `gh:${s.addr.owner}/${s.addr.repo}${s.addr.environment ? ` · ${s.addr.environment}` : ''}`
    if (s.provider === 'gitlab') return `gl:${s.addr.project}${s.addr.environment_scope ? ` · ${s.addr.environment_scope}` : ''}`
    if (s.provider === 'aws_ssm') return `ssm:${s.addr.region}${s.addr.path_prefix ?? ''}`
    if (s.provider === 'cloudflare') return `cf:${s.addr.account_id}/${s.addr.script_name}`
    if (s.provider === 'aws_secrets') return `sm:${s.addr.region}/${s.addr.path_prefix ?? ''}`
    if (s.provider === 'vercel') return `vc:${s.addr.vercel_project}${s.addr.vercel_team_id ? ` · ${s.addr.vercel_team_id}` : ''}`
    if (s.provider === 'netlify') return `nf:${s.addr.netlify_account_id}${s.addr.netlify_site_id ? `/${s.addr.netlify_site_id}` : ''}`
    return `k8s:${s.addr.namespace}/${s.addr.secret_name}`
  }

  function providerLabel(p: string): string {
    if (p === 'github') return 'GitHub'
    if (p === 'gitlab') return 'GitLab'
    if (p === 'aws_ssm') return 'AWS SSM'
    if (p === 'cloudflare') return 'Cloudflare'
    if (p === 'aws_secrets') return 'AWS Secrets'
    if (p === 'vercel') return 'Vercel'
    if (p === 'netlify') return 'Netlify'
    return 'K8s'
  }

  function roleName(id: string): string {
    return roles.find(r => r.id === id)?.name ?? id.slice(0, 12)
  }

  function ttlPct(l: ApiLease): number {
    const total = new Date(l.max_expires_at).getTime() - new Date(l.created_at).getTime()
    const used = Date.now() - new Date(l.created_at).getTime()
    return Math.max(0, Math.min(100, Math.round((used / Math.max(total, 1)) * 100)))
  }

  const interval = (secs: number) =>
    secs % 86400 === 0 ? `every ${secs / 86400}d` : secs % 3600 === 0 ? `every ${secs / 3600}h` : `every ${Math.round(secs / 60)}m`
</script>

<div class="page-n">
  <header class="page-head rise">
    <div>
      <p class="folio">Instruments · the moving parts — rotation, sync, dynamic credentials</p>
      <h1>Operations</h1>
    </div>
    {#if note}<span class="pill pill-info">{note}</span>{/if}
  </header>
  <hr class="ledger-rule" />

  {#if error}<p class="error rise">{error}</p>{/if}

  <!-- ══ rotation ══ -->
  <section class="op-section rise" style="animation-delay: 50ms">
    <div class="section-head">
      <h3>Static rotation</h3>
      <button class="btn btn-sm" onclick={() => (showNewRotation = !showNewRotation)}>+ New policy</button>
    </div>

    {#if showNewRotation}
      <form class="sheet create-form" onsubmit={createRotation}>
        <label class="field"><span class="label">Config</span>
          <select class="select" bind:value={rConfigId} required>
            <option value="" disabled>choose…</option>
            {#each configOptions as o}<option value={o.id}>{o.label}</option>{/each}
          </select>
        </label>
        <label class="field"><span class="label">Secret key</span>
          <input class="input mono" bind:value={rKey} placeholder="DATABASE_URL" required style="text-transform: uppercase" /></label>
        <label class="field"><span class="label">Rotator</span>
          <select class="select" bind:value={rType}><option value="postgres">postgres</option><option value="webhook">webhook</option><option value="mysql">mysql</option><option value="redis">redis</option></select></label>
        <label class="field"><span class="label">Every (days)</span>
          <input class="input" type="number" min="1" bind:value={rIntervalDays} /></label>
        {#if rType === 'postgres'}
          <label class="field grow"><span class="label">Admin DSN (write-only)</span>
            <input class="input mono" type="password" bind:value={rAdminDsn} placeholder="postgres://admin:…@db:5432/app" required /></label>
          <label class="field"><span class="label">DB role to rotate</span>
            <input class="input mono" bind:value={rRole} placeholder="app_user" required /></label>
        {:else if rType === 'webhook'}
          <label class="field grow"><span class="label">Webhook URL</span>
            <input class="input mono" bind:value={rUrl} placeholder="https://…/rotate" required /></label>
          <label class="field"><span class="label">HMAC key (write-only)</span>
            <input class="input mono" type="password" bind:value={rHmac} required /></label>
        {:else if rType === 'mysql'}
          <label class="field"><span class="label">Host:port</span>
            <input class="input mono" bind:value={rMyAddr} placeholder="db:3306" required /></label>
          <label class="field"><span class="label">Admin user</span>
            <input class="input mono" bind:value={rMyAdminUser} placeholder="rotator" required /></label>
          <label class="field"><span class="label">Admin password (write-only)</span>
            <input class="input mono" type="password" bind:value={rMyAdminPassword} /></label>
          <label class="field"><span class="label">Target user to rotate</span>
            <input class="input mono" bind:value={rMyUser} placeholder="app_user" required /></label>
          <label class="field"><span class="label">Target host</span>
            <input class="input mono" bind:value={rMyHost} placeholder="%" /></label>
          <label class="field"><span class="label">TLS mode</span>
            <select class="select" bind:value={rMyTls}><option value="">none</option><option value="true">true</option><option value="skip-verify">skip-verify</option><option value="preferred">preferred</option></select></label>
        {:else if rType === 'redis'}
          <label class="field"><span class="label">Host:port</span>
            <input class="input mono" bind:value={rRdAddr} placeholder="cache:6379" required /></label>
          <label class="field"><span class="label">Admin user</span>
            <input class="input mono" bind:value={rRdAdminUser} placeholder="default (leave blank for requirepass)" /></label>
          <label class="field"><span class="label">Admin password (write-only)</span>
            <input class="input mono" type="password" bind:value={rRdAdminPassword} /></label>
          <label class="field"><span class="label">Target ACL user</span>
            <input class="input mono" bind:value={rRdUser} placeholder="app_reader" required /></label>
          <label class="field grow"><span class="label">Preserve ACL rules (optional)</span>
            <input class="input mono" bind:value={rRdRules} placeholder="~app:* +@read" /></label>
          <label class="field check"><input type="checkbox" bind:checked={rRdTls} /> <span class="label">TLS</span></label>
          <label class="field check"><input type="checkbox" bind:checked={rRdSkipVerify} /> <span class="label">Skip TLS verify</span></label>
        {/if}
        {#if rError}<p class="error wide">{rError}</p>{/if}
        <div class="form-actions wide">
          <button class="btn btn-ghost" type="button" onclick={() => (showNewRotation = false)}>Cancel</button>
          <button class="btn btn-stamp" type="submit" disabled={!rConfigId || !rKey.trim()}>Create policy</button>
        </div>
      </form>
    {/if}

    <div class="sheet table-wrap">
      <table class="ledger" aria-label="Scheduled rotations">
        <thead>
          <tr><th scope="col">Secret</th><th scope="col" style="width:100px">Rotator</th><th scope="col">Config</th><th scope="col" style="width:100px">Cadence</th><th scope="col" style="width:130px">Last run</th><th scope="col" style="width:110px">Next</th><th scope="col" style="width:230px"></th></tr>
        </thead>
        <tbody>
          {#each rotations as r (r.id)}
            <tr class:failed={r.failure_count > 0} class:paused={r.status === 'paused'}>
              <td><span class="name mono">{r.secret_key}</span></td>
              <td><span class="pill pill-neutral">{r.type}</span></td>
              <td class="mono small">{registry.configLabel(r.config_id)}</td>
              <td class="folio">{interval(r.interval_seconds)}</td>
              <td>
                <span class="folio">{r.last_rotated_at ? relTime(r.last_rotated_at) : 'never'}</span>
                {#if r.failure_count > 0}<span class="stamp flat mini-stamp" title={r.last_error}>Failing</span>
                {:else if r.last_rotated_at}<span class="ok-mark" title="succeeded">✓</span>{/if}
              </td>
              <td class="folio">{r.status === 'paused' ? 'paused' : relTime(r.next_rotation_at)}</td>
              <td class="row-actions">
                <button class="btn btn-ghost btn-sm" onclick={() => toggleRuns('rot', r.id)}>Runs</button>
                <button class="btn btn-ghost btn-sm" onclick={() => toggleRotation(r)}>{r.status === 'paused' ? 'Resume' : 'Pause'}</button>
                <button class="btn btn-ghost btn-sm" onclick={() => rotateNow(r)}>Rotate now</button>
                <button class="btn btn-ghost btn-sm del-btn" onclick={() => removeRotation(r)}>✕</button>
              </td>
            </tr>
            {#if runsFor === `rot:${r.id}`}
              <tr class="runs-row"><td colspan="7">
                {#if !runs.length}<span class="folio">No runs recorded yet.</span>
                {:else}
                  <div class="runs">
                    {#each runs as run (run.id)}
                      <div class="run-line">
                        <span class="run-status" class:bad={run.status === 'failure'}>{run.status === 'success' ? '✓' : '✕'}</span>
                        <span class="folio">{relTime(run.started_at)} · attempt {run.attempt_num}</span>
                        {#if run.config_version}<span class="mono small">→ v{run.config_version}</span>{/if}
                        {#if run.error}<span class="run-err mono">{run.error}</span>{/if}
                      </div>
                    {/each}
                  </div>
                {/if}
              </td></tr>
            {/if}
          {:else}
            <tr><td colspan="7" class="empty folio">{loading ? 'Reading…' : 'No rotation policies yet.'}</td></tr>
          {/each}
        </tbody>
      </table>
    </div>
  </section>

  <!-- ══ sync ══ -->
  <section class="op-section rise" style="animation-delay: 110ms">
    <div class="section-head">
      <h3>Sync</h3>
      <button class="btn btn-sm" onclick={() => (showNewSync = !showNewSync)}>+ New target</button>
    </div>

    {#if showNewSync}
      <form class="sheet create-form" onsubmit={createSync}>
        <label class="field"><span class="label">Source config</span>
          <select class="select" bind:value={sConfigId} required>
            <option value="" disabled>choose…</option>
            {#each configOptions as o}<option value={o.id}>{o.label}</option>{/each}
          </select>
        </label>
        <label class="field"><span class="label">Provider</span>
          <select class="select" bind:value={sProvider}>
            <option value="github">GitHub Actions</option>
            <option value="k8s">Kubernetes</option>
            <option value="gitlab">GitLab CI/CD</option>
            <option value="aws_ssm">AWS SSM Parameter Store</option>
            <option value="cloudflare">Cloudflare Workers</option>
            <option value="aws_secrets">AWS Secrets Manager</option>
            <option value="vercel">Vercel</option>
            <option value="netlify">Netlify</option>
          </select></label>
        <label class="field"><span class="label">Every (minutes)</span>
          <input class="input" type="number" min="1" bind:value={sIntervalMin} /></label>
        {#if sProvider === 'github'}
          <label class="field"><span class="label">Owner</span><input class="input mono" bind:value={sOwner} placeholder="acme" required /></label>
          <label class="field"><span class="label">Repository</span><input class="input mono" bind:value={sRepo} placeholder="atlas-api" required /></label>
          <label class="field"><span class="label">Environment (optional)</span><input class="input mono" bind:value={sEnvName} placeholder="production" /></label>
          <label class="field grow"><span class="label">Personal access token (write-only)</span>
            <input class="input mono" type="password" bind:value={sPat} required /></label>
        {:else if sProvider === 'k8s'}
          <label class="field grow"><span class="label">API server URL</span>
            <input class="input mono" bind:value={sApiUrl} placeholder="https://k8s.internal:6443" required /></label>
          <label class="field"><span class="label">Namespace</span><input class="input mono" bind:value={sNamespace} placeholder="prod" required /></label>
          <label class="field"><span class="label">Secret name</span><input class="input mono" bind:value={sSecretName} placeholder="atlas-env" required /></label>
          <label class="field grow"><span class="label">Service-account token (write-only)</span>
            <input class="input mono" type="password" bind:value={sToken} required /></label>
          <label class="field wide"><span class="label">CA certificate PEM (required — the cluster's TLS is verified against it)</span>
            <textarea class="input mono" rows="3" bind:value={sCaCert} required placeholder="-----BEGIN CERTIFICATE-----"></textarea></label>
        {:else if sProvider === 'gitlab'}
          <label class="field grow"><span class="label">GitLab URL (optional)</span>
            <input class="input mono" bind:value={sGitlabUrl} placeholder="https://gitlab.com" /></label>
          <label class="field"><span class="label">Project</span>
            <input class="input mono" bind:value={sGlProject} placeholder="42 or group%2Fproject" required /></label>
          <label class="field"><span class="label">Environment scope (optional)</span>
            <input class="input mono" bind:value={sGlEnvScope} placeholder="*" /></label>
          <label class="field grow"><span class="label">Access token — api scope (write-only)</span>
            <input class="input mono" type="password" bind:value={sGlToken} required /></label>
        {:else if sProvider === 'aws_ssm'}
          <label class="field"><span class="label">AWS region</span>
            <input class="input mono" bind:value={sAwsRegion} placeholder="us-east-1" required /></label>
          <label class="field grow"><span class="label">Parameter path prefix</span>
            <input class="input mono" bind:value={sAwsPathPrefix} placeholder="/janus/atlas/prod" required /></label>
          <label class="field grow"><span class="label">Access key ID</span>
            <input class="input mono" bind:value={sAwsAccessKeyId} required /></label>
          <label class="field grow"><span class="label">Secret access key (write-only)</span>
            <input class="input mono" type="password" bind:value={sAwsSecretAccessKey} required /></label>
          <label class="field grow"><span class="label">Session token (optional, write-only)</span>
            <input class="input mono" type="password" bind:value={sAwsSessionToken} /></label>
        {:else if sProvider === 'cloudflare'}
          <label class="field"><span class="label">Account ID</span>
            <input class="input mono" bind:value={sCfAccountId} placeholder="a1b2c3d4…" required /></label>
          <label class="field"><span class="label">Worker script name</span>
            <input class="input mono" bind:value={sCfScriptName} placeholder="atlas-api" required /></label>
          <label class="field grow"><span class="label">API token — Workers Scripts Edit (write-only)</span>
            <input class="input mono" type="password" bind:value={sCfApiToken} required /></label>
        {:else if sProvider === 'vercel'}
          <label class="field"><span class="label">Project (id or name)</span>
            <input class="input mono" bind:value={sVcProject} placeholder="prj_… or atlas-api" required /></label>
          <label class="field"><span class="label">Team ID (optional)</span>
            <input class="input mono" bind:value={sVcTeamId} placeholder="team_…" /></label>
          <label class="field"><span class="label">Target environment</span>
            <select class="select" bind:value={sVcTarget}>
              <option value="production">production</option>
              <option value="preview">preview</option>
              <option value="development">development</option>
            </select></label>
          <label class="field grow"><span class="label">API token (write-only)</span>
            <input class="input mono" type="password" bind:value={sVcApiToken} required /></label>
        {:else if sProvider === 'netlify'}
          <label class="field"><span class="label">Account ID (or slug)</span>
            <input class="input mono" bind:value={sNfAccountId} placeholder="acct_… or my-team" required /></label>
          <label class="field"><span class="label">Site ID (optional)</span>
            <input class="input mono" bind:value={sNfSiteId} placeholder="site_… (omit for account-level)" /></label>
          <label class="field grow"><span class="label">Personal access token (write-only)</span>
            <input class="input mono" type="password" bind:value={sNfApiToken} required /></label>
        {:else}
          <label class="field"><span class="label">AWS region</span>
            <input class="input mono" bind:value={sSmRegion} placeholder="us-east-1" required /></label>
          <label class="field grow"><span class="label">Secret name prefix (billed per secret)</span>
            <input class="input mono" bind:value={sSmPathPrefix} placeholder="janus/atlas/prod" required /></label>
          <label class="field grow"><span class="label">Access key ID</span>
            <input class="input mono" bind:value={sSmAccessKeyId} required /></label>
          <label class="field grow"><span class="label">Secret access key (write-only)</span>
            <input class="input mono" type="password" bind:value={sSmSecretAccessKey} required /></label>
          <label class="field grow"><span class="label">Session token (optional, write-only)</span>
            <input class="input mono" type="password" bind:value={sSmSessionToken} /></label>
        {/if}
        {#if sError}<p class="error wide">{sError}</p>{/if}
        <div class="form-actions wide">
          <button class="btn btn-ghost" type="button" onclick={() => (showNewSync = false)}>Cancel</button>
          <button class="btn btn-stamp" type="submit" disabled={!sConfigId}>Create target</button>
        </div>
      </form>
    {/if}

    <div class="sheet table-wrap">
      <table class="ledger" aria-label="Sync integrations">
        <thead>
          <tr><th scope="col">Destination</th><th scope="col">Source config</th><th scope="col" style="width:110px">State</th><th scope="col" style="width:130px">Last sync</th><th scope="col" style="width:230px"></th></tr>
        </thead>
        <tbody>
          {#each syncs as s (s.id)}
            <tr class:paused={s.status === 'paused'}>
              <td>
                <span class="pill pill-neutral dest-pill">{providerLabel(s.provider)}</span>
                <span class="mono small">{destLabel(s)}</span>
              </td>
              <td class="mono small">{registry.configLabel(s.config_id)}</td>
              <td>
                {#if s.failure_count > 0}<span class="state bad" title={s.last_error}>error</span>
                {:else if s.status === 'paused'}<span class="state warn">paused</span>
                {:else}<span class="state ok">in sync</span>{/if}
              </td>
              <td class="folio">{s.last_synced_at ? relTime(s.last_synced_at) : 'never'}</td>
              <td class="row-actions">
                <button class="btn btn-ghost btn-sm" onclick={() => toggleRuns('sync', s.id)}>Runs</button>
                <button class="btn btn-ghost btn-sm" onclick={() => toggleSync(s)}>{s.status === 'paused' ? 'Resume' : 'Pause'}</button>
                <button class="btn btn-ghost btn-sm" onclick={() => syncNow(s)}>Push now</button>
                <button class="btn btn-ghost btn-sm del-btn" onclick={() => removeSync(s)}>✕</button>
              </td>
            </tr>
            {#if runsFor === `sync:${s.id}`}
              <tr class="runs-row"><td colspan="5">
                {#if !runs.length}<span class="folio">No runs recorded yet.</span>
                {:else}
                  <div class="runs">
                    {#each runs as run (run.id)}
                      <div class="run-line">
                        <span class="run-status" class:bad={run.status === 'failure'}>{run.status === 'success' ? '✓' : '✕'}</span>
                        <span class="folio">{relTime(run.started_at)} · attempt {run.attempt_num}{run.keys_count != null ? ` · ${run.keys_count} keys` : ''}</span>
                        {#if run.error}<span class="run-err mono">{run.error}</span>{/if}
                      </div>
                    {/each}
                  </div>
                {/if}
              </td></tr>
            {/if}
          {:else}
            <tr><td colspan="5" class="empty folio">{loading ? 'Reading…' : 'No sync targets yet.'}</td></tr>
          {/each}
        </tbody>
      </table>
    </div>
  </section>

  <!-- ══ dynamic ══ -->
  <section class="op-section rise" style="animation-delay: 170ms">
    <div class="section-head">
      <h3>Dynamic credentials</h3>
      <button class="btn btn-sm" onclick={() => (showNewRole = !showNewRole)}>+ New role</button>
    </div>

    {#if issued}
      <div class="sheet issued">
        <span class="stamp ok flat">Issued — shown exactly once</span>
        <div class="issued-grid">
          <span class="folio">Username</span><code class="mono">{issued.username}</code>
          <span class="folio">Password</span><code class="mono pw">{issued.password}</code>
          <span class="folio">Expires</span><span>{relTime(issued.expires_at)}</span>
        </div>
        <div>
          <button class="btn btn-sm" onclick={() => navigator.clipboard.writeText(`${issued!.username}\n${issued!.password}`)}>Copy</button>
          <button class="btn btn-sm btn-ghost" onclick={() => (issued = null)}>Dismiss</button>
        </div>
      </div>
    {/if}

    {#if showNewRole}
      <form class="sheet create-form" onsubmit={createRole}>
        <label class="field"><span class="label">Config scope</span>
          <select class="select" bind:value={dConfigId} required>
            <option value="" disabled>choose…</option>
            {#each configOptions as o}<option value={o.id}>{o.label}</option>{/each}
          </select>
        </label>
        <label class="field"><span class="label">Role name</span><input class="input mono" bind:value={dName} placeholder="report-reader" required /></label>
        <label class="field"><span class="label">Default TTL (min)</span><input class="input" type="number" min="1" bind:value={dTtlMin} /></label>
        <label class="field"><span class="label">Max TTL (min)</span><input class="input" type="number" min="1" bind:value={dMaxTtlMin} /></label>
        <label class="field wide"><span class="label">Admin DSN (write-only)</span>
          <input class="input mono" type="password" bind:value={dAdminDsn} placeholder="postgres://admin:…@db:5432/app" required /></label>
        <label class="field wide"><span class="label">Creation SQL — templates: name, password, expiration in double braces</span>
          <textarea class="input mono" rows="3" bind:value={dCreate}></textarea></label>
        <label class="field wide"><span class="label">Revocation SQL</span>
          <textarea class="input mono" rows="2" bind:value={dRevoke}></textarea></label>
        {#if dError}<p class="error wide">{dError}</p>{/if}
        <div class="form-actions wide">
          <button class="btn btn-ghost" type="button" onclick={() => (showNewRole = false)}>Cancel</button>
          <button class="btn btn-stamp" type="submit" disabled={!dConfigId || !dName.trim()}>Create role</button>
        </div>
      </form>
    {/if}

    <div class="sheet table-wrap">
      <table class="ledger" aria-label="Dynamic secret roles">
        <thead>
          <tr><th scope="col">Role</th><th scope="col">Config</th><th scope="col" style="width:150px">TTL / max</th><th scope="col" style="width:200px"></th></tr>
        </thead>
        <tbody>
          {#each roles as role (role.id)}
            <tr>
              <td><span class="name mono">{role.name}</span></td>
              <td class="mono small">{registry.configLabel(role.config_id)}</td>
              <td class="folio">{Math.round(role.default_ttl_seconds / 60)}m / {Math.round(role.max_ttl_seconds / 60)}m</td>
              <td class="row-actions">
                <button class="btn btn-ghost btn-sm" onclick={() => issue(role)}>Issue credentials</button>
                <button class="btn btn-ghost btn-sm del-btn" onclick={() => removeRole(role)}>✕</button>
              </td>
            </tr>
          {:else}
            <tr><td colspan="4" class="empty folio">{loading ? 'Reading…' : 'No dynamic roles yet.'}</td></tr>
          {/each}
        </tbody>
      </table>
    </div>

    <div class="sheet table-wrap" style="margin-top: var(--s3)">
      <table class="ledger" aria-label="Active dynamic leases">
        <thead>
          <tr><th scope="col" style="width:140px">Lease</th><th scope="col">Role</th><th scope="col" style="width:170px">DB user</th><th scope="col" style="width:220px">Time to live</th><th scope="col" style="width:130px"></th></tr>
        </thead>
        <tbody>
          {#each leases as l (l.id)}
            <tr class:revoked={l.status === 'revoked'}>
              <td class="mono small">{l.id.slice(0, 14)}…</td>
              <td class="mono small">{roleName(l.role_id)}</td>
              <td class="mono small">{l.db_username}</td>
              <td>
                {#if l.status === 'revoked'}
                  <span class="stamp flat mini-stamp">Revoked</span>
                {:else if l.status === 'expired'}
                  <span class="folio">expired {relTime(l.expires_at)}</span>
                {:else}
                  <div class="ttl">
                    <div class="ttl-bar" class:hot={ttlPct(l) > 75}>
                      <span style={`width: ${ttlPct(l)}%`}></span>
                    </div>
                    <span class="folio">expires {relTime(l.expires_at)}</span>
                  </div>
                {/if}
              </td>
              <td class="row-actions">
                {#if l.status === 'active' || l.status === 'creating'}
                  <button class="btn btn-ghost btn-sm" onclick={() => renew(l)}>Renew</button>
                  <button class="btn btn-ghost btn-sm del-btn" onclick={() => revoke(l)}>Revoke</button>
                {/if}
              </td>
            </tr>
          {:else}
            <tr><td colspan="5" class="empty folio">{loading ? 'Reading…' : 'No leases — issue credentials from a role above.'}</td></tr>
          {/each}
        </tbody>
      </table>
    </div>
  </section>
</div>

<style>
  .page-n { max-width: 1240px; margin: 0 auto; }
  .page-head { display: flex; justify-content: space-between; align-items: flex-end; gap: var(--s4); }
  .page-head h1 { margin-top: var(--s1); }
  .error { color: var(--vermilion); font-size: var(--text-sm); margin-top: var(--s3); }

  .op-section { margin-top: var(--s6); }
  .section-head { display: flex; justify-content: space-between; align-items: baseline; margin-bottom: var(--s3); }
  .table-wrap { overflow-x: auto; }

  .create-form {
    display: grid;
    grid-template-columns: repeat(3, 1fr);
    gap: var(--s3) var(--s4);
    padding: var(--s4) var(--s5);
    margin-bottom: var(--s3);
    border-left: 4px solid var(--vermilion);
  }
  .field { display: flex; flex-direction: column; gap: var(--s1); min-width: 0; }
  .field.grow { grid-column: span 2; }
  .wide { grid-column: 1 / -1; }
  .form-actions { display: flex; justify-content: flex-end; gap: var(--s3); }
  .create-form textarea { resize: vertical; }

  .name { font-weight: 600; font-size: var(--text-sm); }
  .small { font-size: var(--text-xs); color: var(--ink-soft); }
  .mini-stamp { font-size: 0.55rem; padding: 0.08rem 0.35rem; transform: rotate(-5deg); display: inline-block; }
  .ok-mark { color: var(--verdigris); font-weight: 700; margin-left: 0.3rem; }
  tr.failed td { background: var(--vermilion-wash); }
  tr.paused td { opacity: 0.6; }

  .runs-row td { background: var(--archivist-wash); }
  .runs { display: flex; flex-direction: column; gap: var(--s1); padding: var(--s2) var(--s3); }
  .run-line { display: flex; align-items: center; gap: var(--s3); font-size: var(--text-sm); }
  .run-status { color: var(--verdigris); font-weight: 700; }
  .run-status.bad { color: var(--vermilion); }
  .run-err { font-size: var(--text-xs); color: var(--vermilion); }

  .dest-pill { margin-right: var(--s2); }
  .state { font-size: var(--text-xs); font-weight: 700; text-transform: uppercase; letter-spacing: 0.08em; }
  .state.ok { color: var(--verdigris); }
  .state.warn { color: var(--ochre); }
  .state.bad { color: var(--vermilion); }

  .issued {
    display: flex;
    align-items: center;
    gap: var(--s5);
    flex-wrap: wrap;
    padding: var(--s4) var(--s5);
    margin-bottom: var(--s3);
    border-left: 4px solid var(--verdigris);
  }
  .issued-grid {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: var(--s1) var(--s3);
    align-items: baseline;
  }
  .issued code { font-size: var(--text-sm); word-break: break-all; }
  .issued .pw { color: var(--vermilion); font-weight: 600; }

  tr.revoked td { opacity: 0.55; }
  .ttl { display: flex; flex-direction: column; gap: 3px; }
  .ttl-bar {
    height: 5px;
    background: var(--paper-low);
    border: 1px solid var(--rule);
    border-radius: 3px;
    overflow: hidden;
  }
  .ttl-bar span { display: block; height: 100%; background: var(--verdigris); }
  .ttl-bar.hot span { background: var(--ochre); }
  .row-actions { text-align: right; white-space: nowrap; }
  .del-btn:hover { color: var(--vermilion); }
  .empty { text-align: center; padding: var(--s6) !important; }

  @media (max-width: 900px) { .create-form { grid-template-columns: 1fr; } .field.grow { grid-column: auto; } }
</style>
