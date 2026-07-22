<script lang="ts">
  import { api, memberScopePath, errorMessage, type UserInfo, type ApiMember, type Role } from '../lib/api'
  import { registry } from '../lib/registry.svelte'
  import { dialog } from '../lib/dialog.svelte'
  import { relTime } from '../lib/util'

  type ScopeKind = 'instance' | 'project' | 'environment'

  let scopeKind = $state<ScopeKind>('instance')
  let pid = $state('')
  let eid = $state('')

  let users = $state<UserInfo[]>([])
  let members = $state<ApiMember[]>([])
  let loading = $state(true)
  let error = $state('')

  let inviting = $state(false)
  let inviteEmail = $state('')
  let invited = $state<{ email: string; password: string } | null>(null)
  let inviteError = $state('')

  const roleRank: Record<Role, number> = { viewer: 0, developer: 1, admin: 2, owner: 3 }

  const scopePath = $derived.by(() => {
    if (scopeKind === 'instance') return memberScopePath({ kind: 'instance' })
    if (scopeKind === 'project') return pid ? memberScopePath({ kind: 'project', pid }) : null
    return pid && eid ? memberScopePath({ kind: 'environment', pid, eid }) : null
  })

  const envOptions = $derived(registry.findProject(pid)?.environments ?? [])

  $effect(() => {
    if (!pid && registry.projects.length) pid = registry.projects[0].id
  })
  $effect(() => {
    if (scopeKind === 'environment' && !envOptions.some(e => e.id === eid)) eid = envOptions[0]?.id ?? ''
  })

  $effect(() => {
    api.listUsers().then(us => (users = us)).catch(() => (users = []))
  })

  $effect(() => {
    if (scopePath) void load(scopePath)
    else members = []
  })

  async function load(path: string) {
    loading = true
    error = ''
    try {
      members = await api.listScopedMembers(path)
    } catch (err) {
      error = errorMessage(err, 'Could not list members for this scope.')
      members = []
    } finally {
      loading = false
    }
  }

  const email = (uid: string) => users.find(u => u.id === uid)?.email ?? `${uid.slice(0, 8)}…`

  interface Row { user: UserInfo; role: Role | null }
  const rows = $derived.by((): Row[] => {
    const roleByUser = new Map(members.map(m => [m.user_id, m.role]))
    return users
      .filter(u => !u.disabled)
      .map(u => ({ user: u, role: roleByUser.get(u.id) ?? null }))
      .sort((a, b) => (roleRank[b.role ?? 'viewer'] ?? -1) - (roleRank[a.role ?? 'viewer'] ?? -1))
  })
  /* bindings whose user we can't resolve (e.g. non-admin listUsers) still show */
  const orphanMembers = $derived(members.filter(m => !users.some(u => u.id === m.user_id)))

  async function invite(e: SubmitEvent) {
    e.preventDefault()
    inviteError = ''
    try {
      const res = await api.createUser(inviteEmail.trim())
      invited = { email: res.email, password: res.password }
      inviteEmail = ''
      users = await api.listUsers().catch(() => users)
    } catch (err) {
      inviteError = errorMessage(err, 'Could not create the user.')
    }
  }

  async function setRole(uid: string, role: Role) {
    if (!scopePath) return
    error = ''
    try {
      await api.putScopedMember(scopePath, uid, role)
      await load(scopePath)
    } catch (err) {
      error = errorMessage(err, 'Role change failed.')
    }
  }

  async function removeBinding(uid: string) {
    if (!scopePath) return
    const ok = await dialog.confirm({
      title: `Remove ${email(uid)}'s ${scopeKind} binding?`,
      body: 'They keep any bindings at other scopes; access unions most-permissively.',
      confirmLabel: 'Remove binding',
      danger: true,
    })
    if (!ok) return
    error = ''
    try {
      await api.deleteScopedMember(scopePath, uid)
      await load(scopePath)
    } catch (err) {
      error = errorMessage(err, 'Remove failed.')
    }
  }

  async function unlock(u: UserInfo) {
    const window = u.locked_until ? ` (auto-unlocks ${relTime(u.locked_until)})` : ''
    const ok = await dialog.confirm({
      title: `Unlock ${u.email}?`,
      body: `Clears the temporary lockout so this account can sign in again immediately${window}.`,
      confirmLabel: 'Unlock account',
      danger: true,
    })
    if (!ok) return
    error = ''
    try {
      await api.unlockUser(u.id)
      users = await api.listUsers().catch(() => users)
    } catch (err) {
      error = errorMessage(err, 'Unlock failed.')
    }
  }

  const scopeLabel = $derived(
    scopeKind === 'instance'
      ? 'instance'
      : scopeKind === 'project'
        ? registry.findProject(pid)?.name ?? '…'
        : `${registry.findProject(pid)?.name ?? '…'} / ${envOptions.find(e => e.id === eid)?.slug ?? '…'}`,
  )
</script>

<div class="page-n">
  <header class="page-head rise">
    <div>
      <p class="folio">Office · deny-by-default RBAC · viewer ⊂ developer ⊂ admin ⊂ owner · top-down inheritance</p>
      <h1>Members</h1>
    </div>
    <button class="btn btn-primary" onclick={() => { inviting = !inviting; invited = null }}>+ Invite member</button>
  </header>
  <hr class="ledger-rule" />

  <div class="scope-bar rise" style="animation-delay: 40ms">
    <div class="seg" role="group" aria-label="Binding scope">
      {#each ['instance', 'project', 'environment'] as k}
        <button class="seg-btn" class:on={scopeKind === k} onclick={() => (scopeKind = k as ScopeKind)}>{k}</button>
      {/each}
    </div>
    {#if scopeKind !== 'instance'}
      <select class="select" bind:value={pid}>
        {#each registry.projects as p}<option value={p.id}>{p.name}</option>{/each}
      </select>
    {/if}
    {#if scopeKind === 'environment'}
      <select class="select" bind:value={eid}>
        {#each envOptions as e}<option value={e.id}>{e.slug}</option>{/each}
      </select>
    {/if}
    <span class="folio">bindings at: <strong>{scopeLabel}</strong></span>
  </div>

  {#if inviting}
    <div class="sheet invite rise">
      {#if invited}
        <div class="minted">
          <span class="stamp ok flat">Created — password shown exactly once</span>
          <code class="mono once">{invited.email}</code>
          <code class="mono once pw">{invited.password}</code>
          <button class="btn btn-sm" onclick={() => navigator.clipboard.writeText(invited!.password)}>Copy password</button>
          <button class="btn btn-sm btn-ghost" onclick={() => { inviting = false; invited = null }}>Done</button>
        </div>
      {:else}
        <form onsubmit={invite}>
          <div class="field grow">
            <label class="label" for="inv-email">Email</label>
            <input id="inv-email" class="input" type="email" bind:value={inviteEmail} placeholder="new@company.dev" required />
          </div>
          <button class="btn btn-stamp" type="submit" disabled={!inviteEmail.trim()}>Create user</button>
          {#if inviteError}<p class="error">{inviteError}</p>{/if}
        </form>
      {/if}
    </div>
  {/if}

  {#if error}<p class="error rise">{error}</p>{/if}

  <div class="sheet table-wrap rise" style="animation-delay: 80ms">
    <table class="ledger">
      <thead>
        <tr>
          <th>Member</th>
          <th style="width: 220px">Role at {scopeKind}</th>
          <th style="width: 200px">Change</th>
          <th style="width: 110px"></th>
        </tr>
      </thead>
      <tbody>
        {#each rows as row (row.user.id)}
          <tr>
            <td class="who">
              <span class="avatar">{row.user.email.slice(0, 2).toUpperCase()}</span>
              <span class="m-name">{row.user.email}</span>
              {#if row.user.locked}
                <span class="pill pill-locked" title={row.user.locked_until ? `Auto-unlocks ${relTime(row.user.locked_until)}` : 'Temporarily locked'}>Locked</span>
              {/if}
            </td>
            <td>
              {#if row.role}
                <span class="role role-{row.role}">{row.role}</span>
                {#if row.role === 'owner' && scopeKind === 'instance'}<span class="folio guard">never-lock-out</span>{/if}
              {:else}
                <span class="folio">no {scopeKind} binding</span>
              {/if}
            </td>
            <td>
              <select class="select" value={row.role ?? ''} onchange={(e) => setRole(row.user.id, (e.currentTarget as HTMLSelectElement).value as Role)}>
                <option value="" disabled>set role…</option>
                <option value="viewer">viewer</option>
                <option value="developer">developer</option>
                <option value="admin">admin</option>
                <option value="owner">owner</option>
              </select>
            </td>
            <td class="row-actions">
              {#if row.user.locked}
                <button class="btn btn-ghost btn-sm unlock-btn" onclick={() => unlock(row.user)}>Unlock</button>
              {/if}
              {#if row.role}
                <button class="btn btn-ghost btn-sm del-btn" onclick={() => removeBinding(row.user.id)}>Remove</button>
              {/if}
            </td>
          </tr>
        {/each}
        {#each orphanMembers as m (m.user_id)}
          <tr>
            <td class="who"><span class="avatar">?</span><span class="m-name mono">{m.user_id.slice(0, 8)}…</span></td>
            <td><span class="role role-{m.role}">{m.role}</span></td>
            <td></td>
            <td class="row-actions">
              <button class="btn btn-ghost btn-sm del-btn" onclick={() => removeBinding(m.user_id)}>Remove</button>
            </td>
          </tr>
        {/each}
        {#if !rows.length && !orphanMembers.length}
          <tr><td colspan="4" class="empty folio">{loading ? 'Reading…' : 'No members visible for this scope.'}</td></tr>
        {/if}
      </tbody>
    </table>
  </div>

  <p class="foot-note folio">
    An instance binding applies everywhere; a project binding covers that project's environments
    and configs; roles union most-permissively. You cannot grant a role above your own, and the
    last instance owner can never be removed.
  </p>
</div>

<style>
  .page-n { max-width: 1100px; margin: 0 auto; }
  .page-head { display: flex; justify-content: space-between; align-items: flex-end; gap: var(--s4); }
  .page-head h1 { margin-top: var(--s1); }

  .scope-bar { display: flex; align-items: center; gap: var(--s3); margin-top: var(--s4); flex-wrap: wrap; }
  .scope-bar .select { max-width: 200px; }
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

  .invite { padding: var(--s4) var(--s5); margin-top: var(--s4); border-left: 4px solid var(--vermilion); }
  .invite form { display: flex; align-items: flex-end; gap: var(--s4); flex-wrap: wrap; }
  .field { display: flex; flex-direction: column; gap: var(--s2); }
  .field.grow { flex: 1; min-width: 220px; }
  .error { color: var(--vermilion); font-size: var(--text-sm); width: 100%; margin-top: var(--s3); }
  .minted { display: flex; align-items: center; gap: var(--s3); flex-wrap: wrap; }
  .once {
    background: var(--paper-low);
    border: 1px dashed var(--rule-strong);
    border-radius: var(--radius);
    padding: var(--s1) var(--s3);
    font-size: var(--text-xs);
  }
  .once.pw { color: var(--vermilion); font-weight: 600; }

  .table-wrap { overflow-x: auto; margin-top: var(--s4); }

  .who { display: flex; align-items: center; gap: var(--s3); }
  .avatar {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 34px; height: 34px;
    border-radius: 50%;
    border: 1.5px solid var(--ink);
    font-weight: 700;
    font-size: 0.7rem;
    letter-spacing: 0.04em;
    background: var(--paper-low);
    flex: none;
  }
  .m-name { font-weight: 620; }

  .role {
    font-size: var(--text-xs);
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.1em;
  }
  .role-owner { color: var(--vermilion); }
  .role-admin { color: var(--archivist); }
  .role-developer { color: var(--verdigris); }
  .role-viewer { color: var(--ink-faint); }
  .guard { display: block; font-size: 0.58rem; }

  .select { max-width: 180px; }
  .row-actions { text-align: right; display: flex; gap: var(--s2); justify-content: flex-end; }
  .del-btn:hover { color: var(--vermilion); }
  .unlock-btn { color: var(--vermilion); }
  .unlock-btn:hover { color: var(--vermilion); text-decoration: underline; }

  .pill-locked {
    color: var(--vermilion);
    background: var(--vermilion-wash);
    margin-left: var(--s2);
  }
  .empty { text-align: center; padding: var(--s6) !important; }
  .foot-note { margin-top: var(--s3); max-width: 72ch; }
</style>
